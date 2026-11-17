package application

import (
	"errors"
	"slices"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// developers is the brand CargoPai gives itself: a name of its own, a logo
// and a colour.
var developers = domain.Brand{
	Name:         "CargoPai Developers",
	LogoURL:      "https://static.cargopai.example/brand/logo.svg",
	PrimaryColor: "#0A7B3C",
}

func (s team) rebrand(caller domain.Principal, product domain.Product, brand domain.Brand, version int64) (domain.Product, error) {
	return s.service.ChangeProductBrand(ctx, ChangeProductBrand{
		Caller: caller, CorrelationID: "correlation-20", ProductID: string(product.ID), Brand: brand, Version: version,
	})
}

// FR-005: an owner gives its product a brand. The change, its audit fact and
// its event are stored together, and whoever may read the product reads the
// brand with it.
func TestChangeProductBrandStoresTheBrandTheAuditFactAndTheEventTogether(t *testing.T) {
	s := newBoard(t)
	s.w.now = started.Add(time.Hour)

	changed, err := s.rebrand(frank, s.cargopai, developers, 1)
	if err != nil {
		t.Fatalf("change the brand: %v", err)
	}

	want := s.cargopai
	want.Brand, want.Version, want.UpdatedAt, want.UpdatedBy = developers, 2, s.w.now, frank
	if changed != want || s.w.products[s.cargopai.ID] != want {
		t.Errorf("changed\n %+v\nstored\n %+v\nwant\n %+v", changed, s.w.products[s.cargopai.ID], want)
	}
	// AC-09: the portal of a product is its owner's to make up.
	wantAudit := []audited{{ports.AuditEntry{
		Actor: frank, Action: "product.change_brand", Allowed: true, Rule: "AC-09",
		ProductID: s.cargopai.ID, ResourceType: "Product", ResourceID: string(s.cargopai.ID), CorrelationID: "correlation-20",
	}, true}}
	if !slices.Equal(s.w.audited, wantAudit) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", s.w.audited, wantAudit)
	}
	if want := []string{"ProductBrandChanged " + string(s.cargopai.ID) + " at 2 correlation-20"}; !slices.Equal(s.w.events, want) {
		t.Errorf("events %v, want %v", s.w.events, want)
	}
	if s.w.transactions != 1 {
		t.Errorf("%d transactions, want 1", s.w.transactions)
	}
	if s.w.products[s.parcel.ID] != s.parcel {
		t.Errorf("the other product is now %+v, want it untouched", s.w.products[s.parcel.ID])
	}

	// Whoever may read the product reads its brand with it: another owner,
	// a member of a workspace, and a platform administrator, in a list too.
	s.w.quiet()
	for name, caller := range map[string]domain.Principal{"another owner": alice, "a member of a workspace": carol, "the platform administrator": admin} {
		if got, err := s.product(caller, s.cargopai); err != nil || got != want {
			t.Errorf("%s reads the product: got %+v and %v, want\n %+v", name, got, err, want)
		}
	}
	if page, err := s.listProducts(admin, 0, nil); err != nil || !slices.Equal(page.Items, []domain.Product{want, s.parcel}) {
		t.Errorf("the administrator lists the products: got %+v and %v, want the brand with its product", page, err)
	}
	s.untouched(t)

	// A brand is replaced as a whole: what the new one leaves out, the
	// product has none of from then on.
	plain, err := s.rebrand(alice, s.cargopai, domain.Brand{Name: "CargoPai 开放平台"}, 2)
	if err != nil || plain.Brand != (domain.Brand{Name: "CargoPai 开放平台"}) || plain.Version != 3 || plain.UpdatedBy != alice ||
		s.w.products[s.cargopai.ID] != plain {
		t.Errorf("the name alone: got %+v and %v, want it at version 3 without a logo or a colour, by alice", plain, err)
	}
}

