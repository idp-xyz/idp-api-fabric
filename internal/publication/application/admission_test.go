package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

const (
	// servedSlot is the slot of the first asset on the listed target, and
	// newRelease a release of the asset that the slot never served.
	servedSlot   = "e0000000-0000-4000-8000-000000000001"
	newRelease   = "c0000000-0000-4000-8000-000000000003"
	servedConfig = "70000000-0000-4000-8000-000000000001"
)

// lapsed is why the approval of a release does not hold, as the authority
// says it to whoever asks to put the release on a slot.
var lapsed = errors.New("the approval of the release does not hold")

// publishing stands in for what the use cases of one slot reach besides
// what listing answers: the slot the store has and its history, and what
// the authority says of a release that is put on the slot or served from
// it. It keeps what it was asked with what listing was asked, in order.
type publishing struct {
	*listing

	// held is the slot the store has. A slot without an identifier is one
	// that took no operation yet.
	held domain.Slot
	// history are the releases the slot served, and latest the one it
	// served last.
	history []string
	latest  string
	// refusing is what the authority answers to whoever puts a release on
	// the slot, and unreadable what it answers to a read of an artifact.
	refusing, unreadable error
	applied              []domain.Operation
}

func (p *publishing) Slot(ctx context.Context, request domain.Request, locked bool) (domain.Slot, bool, error) {
	p.did(ctx, "Slot of %s on %s, %s", request.AssetID, request.TargetID, map[bool]string{false: "not locked", true: "locked"}[locked])
	return p.held, p.held.ID != "", nil
}

func (p *publishing) EnsureSlot(ctx context.Context, slot domain.Slot) error {
	p.did(ctx, "EnsureSlot %s", slot.ID)
	p.held = slot
	return nil
}

func (p *publishing) OperationByKey(ctx context.Context, slot, _ string) (domain.Operation, bool, error) {
	p.did(ctx, "OperationByKey on %s", slot)
	return domain.Operation{}, false, nil
}

func (p *publishing) PriorRelease(ctx context.Context, slot, release string) (bool, error) {
	p.did(ctx, "PriorRelease %s on %s", release, slot)
	return slices.Contains(p.history, release), nil
}

func (p *publishing) LatestRelease(ctx context.Context, slot string) (string, error) {
	p.did(ctx, "LatestRelease on %s", slot)
	return p.latest, nil
}

func (p *publishing) Apply(ctx context.Context, _ domain.Slot, op domain.Operation, _ string) error {
	p.did(ctx, "Apply %s of %q at generation %d", op.Action, op.ReleaseID, op.Generation)
	p.applied = append(p.applied, op)
	return nil
}

func (p *publishing) Config(ctx context.Context, _, config string) (string, string, error) {
	p.did(ctx, "Config %s", config)
	return "internal", "sandbox", nil
}

func (p *publishing) Admission(ctx context.Context, request domain.Request, workspace string, target domain.Target,
	release, previous string, returning bool) error {
	p.did(ctx, "Admission of %s of the asset %s in %s on %s after %q, %s", release, request.AssetID, workspace, target.ID, previous,
		map[bool]string{false: "for the first time", true: "returning"}[returning])
	return p.refusing
}

func (p *publishing) Artifact(ctx context.Context, request domain.Request, workspace, release string) (ports.Artifact, error) {
	p.did(ctx, "Artifact of %s of the asset %s in %s", release, request.AssetID, workspace)
	if p.unreadable != nil {
		return ports.Artifact{}, p.unreadable
	}
	return ports.Artifact{Digest: "sha256:" + strings.Repeat("d", 64), Payload: []byte(release)}, nil
}

func (p *publishing) service(t *testing.T) *Publication {
	t.Helper()
	service, err := New(Dependencies{
		Transactions: p, Store: p, Authority: p, Audit: p.record, Enqueue: p.enqueue, Clock: p, IDs: p,
	})
	if err != nil {
		t.Fatalf("wire the use cases: %v", err)
	}
	return service
}

// onSlot is the world of the first asset, which engineering owns, and its
// slot on the listed target, an internal one. The slot served the first
// release and then the second, which it points at, at its second
// generation. The caller reads and publishes in engineering, and the
// product takes work.
func onSlot() *publishing {
	target := targetOf(listedTarget)
	target.AudienceConfigID = servedConfig
	return &publishing{
		listing: &listing{
			targets: map[string]domain.Target{listedTarget: target},
			access:  ports.AssetAccess{WorkspaceID: engineering, Reader: true, Publisher: true, Active: true},
			blocked: map[string]bool{}, unapproved: map[string]error{},
		},
		held: domain.Slot{
			ID: servedSlot, ProductID: listedProduct, TargetID: listedTarget, AssetID: firstAsset,
			ActiveReleaseID: secondRelease, Generation: 2,
		},
		history: []string{firstRelease, secondRelease}, latest: secondRelease,
	}
}

