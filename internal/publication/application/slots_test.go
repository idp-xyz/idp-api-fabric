package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

const (
	listedProduct = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
	listedTarget  = "2b3c4d5e-6f70-4a81-9b2c-3d4e5f6a7b8c"
	// The assets of the lists below, the workspaces that own them and the
	// releases that the slots point at.
	firstAsset    = "a0000000-0000-4000-8000-000000000001"
	secondAsset   = "a0000000-0000-4000-8000-000000000002"
	thirdAsset    = "a0000000-0000-4000-8000-000000000003"
	fourthAsset   = "a0000000-0000-4000-8000-000000000004"
	engineering   = "b0000000-0000-4000-8000-000000000001"
	operations    = "b0000000-0000-4000-8000-000000000002"
	firstRelease  = "c0000000-0000-4000-8000-000000000001"
	secondRelease = "c0000000-0000-4000-8000-000000000002"
)

var (
	caller = domain.Principal{Issuer: "https://issuer.example", Subject: "alice"}
	noon   = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
)

// inTransaction marks the context of a transaction with its number.
type inTransaction struct{}

// listing stands in for everything outside the use cases of the lists of
// slots: the store, the authority, the transactions, the audit trail and the
// outbox. It answers what the test set, and keeps what it was asked, in the
// order it was asked and in which transaction, which is what the use cases are
// to be held to. The real store and the real authority are tested where they
// live, and the whole of it in cmd/fabric-api.
type listing struct {
	ports.Store
	ports.Authority

	targets  map[string]domain.Target
	readable []ports.ReadableAsset
	access   ports.AssetAccess
	// refusal is what the authority answers instead of a judgement, and
	// failing what the store answers when it is asked for slots.
	refusal, failing error
	// rows are the slots the store has, in the order of the list. It lists
	// as many of them as a page takes.
	rows []ports.PlacedSlot
	// blocked says of an asset and a release, as "asset/release", that an
	// access block covers them.
	blocked map[string]bool

	opened   int
	asked    []string
	audited  []audit
	enqueued int
}

// audit is an entry of the audit trail, and the transaction it was recorded
// in: none for 0.
type audit struct {
	entry       ports.AuditEntry
	transaction int
}

func (l *listing) did(ctx context.Context, format string, arguments ...any) {
	number, _ := ctx.Value(inTransaction{}).(int)
	l.asked = append(l.asked, fmt.Sprintf("%d %s", number, fmt.Sprintf(format, arguments...)))
}

func (l *listing) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	l.opened++
	return fn(context.WithValue(ctx, inTransaction{}, l.opened))
}

func (l *listing) Now() time.Time { return noon }
func (l *listing) NewID() string  { return "d0000000-0000-4000-8000-000000000001" }

func (l *listing) Target(ctx context.Context, product, target string, lock ports.TargetLock) (domain.Target, bool, error) {
	l.did(ctx, "Target %s, %s", target, map[ports.TargetLock]string{ports.Unlocked: "not locked", ports.HoldTarget: "held", ports.EditBlock: "edited"}[lock])
	found, ok := l.targets[target]
	return found, ok && found.ProductID == product, nil
}

func (l *listing) Slots(ctx context.Context, target domain.Target, assets []string, after *ports.SlotPosition, limit int) ([]ports.PlacedSlot, error) {
	l.did(ctx, "Slots of %s on %d assets, %s, at most %d", target.ID, len(assets), behind(after), limit)
	return l.page(limit), l.failing
}

func (l *listing) AssetSlots(ctx context.Context, product, asset string, after *ports.SlotPosition, limit int) ([]ports.PlacedSlot, error) {
	l.did(ctx, "Slots of the asset %s of %s, %s, at most %d", asset, product, behind(after), limit)
	return l.page(limit), l.failing
}

func (l *listing) page(limit int) []ports.PlacedSlot {
	return slices.Clone(l.rows[:min(limit, len(l.rows))])
}

