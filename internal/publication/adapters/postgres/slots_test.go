package postgres_test

import (
	"cmp"
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/publication/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

var (
	publisher = domain.Principal{Issuer: "https://issuer.example", Subject: "publisher"}
	// start is the instant the slots of these tests count their minutes from.
	start = time.Date(2026, 11, 11, 9, 0, 0, 123456000, time.UTC)
)

// at is the instant, so many minutes after the start.
func at(minutes int) time.Time { return start.Add(time.Duration(minutes) * time.Minute) }

// slotStore is a migrated database opened the way a Fabric process opens it,
// as the application role, with the store of publication on it.
type slotStore struct {
	t        *testing.T
	database pgtest.Database
	fabric   *db.Database
	store    *postgres.Store
	product  string
}

func newSlotStore(t *testing.T) slotStore {
	t.Helper()

	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(fabric.Close)
	return slotStore{t: t, database: database, fabric: fabric, store: postgres.NewStore(fabric.Framework), product: ids.NewUUID()}
}

// within runs fn in a transaction that must commit.
func (f slotStore) within(fn func(ctx context.Context) error) {
	f.t.Helper()

	if err := f.fabric.WithinTransaction(f.t.Context(), fn); err != nil {
		f.t.Fatalf("transaction: %v", err)
	}
}

func (f slotStore) target(product, portal string) domain.Target {
	f.t.Helper()

	target := domain.Target{
		ID: ids.NewUUID(), ProductID: product, AudienceConfigID: ids.NewUUID(), PortalKey: portal,
		Audience: "internal", Environment: "sandbox", CreatedAt: start,
	}
	f.within(func(ctx context.Context) error {
		_, _, err := f.store.PutTarget(ctx, target)
		return err
	})
	return target
}

// digest is the key an operation of a slot is made under: one for each.
func digest(slot string, generation int64) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s/%d", slot, generation))))
}

// publish makes the slot of the asset on the target, as the first operation
// of the slot does: a release becomes active at the instant. The slot is
// given the identifier, which decides its place among those of one instant.
func (f slotStore) publish(target domain.Target, asset, id string, instant time.Time) domain.Slot {
	f.t.Helper()

	slot := domain.Slot{ID: id, ProductID: target.ProductID, TargetID: target.ID, AssetID: asset}
	op := domain.Operation{
		ID: ids.NewUUID(), SlotID: id, Generation: 1, Action: domain.Activate, ReleaseID: ids.NewUUID(), By: publisher, At: instant,
	}
	f.within(func(ctx context.Context) error {
		if err := f.store.EnsureSlot(ctx, slot); err != nil {
			return err
		}
		return f.store.Apply(ctx, slot, op, digest(id, 1))
	})
	slot.Generation, slot.ActiveReleaseID = 1, op.ReleaseID
	return slot
}

// operate does something to a slot that took its first operation already: the
// next generation of the slot.
func (f slotStore) operate(slot domain.Slot, action domain.Action, instant time.Time) domain.Slot {
	f.t.Helper()

	op := domain.Operation{
		ID: ids.NewUUID(), SlotID: slot.ID, Generation: slot.Generation + 1, Action: action,
		PreviousReleaseID: slot.ActiveReleaseID, By: publisher, At: instant,
	}
	if action == domain.Withdraw {
		op.Reason = "taken down"
	} else {
		op.Reason, op.ReleaseID = "back again", ids.NewUUID()
	}
	f.within(func(ctx context.Context) error {
		return f.store.Apply(ctx, slot, op, digest(slot.ID, op.Generation))
	})
	slot.Generation, slot.ActiveReleaseID = op.Generation, op.ReleaseID
	return slot
}

// listing reads a page of a list: behind the position, of at most that many.
type listing func(after *ports.SlotPosition, limit int) ([]ports.PlacedSlot, error)