// A change is made from the version of the product the caller read. One made
// from another is refused, and nothing is recorded: the caller reads the
// product again and decides anew.
func TestChangeProductBrandIsMadeFromTheVersionTheCallerRead(t *testing.T) {
	s := newBoard(t)
	changed, err := s.rebrand(frank, s.cargopai, developers, 1)
	if err != nil {
		t.Fatalf("change the brand: %v", err)
	}

	for _, version := range []int64{1, 3, 0, -1} {
		s.w.quiet()
		if got, err := s.rebrand(alice, s.cargopai, domain.Brand{Name: "CargoPai"}, version); !errors.Is(err, ErrVersionMismatch) || got != (domain.Product{}) {
			t.Errorf("a change from version %d: got %+v and %v, want nothing and %v", version, got, err, ErrVersionMismatch)
		}
		if s.w.products[s.cargopai.ID] != changed || len(s.w.audited)+len(s.w.events) != 0 {
			t.Errorf("a change from version %d left the product %+v, audit %+v, events %v", version, s.w.products[s.cargopai.ID], s.w.audited, s.w.events)
		}
	}
	// The version is asked before the brand: the brand the product has, from
	// a version it is not at, is refused like any other.
	if got, err := s.rebrand(alice, s.cargopai, developers, 1); !errors.Is(err, ErrVersionMismatch) || got != (domain.Product{}) {
		t.Errorf("the same brand from the version before: got %+v and %v, want nothing and %v", got, err, ErrVersionMismatch)
	}
}

// A change to the brand the product has changes nothing: it is answered with
// the product as it is, and nothing is stored, recorded or announced.
func TestTheSameBrandAgainChangesNoProduct(t *testing.T) {
	s := newBoard(t)
	s.w.now = started.Add(time.Hour)

	got, err := s.rebrand(alice, s.cargopai, s.cargopai.Brand, 1)
	if err != nil || got != s.cargopai || s.w.products[s.cargopai.ID] != s.cargopai {
		t.Errorf("the brand it was registered with: got %+v and %v, stored %+v, want the product as it was", got, err, s.w.products[s.cargopai.ID])
	}
	if len(s.w.audited)+len(s.w.events) != 0 {
		t.Errorf("a change that changed nothing left audit %+v and events %v", s.w.audited, s.w.events)
	}

	changed, err := s.rebrand(alice, s.cargopai, developers, 1)
	if err != nil {
		t.Fatalf("change the brand: %v", err)
	}
	s.w.quiet()
	s.w.now = started.Add(2 * time.Hour)
	if again, err := s.rebrand(frank, s.cargopai, developers, 2); err != nil || again != changed || s.w.products[s.cargopai.ID] != changed {
		t.Errorf("the same brand again: got %+v and %v, want the product as alice left it", again, err)
	}
	if len(s.w.audited)+len(s.w.events) != 0 {
		t.Errorf("a change that changed nothing left audit %+v and events %v", s.w.audited, s.w.events)
	}
}

// An owner of the product changes its brand, and nobody else. The answer to
// anyone else depends on the product alone: it is the same whatever the
// request says.
func TestChangeProductBrandRefusesWhoeverDoesNotOwnTheProduct(t *testing.T) {
	for name, c := range withoutPortal() {
		t.Run(name, func(t *testing.T) {
			s := newBoard(t)

			requests := map[string]struct {
				brand   domain.Brand
				version int64
			}{
				"a brand, from the version of the product": {developers, 1},
				"a brand, from another version":            {developers, 7},
				"the brand the product has":                {s.cargopai.Brand, 1},
				"a brand that is none":                     {domain.Brand{Name: " CargoPai ", LogoURL: "http://static.cargopai.example/logo.svg"}, 1},
			}
			for what, r := range requests {
				s.w.quiet()
				changed, err := s.rebrand(c.caller, s.cargopai, r.brand, r.version)
				if err != c.want || changed != (domain.Product{}) {
					t.Fatalf("%s: got %+v and %v, want nothing and exactly %v", what, changed, err, c.want)
				}
				// The decision is made inside the transaction and recorded
				// once that transaction is over.
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "product.change_brand", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "Product", ResourceID: string(s.cargopai.ID), CorrelationID: "correlation-20",
				}, false}}
				if !slices.Equal(s.w.audited, want) {
					t.Errorf("%s: audited\n %+v\nwant, outside the transaction,\n %+v", what, s.w.audited, want)
				}
				if s.w.products[s.cargopai.ID] != s.cargopai || len(s.w.events) != 0 {
					t.Errorf("%s: a refused change left the product %+v and the events %v", what, s.w.products[s.cargopai.ID], s.w.events)
				}
			}
		})
	}
}