func behind(after *ports.SlotPosition) string {
	if after == nil {
		return "from the start"
	}
	return "behind " + after.ID
}

func (l *listing) HasBlock(ctx context.Context, target domain.Target, asset, release string) (bool, error) {
	l.did(ctx, "HasBlock %s/%s on %s", asset, release, target.ID)
	return l.blocked[asset+"/"+release], nil
}

func (l *listing) ReadableAssets(ctx context.Context, request domain.Request) ([]ports.ReadableAsset, error) {
	l.did(ctx, "ReadableAssets of %s", request.ProductID)
	return l.readable, l.refusal
}

func (l *listing) Asset(ctx context.Context, request domain.Request) (ports.AssetAccess, error) {
	l.did(ctx, "Asset %s of %s", request.AssetID, request.ProductID)
	return l.access, l.refusal
}

func (l *listing) record(ctx context.Context, entry ports.AuditEntry) error {
	number, _ := ctx.Value(inTransaction{}).(int)
	l.audited = append(l.audited, audit{entry, number})
	return nil
}

func (l *listing) enqueue(context.Context, domain.Request, ports.Event) error {
	l.enqueued++
	return nil
}

func (l *listing) service(t *testing.T) *Publication {
	t.Helper()

	service, err := New(Dependencies{
		Transactions: l, Store: l, Authority: l, Audit: l.record, Enqueue: l.enqueue, Clock: l, IDs: l,
	})
	if err != nil {
		t.Fatalf("wire the use cases: %v", err)
	}
	return service
}

// place is a slot of a list: of the asset, on the target, pointing at the
// release or at none, and the minutes after noon its first operation took.
func place(id, asset string, target domain.Target, release string, minutes int) ports.PlacedSlot {
	return ports.PlacedSlot{
		Slot:   domain.Slot{ID: id, ProductID: listedProduct, TargetID: target.ID, AssetID: asset, ActiveReleaseID: release, Generation: 1},
		Target: target, Since: noon.Add(time.Duration(minutes) * time.Minute),
	}
}

func targetOf(id string) domain.Target {
	return domain.Target{ID: id, ProductID: listedProduct, Audience: "internal", Environment: "sandbox"}
}

// ofTarget is the world of four assets in two workspaces, with a slot each
// on one target: engineering owns the first and the third, operations the
// second and the fourth. The first, the second and the fourth slots point at
// a release, the third at none.
func ofTarget() *listing {
	target := targetOf(listedTarget)
	return &listing{
		targets: map[string]domain.Target{listedTarget: target},
		readable: []ports.ReadableAsset{
			{AssetID: firstAsset, WorkspaceID: engineering}, {AssetID: secondAsset, WorkspaceID: operations},
			{AssetID: thirdAsset, WorkspaceID: engineering}, {AssetID: fourthAsset, WorkspaceID: operations},
		},
		rows: []ports.PlacedSlot{
			place("e0000000-0000-4000-8000-000000000001", firstAsset, target, firstRelease, 1),
			place("e0000000-0000-4000-8000-000000000002", secondAsset, target, secondRelease, 2),
			place("e0000000-0000-4000-8000-000000000003", thirdAsset, target, "", 3),
			place("e0000000-0000-4000-8000-000000000004", fourthAsset, target, firstRelease, 4),
		},
		blocked: map[string]bool{},
	}
}

// ofAsset is the world of the first asset, which engineering owns, with a
// slot on each of four targets: the first, the second and the fourth point at
// a release, the third at none.
func ofAsset() *listing {
	targets := []domain.Target{
		targetOf("f0000000-0000-4000-8000-000000000001"), targetOf("f0000000-0000-4000-8000-000000000002"),
		targetOf("f0000000-0000-4000-8000-000000000003"), targetOf("f0000000-0000-4000-8000-000000000004"),
	}
	return &listing{
		access: ports.AssetAccess{WorkspaceID: engineering, Reader: true},
		rows: []ports.PlacedSlot{
			place("e0000000-0000-4000-8000-000000000001", firstAsset, targets[0], firstRelease, 1),
			place("e0000000-0000-4000-8000-000000000002", firstAsset, targets[1], secondRelease, 2),
			place("e0000000-0000-4000-8000-000000000003", firstAsset, targets[2], "", 3),
			place("e0000000-0000-4000-8000-000000000004", firstAsset, targets[3], firstRelease, 4),
		},
		blocked: map[string]bool{},
	}
}