// slotRequest names the slot of onSlot.
var slotRequest = domain.Request{
	Caller: caller, ProductID: listedProduct, TargetID: listedTarget, AssetID: firstAsset, CorrelationID: "correlation-3",
}

// reached is what every change of the slot of onSlot asks before it looks
// at what the change is: who the caller is to the asset, the target, held,
// the slot, locked, and whether the key was used.
func reached() []string {
	return []string{
		"1 Asset " + firstAsset + " of " + listedProduct,
		"1 Target " + listedTarget + ", held",
		"1 Slot of " + firstAsset + " on " + listedTarget + ", locked",
		"1 OperationByKey on " + servedSlot,
	}
}

// A release is put on a slot in one of two ways, and they ask different
// things. An activation puts it there for the first time: the authority is
// asked to admit it on an approval that holds, against the release the slot
// served last, and the history of the slot is not looked at. A restoration
// returns the slot to a release: the release has to be in the history of
// the slot, nothing is compared, and the authority is asked to admit it as
// a release that returns, which is how it knows not to ask whether the
// approval still holds. A withdrawal puts nothing on the slot and asks the
// authority nothing more than who the caller is.
func TestAReleaseIsPutOnASlotForTheFirstTimeOrReturnsToIt(t *testing.T) {
	changes := map[string]struct {
		change domain.Change
		// asks is what the change asks once the slot was reached.
		asks    []string
		applied domain.Operation
		action  string
		rule    string
	}{
		"an activation": {
			change: domain.Change{Action: domain.Activate, ExpectedGeneration: 2, ReleaseID: newRelease, IdempotencyKey: "next"},
			asks: []string{
				"1 Config " + servedConfig,
				"1 HasBlock " + firstAsset + "/" + newRelease + " on " + listedTarget,
				"1 LatestRelease on " + servedSlot,
				fmt.Sprintf("1 Admission of %s of the asset %s in %s on %s after %q, for the first time", newRelease, firstAsset, engineering, listedTarget, secondRelease),
				fmt.Sprintf("1 Apply ACTIVATE of %q at generation 3", newRelease),
			},
			applied: domain.Operation{Action: domain.Activate, PreviousReleaseID: secondRelease, ReleaseID: newRelease},
			action:  "publication.activate", rule: "AC-04",
		},
		"a restoration": {
			change: domain.Change{Action: domain.Restore, ExpectedGeneration: 2, ReleaseID: firstRelease, Reason: "the second breaks clients", IdempotencyKey: "back"},
			asks: []string{
				"1 Config " + servedConfig,
				"1 HasBlock " + firstAsset + "/" + firstRelease + " on " + listedTarget,
				"1 PriorRelease " + firstRelease + " on " + servedSlot,
				fmt.Sprintf("1 Admission of %s of the asset %s in %s on %s after %q, returning", firstRelease, firstAsset, engineering, listedTarget, ""),
				fmt.Sprintf("1 Apply RESTORE of %q at generation 3", firstRelease),
			},
			applied: domain.Operation{Action: domain.Restore, PreviousReleaseID: secondRelease, ReleaseID: firstRelease, Reason: "the second breaks clients"},
			action:  "publication.restore", rule: "AC-04",
		},
		"a withdrawal": {
			change:  domain.Change{Action: domain.Withdraw, ExpectedGeneration: 2, Reason: "wrong audience", IdempotencyKey: "down"},
			asks:    []string{`1 Apply WITHDRAW of "" at generation 3`},
			applied: domain.Operation{Action: domain.Withdraw, PreviousReleaseID: secondRelease, Reason: "wrong audience"},
			action:  "publication.withdraw", rule: "AC-05",
		},
	}
	for name, c := range changes {
		t.Run(name, func(t *testing.T) {
			w := onSlot()
			got, err := w.service(t).ChangeSlot(context.Background(), slotRequest, c.change)
			want := c.applied
			want.ID, want.SlotID, want.Generation, want.By, want.At = w.NewID(), servedSlot, 3, caller, noon
			if err != nil || got != want {
				t.Fatalf("got %+v and %v, want %+v", got, err, want)
			}
			if asked := append(reached(), c.asks...); !slices.Equal(w.asked, asked) {
				t.Errorf("the change asked\n %s\nwant\n %s", strings.Join(w.asked, "\n "), strings.Join(asked, "\n "))
			}
			if !reflect.DeepEqual(w.applied, []domain.Operation{want}) {
				t.Errorf("the store took %+v, want the one operation %+v", w.applied, want)
			}
			record := ports.AuditEntry{
				Request: slotRequest, Action: c.action, Rule: c.rule, Resource: "PublicationSlot", ResourceID: servedSlot, Allowed: true,
			}
			if !reflect.DeepEqual(w.audited, []audit{{record, 1}}) || w.enqueued != 1 || w.opened != 1 {
				t.Errorf("the change left %+v and %d events in %d transactions, want the one record %+v and one event, in the one transaction", w.audited, w.enqueued, w.opened, record)
			}
		})
	}
}