// walkSlots reads a list to its end, a page of limit slots at a time, each page
// behind the last slot of the one before. It returns the pages that held
// something.
func walkSlots(t *testing.T, limit int, read listing) [][]ports.PlacedSlot {
	t.Helper()

	var (
		pages [][]ports.PlacedSlot
		after *ports.SlotPosition
	)
	for len(pages) < 1000 {
		slots, err := read(after, limit)
		if err != nil {
			t.Fatalf("read a page: %v", err)
		}
		if len(slots) > limit {
			t.Fatalf("a page of at most %d slots holds %d", limit, len(slots))
		}
		if len(slots) == 0 {
			return pages
		}
		pages = append(pages, slots)
		position := slots[len(slots)-1].Position()
		after = &position
	}
	t.Fatal("the list does not end")
	return nil
}

// inOrder checks that a list read in pages of every size is the slots wanted,
// each once, in pages that are full but for the last.
func inOrder(t *testing.T, what string, want []string, read listing) {
	t.Helper()

	for _, limit := range []int{1, 2, 3, len(want), len(want) + 1, 100} {
		if limit < 1 {
			continue
		}
		pages := walkSlots(t, limit, read)
		var got []string
		for index, page := range pages {
			for _, placed := range page {
				got = append(got, placed.Slot.ID)
			}
			if index < len(pages)-1 && len(page) != limit {
				t.Errorf("%s, in pages of %d: page %d holds %d slots and is not the last", what, limit, index+1, len(page))
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s, in pages of %d:\n %v\nwant\n %v", what, limit, got, want)
		}
		if wantPages := (len(want) + limit - 1) / limit; len(pages) != wantPages {
			t.Errorf("%s, in pages of %d: %d pages hold something, want %d", what, limit, len(pages), wantPages)
		}
	}
}

// of is the slots read by the identifier.
func of(slots ...domain.Slot) []string {
	listed := make([]string, 0, len(slots))
	for _, slot := range slots {
		listed = append(listed, slot.ID)
	}
	return listed
}

const (
	lowest  = "00000000-0000-4000-8000-000000000000"
	highest = "ffffffff-ffff-4fff-bfff-ffffffffffff"
)

// A list of slots is read a page at a time, oldest first by the instant the
// first operation of a slot was accepted, and what took its first operation
// in one instant in the order of the identifiers of the slots. Whatever the
// size of the page, every slot is read once, and every page but the last is
// full. The slots are made in an order that is none of the orders they are
// listed in, so that the order of the rows of the table is no help.
func TestTheSlotsOfATargetAreListedOldestFirstAPageAtATime(t *testing.T) {
	f := newSlotStore(t)
	target := f.target(f.product, "engineering")

	type made struct {
		id, asset string
		minute    int
	}
	order := []made{
		{ids.NewUUID(), ids.NewUUID(), 30}, {"ffffffff-ffff-4fff-bfff-fffffffffff1", ids.NewUUID(), 10},
		{ids.NewUUID(), ids.NewUUID(), 50}, {"00000000-0000-4000-8000-000000000001", ids.NewUUID(), 10},
		{ids.NewUUID(), ids.NewUUID(), 20}, {ids.NewUUID(), ids.NewUUID(), 40}, {ids.NewUUID(), ids.NewUUID(), 5},
	}
	slots := map[string]domain.Slot{}
	var assets []string
	for _, m := range order {
		slots[m.id] = f.publish(target, m.asset, m.id, at(m.minute))
		assets = append(assets, m.asset)
	}
	oldestFirst := slices.Clone(order)
	slices.SortFunc(oldestFirst, func(a, b made) int { return cmp.Or(cmp.Compare(a.minute, b.minute), strings.Compare(a.id, b.id)) })
	var want []string
	for _, m := range oldestFirst {
		want = append(want, m.id)
	}

	read := func(after *ports.SlotPosition, limit int) ([]ports.PlacedSlot, error) {
		return f.store.Slots(t.Context(), target, assets, after, limit)
	}
	inOrder(t, "the slots of the target", want, read)

	// What is listed is the slot as it is stored, with the target it is on and
	// the instant of its first operation, in UTC to the microsecond.
	first, err := read(nil, 100)
	if err != nil || len(first) != len(order) {
		t.Fatalf("read the list: %d slots, %v", len(first), err)
	}
	for index, placed := range first {
		if placed.Slot != slots[want[index]] || placed.Target.ID != target.ID || placed.Target.PortalKey != "engineering" ||
			!placed.Since.Equal(at(oldestFirst[index].minute)) || placed.Since.Location() != time.UTC {
			t.Errorf("slot %d of the list is %+v on %+v since %v, want %+v at minute %d", index, placed.Slot, placed.Target, placed.Since,
				slots[want[index]], oldestFirst[index].minute)
		}
	}
	// A slot is on one target only: another target, with a slot of an asset
	// that is not named here, lists none of these.
	preview := f.target(f.product, "preview")
	f.publish(preview, ids.NewUUID(), ids.NewUUID(), at(1))
	if got, err := f.store.Slots(t.Context(), preview, assets, nil, 100); err != nil || len(got) != 0 {
		t.Errorf("a target whose slots are none of the assets named lists %d slots (%v)", len(got), err)
	}
}

// The assets a caller reads are a condition of the statement, ahead of the
// position and of the LIMIT: the slots of the others are in nobody's page,
// and so they cannot leave a page short. An empty set is no asset.
func TestTheSlotsOfATargetAreOnlyThoseOfTheAssetsNamed(t *testing.T) {
	f := newSlotStore(t)
	target, preview := f.target(f.product, "engineering"), f.target(f.product, "preview")
	var slots []domain.Slot
	for minute := 1; minute <= 6; minute++ {
		slots = append(slots, f.publish(target, ids.NewUUID(), ids.NewUUID(), at(minute)))
	}
	// The slot of a named asset on another target is not a slot of this one.
	f.publish(preview, slots[1].AssetID, ids.NewUUID(), at(7))

	named := []string{slots[1].AssetID, slots[2].AssetID, slots[4].AssetID, ids.NewUUID()}
	read := func(after *ports.SlotPosition, limit int) ([]ports.PlacedSlot, error) {
		return f.store.Slots(t.Context(), target, named, after, limit)
	}
	inOrder(t, "the slots of three assets among six", of(slots[1], slots[2], slots[4]), read)
	pages := walkSlots(t, 2, read)
	if len(pages) != 2 || len(pages[0]) != 2 || len(pages[1]) != 1 {
		t.Errorf("in pages of two the three slots come in %d pages, the first holding two", len(pages))
	}

	for name, none := range map[string][]string{"an empty set": {}, "no set": nil} {
		if got, err := f.store.Slots(t.Context(), target, none, nil, 100); err != nil || len(got) != 0 {
			t.Errorf("%s of assets lists %d slots (%v), want none", name, len(got), err)
		}
	}
}

// The slots of a target are those of the target under the product that has
// it. A target that is named under another product is the target of no slot,
// and lists none, though the identifier of the target is one that has slots.
func TestTheSlotsOfATargetAreNotListedUnderAnotherProduct(t *testing.T) {
	f := newSlotStore(t)
	target := f.target(f.product, "engineering")
	slot := f.publish(target, ids.NewUUID(), ids.NewUUID(), at(10))
	named := []string{slot.AssetID}

	if got, err := f.store.Slots(t.Context(), target, named, nil, 100); err != nil || !slices.Equal(placedIDs(got), of(slot)) {
		t.Fatalf("the target lists %v (%v), want its slot %s", placedIDs(got), err, slot.ID)
	}
	misplaced := target
	misplaced.ProductID = ids.NewUUID()
	if got, err := f.store.Slots(t.Context(), misplaced, named, nil, 100); err != nil || len(got) != 0 {
		t.Errorf("the target named under another product lists %v (%v), want none", placedIDs(got), err)
	}
}

// A page starts behind a position, wherever the slots around that position
// are by then. What took its first operation behind it is read by a later
// page; what took it before is not; and a slot that was changed since keeps
// its place, the one at the position included.
func TestAPageOfSlotsStartsBehindAPositionWhateverChangedAroundIt(t *testing.T) {
	f := newSlotStore(t)
	target := f.target(f.product, "engineering")
	var assets []string
	publish := func(minute int) domain.Slot {
		asset := ids.NewUUID()
		assets = append(assets, asset)
		return f.publish(target, asset, ids.NewUUID(), at(minute))
	}
	var slots []domain.Slot
	for minute := 10; minute <= 50; minute += 10 {
		slots = append(slots, publish(minute))
	}
	read := func(after *ports.SlotPosition, limit int) []ports.PlacedSlot {
		t.Helper()

		placed, err := f.store.Slots(t.Context(), target, assets, after, limit)
		if err != nil {
			t.Fatalf("list the slots: %v", err)
		}
		return placed
	}

	page := read(nil, 2)
	if got := []string{page[0].Slot.ID, page[1].Slot.ID}; !slices.Equal(got, of(slots[0], slots[1])) {
		t.Fatalf("the first page is %v, want %v", got, of(slots[0], slots[1]))
	}
	position := page[1].Position()

	// Between the two pages: the slot at the position is withdrawn, the one
	// behind it is withdrawn and returned to a release, and one slot is made
	// before the position, one right behind it and one at the end.
	slots[1] = f.operate(slots[1], domain.Withdraw, at(61))
	slots[2] = f.operate(f.operate(slots[2], domain.Withdraw, at(62)), domain.Restore, at(63))
	early, next, end := publish(15), publish(25), publish(60)

	if rest := read(&position, 10); !slices.Equal(placedIDs(rest), of(next, slots[2], slots[3], slots[4], end)) {
		t.Errorf("behind the position the list holds %v, want %v", placedIDs(rest), of(next, slots[2], slots[3], slots[4], end))
	} else if rest[1].Slot != slots[2] {
		t.Errorf("a slot that was changed is listed as %+v, want %+v", rest[1].Slot, slots[2])
	}
	// Read again from the start, the list is what is there now.
	if all := read(nil, 10); !slices.Equal(placedIDs(all), of(slots[0], early, slots[1], next, slots[2], slots[3], slots[4], end)) {
		t.Errorf("from the start the list holds %v", placedIDs(all))
	}
	// Behind the last slot there is nothing, and behind a position before all
	// of them there is everything.
	last := end.ID
	if beyond := read(&ports.SlotPosition{At: at(60), ID: last}, 10); len(beyond) != 0 {
		t.Errorf("behind the last slot the list holds %v, want nothing", placedIDs(beyond))
	}
	if all := read(&ports.SlotPosition{At: at(-60), ID: highest}, 10); len(all) != 8 {
		t.Errorf("behind a position before them all the list holds %d slots, want 8", len(all))
	}
	// Of two slots of one instant the position names one: the identifier
	// decides which side of it the other is on.
	if behind := read(&ports.SlotPosition{At: at(60), ID: lowest}, 10); !slices.Equal(placedIDs(behind), of(end)) {
		t.Errorf("behind a smaller identifier of the same instant the list holds %v, want the last slot", placedIDs(behind))
	}
	if behind := read(&ports.SlotPosition{At: at(60), ID: highest}, 10); len(behind) != 0 {
		t.Errorf("behind a greater identifier of the same instant the list holds %v, want nothing", placedIDs(behind))
	}
}

func placedIDs(placed []ports.PlacedSlot) []string {
	listed := make([]string, 0, len(placed))
	for _, one := range placed {
		listed = append(listed, one.Slot.ID)
	}
	return listed
}

// The slots of an asset are those it has on the targets of its product: not
// those of another asset, and not those another product has under an asset of
// the same identifier. Each comes with its target, and the order is that of
// the first operation of the slot, not that of its target.
func TestTheSlotsOfAnAssetAreThoseOnTheTargetsOfItsProduct(t *testing.T) {
	f := newSlotStore(t)
	other := ids.NewUUID()
	engineering, preview := f.target(f.product, "engineering"), f.target(f.product, "preview")
	f.target(f.product, "unused")
	foreign := f.target(other, "engineering")
	asset, another := ids.NewUUID(), ids.NewUUID()

	onPreview := f.publish(preview, asset, ids.NewUUID(), at(20))
	onEngineering := f.publish(engineering, asset, ids.NewUUID(), at(30))
	f.publish(engineering, another, ids.NewUUID(), at(10))
	underOther := f.publish(foreign, asset, ids.NewUUID(), at(5))
	// What was done to a slot since does not move it.
	onPreview = f.operate(onPreview, domain.Withdraw, at(40))

	read := func(after *ports.SlotPosition, limit int) ([]ports.PlacedSlot, error) {
		return f.store.AssetSlots(t.Context(), f.product, asset, after, limit)
	}
	inOrder(t, "the slots of the asset", of(onPreview, onEngineering), read)

	listed, err := read(nil, 100)
	if err != nil || len(listed) != 2 {
		t.Fatalf("read the list: %d slots, %v", len(listed), err)
	}
	for index, want := range []struct {
		slot   domain.Slot
		target domain.Target
	}{{onPreview, preview}, {onEngineering, engineering}} {
		got := listed[index]
		if got.Slot != want.slot || got.Target.ID != want.target.ID || got.Target.ProductID != f.product ||
			got.Target.PortalKey != want.target.PortalKey || got.Target.AudienceConfigID != want.target.AudienceConfigID ||
			got.Target.Audience != "internal" || got.Target.Environment != "sandbox" || got.Target.BlockGeneration != 0 {
			t.Errorf("slot %d is %+v on %+v, want %+v on %+v", index, got.Slot, got.Target, want.slot, want.target)
		}
	}
	// The same identifier under another product is a slot of that product,
	// and is listed there only. A product that never had the asset lists none.
	if got, err := f.store.AssetSlots(t.Context(), other, asset, nil, 100); err != nil || !slices.Equal(placedIDs(got), of(underOther)) {
		t.Errorf("the other product lists %v (%v) of the asset, want its own slot %s", placedIDs(got), err, underOther.ID)
	}
	if got, err := f.store.AssetSlots(t.Context(), ids.NewUUID(), asset, nil, 100); err != nil || len(got) != 0 {
		t.Errorf("a product that is none lists %d slots of the asset (%v)", len(got), err)
	}
}

// Of the publications of an asset that took their first operation in one
// instant the order is that of the identifiers of their slots, and a page
// that ends between two of them is followed by a page that starts with the
// other: a position is a slot and not an instant. The slots are made in an
// order that is none of the orders they are listed in, each on a target of
// its own, so that neither the order of the rows nor that of the targets is
// any help.
func TestThePublicationsOfAnAssetOfOneInstantAreListedBySlotAndAPageMayEndBetweenThem(t *testing.T) {
	f := newSlotStore(t)
	asset := ids.NewUUID()

	type made struct {
		id     string
		minute int
	}
	order := []made{
		{"80000000-0000-4000-8000-000000000003", 10}, {"ffffffff-ffff-4fff-bfff-fffffffffff1", 10},
		{"c0000000-0000-4000-8000-000000000002", 5}, {"00000000-0000-4000-8000-000000000001", 10},
		{"40000000-0000-4000-8000-000000000002", 20}, {"80000000-0000-4000-8000-000000000001", 10},
	}
	targets := map[string]domain.Target{}
	for index, m := range order {
		targets[m.id] = f.target(f.product, fmt.Sprintf("portal-%d", index))
		f.publish(targets[m.id], asset, m.id, at(m.minute))
	}
	oldestFirst := slices.Clone(order)
	slices.SortFunc(oldestFirst, func(a, b made) int { return cmp.Or(cmp.Compare(a.minute, b.minute), strings.Compare(a.id, b.id)) })
	var want []string
	for _, m := range oldestFirst {
		want = append(want, m.id)
	}

	read := func(after *ports.SlotPosition, limit int) ([]ports.PlacedSlot, error) {
		return f.store.AssetSlots(t.Context(), f.product, asset, after, limit)
	}
	inOrder(t, "the publications of the asset", want, read)

	// Every slot comes with the target it is on.
	listed, err := read(nil, 100)
	if err != nil || len(listed) != len(order) {
		t.Fatalf("read the list: %d slots, %v", len(listed), err)
	}
	for index, placed := range listed {
		if placed.Target.ID != targets[want[index]].ID || placed.Slot.TargetID != placed.Target.ID {
			t.Errorf("slot %d of the list is on the target %s, want %s", index, placed.Target.ID, targets[want[index]].ID)
		}
	}
}

// A slot is made with its first operation. A row of a slot that took none,
// which the tables let a hand write, is no publication: the read of one slot
// answers it, and no list does. What makes a slot a publication is that its
// generation is past zero, and not that an operation row names it: a slot at
// generation zero is listed under no operation, either.
func TestASlotWithoutAnOperationIsNoPublicationAndIsNotListed(t *testing.T) {
	f := newSlotStore(t)
	target := f.target(f.product, "engineering")
	published := f.publish(target, ids.NewUUID(), ids.NewUUID(), at(10))
	bare, bareAsset := ids.NewUUID(), ids.NewUUID()
	torn, tornAsset := ids.NewUUID(), ids.NewUUID()

	owner := pgtest.Connect(t, f.database.Migrator)
	for _, row := range [][2]string{{bare, bareAsset}, {torn, tornAsset}} {
		if _, err := owner.Exec(t.Context(), `INSERT INTO publication.slot (slot_id, product_id, target_id, api_asset_id)
VALUES ($1, $2, $3, $4)`, row[0], f.product, target.ID, row[1]); err != nil {
			t.Fatalf("write a slot by hand: %v", err)
		}
	}
	// An operation row for a slot that is still at generation zero is a
	// slot the tables should not hold. It is not listed for it.
	if _, err := owner.Exec(t.Context(), `INSERT INTO publication.operation
    (operation_id, slot_id, generation, key_digest, action, release_id, reason, actor_issuer, actor_subject, occurred_at)
VALUES (gen_random_uuid(), $1, 1, $2, 'ACTIVATE', gen_random_uuid(), '', 'https://issuer.example', 'publisher', now())`,
		torn, digest(torn, 1)); err != nil {
		t.Fatalf("write an operation by hand: %v", err)
	}

	request := domain.Request{ProductID: f.product, TargetID: target.ID, AssetID: bareAsset}
	if slot, found, err := f.store.Slot(t.Context(), request, false); err != nil || !found || slot.ID != bare || slot.Generation != 0 {
		t.Fatalf("the read of the slot finds %+v (found %t, %v), want the slot at generation zero", slot, found, err)
	}
	if got, err := f.store.Slots(t.Context(), target, []string{published.AssetID, bareAsset, tornAsset}, nil, 100); err != nil ||
		!slices.Equal(placedIDs(got), of(published)) {
		t.Errorf("the slots of the target are %v (%v), want only the one that took an operation", placedIDs(got), err)
	}
	for _, asset := range []string{bareAsset, tornAsset} {
		if got, err := f.store.AssetSlots(t.Context(), f.product, asset, nil, 100); err != nil || len(got) != 0 {
			t.Errorf("the slots of the asset are %v (%v), want none", placedIDs(got), err)
		}
	}
}

// A page takes at least one row: a store asked for less sends nothing to the
// database.
func TestAListOfSlotsTakesAPageOfAtLeastOneRow(t *testing.T) {
	f := newSlotStore(t)
	target := f.target(f.product, "engineering")
	for _, limit := range []int{0, -1} {
		_, errTarget := f.store.Slots(t.Context(), target, []string{ids.NewUUID()}, nil, limit)
		_, errAsset := f.store.AssetSlots(t.Context(), f.product, ids.NewUUID(), nil, limit)
		for list, err := range map[string]error{"the slots of a target": errTarget, "the slots of an asset": errAsset} {
			if err == nil || !strings.Contains(err.Error(), "at least one row") {
				t.Errorf("a page of %d of %s: got %v, want it refused", limit, list, err)
			}
		}
	}
}