// listOfSlots is one of the two lists: how it is read, what the request it is
// read with names, and what a read of it asks first, before it reads a slot.
type listOfSlots struct {
	name    string
	world   func() *listing
	request domain.Request
	read    func(*Publication, context.Context, domain.Request, *ports.SlotPosition, int) (SlotPage, error)
	// before is what a read asks of the ports before it reads any slot, for
	// a page of limit slots that starts where after says.
	before func(limit int, after string) []string
	// judgement is the one call of the authority that decides who reads.
	judgement string
}

func listsOfSlots() []listOfSlots {
	return []listOfSlots{
		{
			name: "the slots of a target", world: ofTarget, read: (*Publication).ListSlots,
			request: domain.Request{Caller: caller, ProductID: listedProduct, TargetID: listedTarget, CorrelationID: "correlation-1"},
			before: func(limit int, after string) []string {
				return []string{
					"1 ReadableAssets of " + listedProduct,
					"1 Target " + listedTarget + ", not locked",
					fmt.Sprintf("1 Slots of %s on 4 assets, %s, at most %d", listedTarget, after, limit+1),
				}
			},
			judgement: "1 ReadableAssets of " + listedProduct,
		},
		{
			name: "the publications of an asset", world: ofAsset, read: (*Publication).ListAssetPublications,
			request: domain.Request{Caller: caller, ProductID: listedProduct, AssetID: firstAsset, CorrelationID: "correlation-2"},
			before: func(limit int, after string) []string {
				return []string{
					"1 Asset " + firstAsset + " of " + listedProduct,
					fmt.Sprintf("1 Slots of the asset %s of %s, %s, at most %d", firstAsset, listedProduct, after, limit+1),
				}
			},
			judgement: "1 Asset " + firstAsset + " of " + listedProduct,
		},
	}
}

// reading reads a page, and turns a panic into the failure it is: a page of
// a size nobody should be able to ask for must not take the test down.
func (list listOfSlots) reading(service *Publication, after *ports.SlotPosition, limit int) (page SlotPage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("the read of a page of %d panicked: %v", limit, recovered)
		}
	}()
	return list.read(service, context.Background(), list.request, after, limit)
}

// viewed is what a read asks for each of the slots it reads: whether an
// access block covers it. Nothing is asked of the check of the release it
// points at: a release is put on a slot on its check, and a slot says
// nothing more of it.
func viewed(rows []ports.PlacedSlot) []string {
	var asked []string
	for _, row := range rows {
		asked = append(asked, fmt.Sprintf("1 HasBlock %s/%s on %s", row.Slot.AssetID, row.Slot.ActiveReleaseID, row.Target.ID))
	}
	return asked
}

// beginsWith reports whether what was asked begins with the lines.
func beginsWith(asked, lines []string) bool {
	return len(asked) >= len(lines) && slices.Equal(asked[:len(lines)], lines)
}