// What cannot be a brand is refused by the model, for an owner too, and a
// product that is not there has no brand to change. Neither is a refusal of
// the caller, and neither is recorded.
func TestChangeProductBrandRefusesWhatCannotBeABrand(t *testing.T) {
	s := newBoard(t)

	for what, brand := range map[string]domain.Brand{
		"no name":               {LogoURL: developers.LogoURL},
		"a name in white space": {Name: " CargoPai Developers "},
		"a logo over http":      {Name: "CargoPai Developers", LogoURL: "http://static.cargopai.example/logo.svg"},
		"a colour by its name":  {Name: "CargoPai Developers", PrimaryColor: "green"},
	} {
		if changed, err := s.rebrand(alice, s.cargopai, brand, 1); !errors.Is(err, domain.ErrInvalid) || changed != (domain.Product{}) {
			t.Errorf("%s: got %+v and %v, want nothing and %v", what, changed, err, domain.ErrInvalid)
		}
	}

	gone := domain.Product{ID: missingID}
	for name, caller := range map[string]domain.Principal{"an owner of another product": alice, "the platform administrator": admin, "a stranger": mallory} {
		if changed, err := s.rebrand(caller, gone, developers, 1); !errors.Is(err, ErrProductNotFound) || changed != (domain.Product{}) {
			t.Errorf("%s changes the brand of a product that does not exist: got %+v and %v, want nothing and %v", name, changed, err, ErrProductNotFound)
		}
	}
	if changed, err := s.rebrand(alice, domain.Product{ID: "cargopai"}, developers, 1); err != ErrProductNotFound || changed != (domain.Product{}) {
		t.Errorf("an identifier that is none: got %+v and %v, want nothing and exactly %v", changed, err, ErrProductNotFound)
	}

	if s.w.products[s.cargopai.ID] != s.cargopai || len(s.w.audited)+len(s.w.events) != 0 {
		t.Errorf("what was refused left the product %+v, audit %+v, events %v", s.w.products[s.cargopai.ID], s.w.audited, s.w.events)
	}
}

// INV-01: a suspended product takes no change of its brand, not even to the
// brand it has. Whoever may read it still reads it.
func TestASuspendedProductTakesNoChangeOfItsBrand(t *testing.T) {
	s := newBoard(t)
	suspended := s.cargopai
	suspended.Status = domain.ProductSuspended
	s.w.products[s.cargopai.ID] = suspended

	for what, brand := range map[string]domain.Brand{"another brand": developers, "the brand it has": suspended.Brand} {
		if changed, err := s.rebrand(alice, s.cargopai, brand, 1); !errors.Is(err, domain.ErrProductNotActive) || changed != (domain.Product{}) {
			t.Errorf("%s: got %+v and %v, want nothing and %v", what, changed, err, domain.ErrProductNotActive)
		}
	}
	if s.w.products[s.cargopai.ID] != suspended || len(s.w.audited)+len(s.w.events) != 0 {
		t.Errorf("a suspended product is now %+v, with audit %+v and events %v", s.w.products[s.cargopai.ID], s.w.audited, s.w.events)
	}
	if got, err := s.product(carol, s.cargopai); err != nil || got != suspended {
		t.Errorf("a member reads the suspended product: got %+v and %v", got, err)
	}
}

// What a change decides from is read once the product is locked, and one
// that finds it locked waits. So it decides from what the one it waited for
// left behind, not from what was there when it arrived.
func TestAChangeOfTheBrandThatWaitedDecidesFromWhatItFindsThen(t *testing.T) {
	s := newBoard(t)
	// While alice waits to change the brand, frank changes it.
	s.w.meanwhile = func() {
		changed, err := s.cargopai.ChangeBrand(developers, frank, s.w.now)
		if err != nil {
			t.Fatalf("change the brand: %v", err)
		}
		s.w.products[s.cargopai.ID] = changed
	}

	if got, err := s.rebrand(alice, s.cargopai, domain.Brand{Name: "CargoPai 开放平台"}, 1); !errors.Is(err, ErrVersionMismatch) || got != (domain.Product{}) {
		t.Fatalf("got %+v and %v, want nothing and %v", got, err, ErrVersionMismatch)
	}
	// frank's change stands, and alice's left nothing.
	if got := s.w.products[s.cargopai.ID]; got.Brand != developers || got.Version != 2 || got.UpdatedBy != frank {
		t.Errorf("the product is %+v, want it as frank changed it", got)
	}
	if len(s.w.audited)+len(s.w.events) != 0 {
		t.Errorf("the change that waited left audit %+v and events %v", s.w.audited, s.w.events)
	}

	// From the version frank left, alice changes it.
	s.w.meanwhile = nil
	if got, err := s.rebrand(alice, s.cargopai, domain.Brand{Name: "CargoPai 开放平台"}, 2); err != nil || got.Version != 3 || got.UpdatedBy != alice {
		t.Errorf("from the version frank left: got %+v and %v, want the product at version 3, by alice", got, err)
	}
}