// What the authority says of a release that is put on a slot is what the
// change is answered: nothing is applied, recorded as done or announced. A
// refusal for who the caller is, a publisher who had a part in the revision,
// is on record as one, under the action that was refused; a refusal for the
// approval of the release is a state of the release and no record of the
// caller. The authority is asked the same of a release that returns: it
// decides what it does not ask of one.
func TestWhatTheAuthorityRefusesIsNotPutOnTheSlot(t *testing.T) {
	involved := errors.Join(errors.New("the publisher recorded the revision"), ErrForbidden)
	activate := domain.Change{Action: domain.Activate, ExpectedGeneration: 2, ReleaseID: newRelease, IdempotencyKey: "next"}
	restore := domain.Change{Action: domain.Restore, ExpectedGeneration: 2, ReleaseID: firstRelease, Reason: "the second breaks clients", IdempotencyKey: "back"}
	for name, c := range map[string]struct {
		change  domain.Change
		refusal error
		// record is the action the refusal is on record under, if it is.
		record string
	}{
		"an activation under an approval that does not hold": {activate, lapsed, ""},
		"an activation by who had a part in the revision":    {activate, involved, "publication.activate"},
		"a restoration the authority does not admit":         {restore, lapsed, ""},
		"a restoration by who had a part in the revision":    {restore, involved, "publication.restore"},
	} {
		t.Run(name, func(t *testing.T) {
			w := onSlot()
			w.refusing = c.refusal
			got, err := w.service(t).ChangeSlot(context.Background(), slotRequest, c.change)
			if !errors.Is(err, c.refusal) || got != (domain.Operation{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", got, err, c.refusal)
			}
			if len(w.applied) != 0 || w.enqueued != 0 {
				t.Errorf("the refused change applied %+v and left %d events, want neither", w.applied, w.enqueued)
			}
			var want []audit
			if c.record != "" {
				want = []audit{{ports.AuditEntry{Request: slotRequest, Action: c.record, Rule: "DENY-03", Resource: "PublicationSlot"}, 0}}
			}
			if !reflect.DeepEqual(w.audited, want) {
				t.Errorf("the refusal is on record as %+v, want %+v", w.audited, want)
			}
		})
	}
}

// A slot returns only to a release it served. One it did not serve is
// refused before the authority is asked anything of the release, whatever
// the authority would say, and so is any release for a slot that took no
// operation yet. An activation is not held to the history: what a slot
// served before is activated as any release is, on an approval that holds.
func TestASlotReturnsOnlyToAReleaseItServed(t *testing.T) {
	asked := func(w *publishing, what string) bool {
		return slices.ContainsFunc(w.asked, func(line string) bool { return strings.Contains(line, what) })
	}
	restore := domain.Change{Action: domain.Restore, ExpectedGeneration: 2, ReleaseID: newRelease, Reason: "try the other", IdempotencyKey: "other"}

	w := onSlot()
	got, err := w.service(t).ChangeSlot(context.Background(), slotRequest, restore)
	if !errors.Is(err, domain.ErrNotInHistory) || got != (domain.Operation{}) || len(w.applied) != 0 || len(w.audited) != 0 {
		t.Errorf("a return to a release the slot never served: %+v and %v, applied %+v, recorded %+v; want %v and nothing", got, err, w.applied, w.audited, domain.ErrNotInHistory)
	}
	if !asked(w, "PriorRelease "+newRelease) || asked(w, "Admission") || asked(w, "LatestRelease") {
		t.Errorf("the return asked %v, want the history asked and the authority not asked to admit", w.asked)
	}

	// A slot that took no operation has no history to return to.
	w = onSlot()
	w.held = domain.Slot{}
	first := restore
	first.ExpectedGeneration, first.ReleaseID = 0, firstRelease
	got, err = w.service(t).ChangeSlot(context.Background(), slotRequest, first)
	if !errors.Is(err, domain.ErrNotInHistory) || got != (domain.Operation{}) || len(w.applied) != 0 {
		t.Errorf("a return on a slot that took no operation: %+v and %v, applied %+v; want %v and nothing", got, err, w.applied, domain.ErrNotInHistory)
	}
	if asked(w, "PriorRelease") || asked(w, "Admission") || asked(w, "EnsureSlot") {
		t.Errorf("the return asked %v, want neither the history nor the authority asked, and no slot made", w.asked)
	}

	// The first release, which the slot served, is activated as a release
	// that is new to it: compared with what the slot served last, and
	// admitted for the first time.
	w = onSlot()
	again := domain.Change{Action: domain.Activate, ExpectedGeneration: 2, ReleaseID: firstRelease, IdempotencyKey: "again"}
	if _, err := w.service(t).ChangeSlot(context.Background(), slotRequest, again); err != nil {
		t.Fatal(err)
	}
	admission := fmt.Sprintf("1 Admission of %s of the asset %s in %s on %s after %q, for the first time", firstRelease, firstAsset, engineering, listedTarget, secondRelease)
	if !slices.Contains(w.asked, admission) || asked(w, "PriorRelease") {
		t.Errorf("the activation of a release the slot served asked %v, want %q and the history not asked", w.asked, admission)
	}
}

// What a slot points at is served without a word about its approval. The
// read asks who the caller is to the asset, holds the target, asks whether
// the target serves, reads the slot, asks whether a block covers the
// release, and reads the artifact: the authority is asked neither whether
// the approval of the release holds nor to admit it, so that an approval
// that does not hold changes nothing of what is served. A read that is
// served leaves no record.
func TestWhatASlotPointsAtIsServedWithoutAskingAboutItsApproval(t *testing.T) {
	w := onSlot()
	// The approval of what the slot points at does not hold, and the
	// authority would admit nothing.
	w.unapproved[secondRelease], w.refusing = lapsed, lapsed
	got, err := w.service(t).ActiveArtifact(context.Background(), slotRequest)
	if want := (ports.Artifact{Digest: "sha256:" + strings.Repeat("d", 64), Payload: []byte(secondRelease)}); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v and %v, want the artifact of the release the slot points at: %+v", got, err, want)
	}
	asked := []string{
		"1 Asset " + firstAsset + " of " + listedProduct,
		"1 Target " + listedTarget + ", held",
		"1 Config " + servedConfig,
		"1 Slot of " + firstAsset + " on " + listedTarget + ", not locked",
		"1 HasBlock " + firstAsset + "/" + secondRelease + " on " + listedTarget,
		"1 Artifact of " + secondRelease + " of the asset " + firstAsset + " in " + engineering,
	}
	if !slices.Equal(w.asked, asked) {
		t.Errorf("the read asked\n %s\nwant\n %s", strings.Join(w.asked, "\n "), strings.Join(asked, "\n "))
	}
	if w.opened != 1 || len(w.audited) != 0 || w.enqueued != 0 {
		t.Errorf("the read opened %d transactions and left %d records and %d events, want one transaction and no record", w.opened, len(w.audited), w.enqueued)
	}
}