// A page holds at least one slot and at most a hundred, as the contract says
// of every list. What is asked for beyond that is refused before anything is
// asked of a port, no transaction is opened and nothing is recorded; the
// bounds themselves are pages.
func TestAPageOfSlotsHoldsAtLeastOneAndAtMostAHundred(t *testing.T) {
	for _, list := range listsOfSlots() {
		t.Run(list.name, func(t *testing.T) {
			for _, limit := range []int{-1, 0, 101, 1000} {
				w := list.world()
				page, err := list.reading(w.service(t), nil, limit)
				if !errors.Is(err, domain.ErrInvalid) || page.Items != nil || page.Next != nil {
					t.Errorf("a page of %d: got %+v and %v, want nothing and %v", limit, page, err, domain.ErrInvalid)
				}
				if len(w.asked) != 0 || w.opened != 0 || len(w.audited) != 0 {
					t.Errorf("a page of %d was refused after %d transactions, the ports were asked %v and %d records were made", limit, w.opened, w.asked, len(w.audited))
				}
			}
			for _, limit := range []int{1, 100} {
				w := list.world()
				if _, err := list.reading(w.service(t), nil, limit); err != nil {
					t.Errorf("a page of %d: %v", limit, err)
				}
				// The store is asked for a slot more than the page holds.
				if before := list.before(limit, "from the start"); !beginsWith(w.asked, before) {
					t.Errorf("a page of %d asked %v, want it to begin with %v", limit, w.asked, before)
				}
			}
		})
	}
}

// A page holds the slots it was asked for. The store is asked for one more,
// which only says that another page follows: that one is not read, and the
// page that follows starts behind the last slot of this one. A page that
// takes every slot there is says nothing of a page that follows. A read
// leaves no record and no event, and everything it asks is asked in the one
// transaction.
func TestAPageHoldsWhatItWasAskedForAndTheSlotBehindItOnlySaysThatAnotherFollows(t *testing.T) {
	for _, list := range listsOfSlots() {
		t.Run(list.name, func(t *testing.T) {
			for _, limit := range []int{1, 2, 3, 4, 5} {
				w := list.world()
				rows := slices.Clone(w.rows)
				page, err := list.reading(w.service(t), nil, limit)
				if err != nil {
					t.Fatalf("a page of %d: %v", limit, err)
				}
				held := min(limit, len(rows))
				if len(page.Items) != held {
					t.Fatalf("a page of %d holds %d slots, want %d", limit, len(page.Items), held)
				}
				for index, item := range page.Items {
					if want := (domain.SlotView{Slot: rows[index].Slot}); !reflect.DeepEqual(item, want) {
						t.Errorf("slot %d of a page of %d is %+v, want %+v", index, limit, item, want)
					}
				}
				if limit < len(rows) {
					if position := rows[limit-1].Position(); page.Next == nil || *page.Next != position {
						t.Errorf("the page after a page of %d starts at %+v, want behind the last slot of the page: %+v", limit, page.Next, position)
					}
				} else if page.Next != nil {
					t.Errorf("a page of %d takes every slot there is and says that %+v follows", limit, page.Next)
				}
				asked := append(list.before(limit, "from the start"), viewed(rows[:held])...)
				if !slices.Equal(w.asked, asked) {
					t.Errorf("a page of %d asked\n %v\nwant\n %v", limit, w.asked, asked)
				}
				if w.opened != 1 || len(w.audited) != 0 || w.enqueued != 0 {
					t.Errorf("a page of %d opened %d transactions, left %d records and %d events, want one transaction and no record", limit, w.opened, len(w.audited), w.enqueued)
				}
			}

			// Where the page starts is handed to the store as it was given.
			w := list.world()
			position := ports.SlotPosition{At: noon, ID: "e0000000-0000-4000-8000-000000000009"}
			if _, err := list.reading(w.service(t), &position, 2); err != nil {
				t.Fatal(err)
			}
			if before := list.before(2, "behind "+position.ID); !beginsWith(w.asked, before) {
				t.Errorf("a page behind a slot asked %v, want it to begin with %v", w.asked, before)
			}

			// A list without slots is a page that holds none, and says that
			// none follows.
			w = list.world()
			w.rows = nil
			page, err := list.reading(w.service(t), nil, 50)
			if err != nil || page.Items == nil || len(page.Items) != 0 || page.Next != nil {
				t.Errorf("a list without slots reads as %+v (%v), want an empty page that is not nil, and no page after it", page, err)
			}
		})
	}
}