// INV-13: a change of the brand, its audit fact and its event are one
// transaction. Whatever fails takes all of it down, and the product is as it
// was.
func TestChangeProductBrandLeavesTheProductAsItWasWhenAnyPartOfItFails(t *testing.T) {
	down := errors.New("down")
	for _, step := range []string{"audit", "events", "commit", "read", "held", "roles", "lock", "rebrand"} {
		t.Run(step+" fails", func(t *testing.T) {
			s := newBoard(t)
			s.w.failing[step] = down

			if changed, err := s.rebrand(alice, s.cargopai, developers, 1); !errors.Is(err, down) || changed != (domain.Product{}) {
				t.Fatalf("got %+v and %v, want nothing and the failure", changed, err)
			}
			if s.w.products[s.cargopai.ID] != s.cargopai || len(s.w.audited)+len(s.w.events) != 0 {
				t.Errorf("it left the product %+v, audit %+v, events %v", s.w.products[s.cargopai.ID], s.w.audited, s.w.events)
			}
		})
	}

	// A store that finds the product otherwise than it was locked is
	// reported as somebody else's change, and leaves nothing of the store in
	// the answer.
	s := newBoard(t)
	s.w.failing["rebrand"] = ports.ErrProductChanged
	if _, err := s.rebrand(alice, s.cargopai, developers, 1); !errors.Is(err, ErrVersionMismatch) || errors.Is(err, ports.ErrProductChanged) {
		t.Errorf("a change that lost the race: got %v, want %v and nothing of the store", err, ErrVersionMismatch)
	}
	if s.w.products[s.cargopai.ID] != s.cargopai || len(s.w.audited)+len(s.w.events) != 0 {
		t.Errorf("a change that lost the race left the product %+v, audit %+v, events %v", s.w.products[s.cargopai.ID], s.w.audited, s.w.events)
	}

	s = newBoard(t)
	s.w.failing["commit"] = ports.ErrOutcomeUncertain
	if _, err := s.rebrand(alice, s.cargopai, developers, 1); !errors.Is(err, ports.ErrOutcomeUncertain) {
		t.Errorf("a change whose commit has an unknown outcome: got %v, want %v", err, ports.ErrOutcomeUncertain)
	}
}

// A refusal stands whether or not it could be recorded, and a request by
// nobody is refused.
func TestTheBrandRefusesAndFailsAsEverythingElseDoes(t *testing.T) {
	s := newBoard(t)
	s.w.failing["audit"] = errors.New("the audit trail is down")

	for what, c := range map[string]answered{
		"a change by a member":          {carol, ErrForbidden},
		"a change by the administrator": {admin, ErrForbidden},
		"a change by a stranger":        {mallory, ErrProductNotFound},
	} {
		if _, err := s.rebrand(c.caller, s.cargopai, developers, 1); !errors.Is(err, c.want) || !errors.Is(err, ErrRefusalNotAudited) {
			t.Errorf("%s: got %v, want %v and %v", what, err, c.want, ErrRefusalNotAudited)
		}
	}
	delete(s.w.failing, "audit")

	s.w.quiet()
	if _, err := s.rebrand(domain.Principal{}, s.cargopai, developers, 1); !errors.Is(err, ErrForbidden) {
		t.Errorf("a change by nobody: got %v, want %v", err, ErrForbidden)
	}
	if s.w.products[s.cargopai.ID] != s.cargopai {
		t.Errorf("what was refused left the product %+v", s.w.products[s.cargopai.ID])
	}
	s.untouched(t)
}