// What keeps an artifact from being served is said by the slot, the target
// and the product, and not by the approval: a block that covers the
// release, a slot that points at none, a product that takes no work and a
// target that does not serve. Each is answered as nothing to serve, without
// a reason, is on record as a refusal of the read, and the artifact is not
// read. An artifact that cannot be read is a failure, and on no record.
func TestAnArtifactIsNotServedUnderABlockFromAnEmptySlotOrOfAProductThatIsSuspended(t *testing.T) {
	refusal := []audit{{ports.AuditEntry{Request: slotRequest, Action: "publication.download", Rule: "DENY-03", Resource: "PublicationSlot"}, 0}}
	for name, arrange := range map[string]func(*publishing){
		"a block of the release":       func(w *publishing) { w.blocked[firstAsset+"/"+secondRelease] = true },
		"a slot that was withdrawn":    func(w *publishing) { w.held.ActiveReleaseID, w.held.Generation = "", 3 },
		"a slot that took nothing yet": func(w *publishing) { w.held = domain.Slot{} },
		"a product that is suspended":  func(w *publishing) { w.access.Active = false },
		"a target of another audience": func(w *publishing) {
			target := w.targets[listedTarget]
			target.Audience = "partner"
			w.targets[listedTarget] = target
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := onSlot()
			arrange(w)
			got, err := w.service(t).ActiveArtifact(context.Background(), slotRequest)
			if !errors.Is(err, ErrNoVisibleArtifact) || !reflect.DeepEqual(got, ports.Artifact{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", got, err, ErrNoVisibleArtifact)
			}
			if !reflect.DeepEqual(w.audited, refusal) {
				t.Errorf("the refusal is on record as %+v, want %+v", w.audited, refusal)
			}
			for _, line := range w.asked {
				if strings.Contains(line, "Artifact") {
					t.Errorf("the artifact was read all the same: %s", line)
				}
			}
		})
	}

	w := onSlot()
	w.unreadable = errors.New("the bytes are not the ones the release names")
	if got, err := w.service(t).ActiveArtifact(context.Background(), slotRequest); !errors.Is(err, w.unreadable) || !reflect.DeepEqual(got, ports.Artifact{}) || len(w.audited) != 0 {
		t.Errorf("an artifact that cannot be read: %+v and %v, with %d records; want the failure and no record", got, err, len(w.audited))
	}
}