// A slot is read as the read of one slot reads it: whether an access block
// covers it, as the store says of the asset and of the release it points at.
func TestASlotOfAListIsReadAsTheReadOfOneSlotIs(t *testing.T) {
	for _, list := range listsOfSlots() {
		t.Run(list.name, func(t *testing.T) {
			w := list.world()
			// A block covers the first release of the first asset, and the third
			// asset and the first asset as a whole.
			w.blocked[firstAsset+"/"+firstRelease] = true
			w.blocked[thirdAsset+"/"] = true
			w.blocked[firstAsset+"/"] = true
			rows := slices.Clone(w.rows)

			page, err := list.reading(w.service(t), nil, 4)
			if err != nil || len(page.Items) != 4 {
				t.Fatalf("read the page: %d slots (%v)", len(page.Items), err)
			}
			var want []domain.SlotView
			for _, row := range rows {
				want = append(want, domain.SlotView{Slot: row.Slot, Blocked: w.blocked[row.Slot.AssetID+"/"+row.Slot.ActiveReleaseID]})
			}
			if !reflect.DeepEqual(page.Items, want) {
				t.Errorf("the slots read as\n %+v\nwant\n %+v", page.Items, want)
			}
			if !page.Items[0].Blocked || page.Items[1].Blocked || !page.Items[2].Blocked {
				t.Errorf("the first slot and the third, which points at none, are blocked and the second is not: %+v", page.Items)
			}
			if asked := append(list.before(4, "from the start"), viewed(rows)...); !slices.Equal(w.asked, asked) {
				t.Errorf("a page asked\n %v\nwant\n %v", w.asked, asked)
			}
		})
	}
}

// What the store lists is the slots of the assets the caller reads. Should it
// list the slot of another asset, the page is not read in part and not read
// in the workspace of nobody: it fails as a whole, and what it fails with is
// no refusal, which is on record and told to the caller as one, but a failure
// of the service.
func TestAPageThatHoldsASlotOfAnAssetTheCallerDoesNotReadFailsAsAWhole(t *testing.T) {
	w := ofTarget()
	w.readable = slices.DeleteFunc(w.readable, func(asset ports.ReadableAsset) bool { return asset.AssetID == secondAsset })
	page, err := listsOfSlots()[0].reading(w.service(t), nil, 4)
	if err == nil || errors.Is(err, ErrForbidden) || errors.Is(err, ErrTargetNotFound) || page.Items != nil || page.Next != nil {
		t.Errorf("got %+v and %v, want nothing and a failure that is no refusal", page, err)
	}
	if len(w.audited) != 0 {
		t.Errorf("a failure of the service left %d records of a refusal", len(w.audited))
	}
	for _, line := range w.asked {
		if strings.HasPrefix(line, fmt.Sprintf("1 HasBlock %s/", secondAsset)) {
			t.Errorf("the slot of the asset the caller does not read was read: %s", line)
		}
	}
}

// A refusal to list slots is on record as a refusal to read a slot: under the
// action of the read, the rule of refusals and the kind of resource a slot is,
// and for no resource of its own. It is recorded outside the transaction that
// refused, and a refusal is no more than what the authority or the lookup of the
// target says: a failure is on no record. The authority is asked first, so that
// a caller that is refused does not learn whether the target is there.
func TestARefusalToListSlotsIsOnRecordAsOneToReadASlot(t *testing.T) {
	entry := func(request domain.Request) ports.AuditEntry {
		return ports.AuditEntry{Request: request, Action: "publication.read", Rule: "DENY-03", Resource: "PublicationSlot"}
	}
	for _, list := range listsOfSlots() {
		t.Run(list.name, func(t *testing.T) {
			w := list.world()
			w.refusal = ErrForbidden
			page, err := list.reading(w.service(t), nil, 10)
			if !errors.Is(err, ErrForbidden) || page.Items != nil || page.Next != nil {
				t.Fatalf("got %+v and %v, want nothing and %v", page, err, ErrForbidden)
			}
			if want := []audit{{entry(list.request), 0}}; !reflect.DeepEqual(w.audited, want) {
				t.Errorf("the refusal is on record as\n %+v\nwant\n %+v", w.audited, want)
			}
			if want := []string{list.judgement}; !slices.Equal(w.asked, want) {
				t.Errorf("a refused caller asked %v, want only the judgement of the caller: %v", w.asked, want)
			}

			// A store that fails is a failure, and on no record.
			w = list.world()
			w.failing = errors.New("the connection is gone")
			if _, err := list.reading(w.service(t), nil, 10); !errors.Is(err, w.failing) || len(w.audited) != 0 {
				t.Errorf("a store that fails: %v, with %d records, want the failure and none", err, len(w.audited))
			}
		})
	}

	// A target that the product does not have is a refusal to whoever reads.
	w := ofTarget()
	list := listsOfSlots()[0]
	request := list.request
	request.TargetID = "2b3c4d5e-6f70-4a81-9b2c-999999999999"
	page, err := w.service(t).ListSlots(t.Context(), request, nil, 10)
	if !errors.Is(err, ErrTargetNotFound) || page.Items != nil {
		t.Fatalf("got %+v and %v, want nothing and %v", page, err, ErrTargetNotFound)
	}
	if want := []audit{{entry(request), 0}}; !reflect.DeepEqual(w.audited, want) {
		t.Errorf("the refusal is on record as\n %+v\nwant\n %+v", w.audited, want)
	}
	if want := []string{list.judgement, "1 Target " + request.TargetID + ", not locked"}; !slices.Equal(w.asked, want) {
		t.Errorf("a target that is not there asked %v, want the judgement of the caller and then the target: %v", w.asked, want)
	}
}

// Whoever reads a slot of an asset lists its publications, whether or not it
// publishes: an author or a reviewer of the workspace does. Whoever does not
// read the slots of the asset is refused, as the authority says.
func TestWhoReadsTheSlotsOfAnAssetListsItsPublicationsWithoutPublishingThem(t *testing.T) {
	list := listsOfSlots()[1]

	w := list.world()
	w.access = ports.AssetAccess{WorkspaceID: engineering, Reader: true, Publisher: false}
	if page, err := list.reading(w.service(t), nil, 10); err != nil || len(page.Items) != 4 {
		t.Errorf("a reader that is no publisher lists %d slots (%v), want the four", len(page.Items), err)
	}

	w = list.world()
	w.access = ports.AssetAccess{WorkspaceID: engineering, Reader: false, Publisher: true}
	if page, err := list.reading(w.service(t), nil, 10); !errors.Is(err, ErrForbidden) || page.Items != nil {
		t.Errorf("got %+v and %v, want nothing and %v for who does not read", page, err, ErrForbidden)
	}
	if len(w.asked) != 1 {
		t.Errorf("a caller that does not read asked %v, want only the judgement", w.asked)
	}
}

// A list is read from what the caller reads and what is there in the one
// transaction: the authority, the target, the slots and what each slot needs
// are all asked inside it, which is how the permissions and the page are of
// one transaction.
func TestAListIsReadInOneTransactionWithWhatItDecidesOn(t *testing.T) {
	for _, list := range listsOfSlots() {
		t.Run(list.name, func(t *testing.T) {
			w := list.world()
			if _, err := list.reading(w.service(t), nil, 4); err != nil {
				t.Fatal(err)
			}
			if w.opened != 1 || len(w.asked) < 5 {
				t.Fatalf("%d transactions, and the ports were asked %v", w.opened, w.asked)
			}
			for _, line := range w.asked {
				if !strings.HasPrefix(line, "1 ") {
					t.Errorf("%q was asked outside the transaction of the read", line)
				}
			}
		})
	}
}
