package application

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

var ctx = context.Background()

// registration is the registration of CargoPai by the platform administrator,
// with alice as its owner. A test changes what it is about.
func registration() RegisterProduct {
	return RegisterProduct{
		Caller: admin, CorrelationID: "correlation-1",
		Key: "cargopai", DisplayName: "CargoPai", Owner: cargo, Owners: []domain.Principal{alice},
	}
}

// registered registers a product with the key and the owner.
func registered(t *testing.T, service *ProductGovernance, key string, owner domain.Principal) domain.Product {
	t.Helper()

	command := registration()
	command.Key, command.Owners = key, []domain.Principal{owner}
	product, err := service.RegisterProduct(ctx, command)
	if err != nil {
		t.Fatalf("register %s: %v", key, err)
	}
	return product
}

// created creates a workspace of the product as its owner.
func created(t *testing.T, service *ProductGovernance, product domain.Product, owner domain.Principal, key string) domain.Workspace {
	t.Helper()

	workspace, err := service.CreateWorkspace(ctx, CreateWorkspace{
		Caller: owner, CorrelationID: "correlation-w", ProductID: string(product.ID), Key: key, DisplayName: "API engineering",
	})
	if err != nil {
		t.Fatalf("create the workspace %s: %v", key, err)
	}
	return workspace
}

// quiet forgets what the set-up of a test recorded, so that the test reads
// only what the call under test left.
func (w *world) quiet() { w.audited, w.events, w.transactions = nil, nil, 0 }

func TestRegisterProductStoresTheProductItsOwnersTheAuditFactAndTheEventTogether(t *testing.T) {
	w, service := newWorld(t)
	command := registration()
	command.Owners = []domain.Principal{alice, bob}

	product, err := service.RegisterProduct(ctx, command)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	want := domain.Product{
		ID: "00000000-0000-4000-8000-000000000001", Key: "cargopai", DisplayName: "CargoPai",
		Status: domain.ProductActive, Owner: cargo, Version: 1, CreatedAt: started, CreatedBy: admin, UpdatedAt: started,
	}
	if product != want {
		t.Errorf("product\n %+v\nwant\n %+v", product, want)
	}
	if stored := w.products[product.ID]; stored != want {
		t.Errorf("stored\n %+v\nwant\n %+v", stored, want)
	}

	// Each first owner holds its role under an identifier of its own, in
	// force from the registration, granted by the registrant.
	const first, second = "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003"
	wantRoles := map[domain.RoleAssignmentID]domain.RoleAssignment{
		first: {ID: first, ProductID: product.ID, Principal: alice, Role: domain.RoleProductOwner,
			Version: 1, GrantedAt: started, GrantedBy: admin},
		second: {ID: second, ProductID: product.ID, Principal: bob, Role: domain.RoleProductOwner,
			Version: 1, GrantedAt: started, GrantedBy: admin},
	}
	if !maps.Equal(w.roles, wantRoles) {
		t.Errorf("roles\n %+v\nwant\n %+v", w.roles, wantRoles)
	}

	wantAudit := []audited{{ports.AuditEntry{
		Actor: admin, Action: "product.register", Allowed: true, Rule: "AC-08",
		ProductID: product.ID, ResourceType: "Product", ResourceID: string(product.ID), CorrelationID: "correlation-1",
	}, true}}
	if !slices.Equal(w.audited, wantAudit) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", w.audited, wantAudit)
	}
	if want := []string{"ProductRegistered " + string(product.ID) + " correlation-1"}; !slices.Equal(w.events, want) {
		t.Errorf("events %v, want %v", w.events, want)
	}
	if w.transactions != 1 {
		t.Errorf("%d transactions, want 1", w.transactions)
	}
}

// AC-08: only a platform administrator registers a product. Owning a product
// is not administering the platform (F05 §5.1).
func TestRegisterProductRefusesWhoeverIsNotAPlatformAdministrator(t *testing.T) {
	for name, caller := range map[string]domain.Principal{
		"a stranger":             mallory,
		"the owner of a product": alice,
		"the administrator's name under another issuer": {Issuer: "https://other.example", Subject: admin.Subject},
	} {
		t.Run(name, func(t *testing.T) {
			w, service := newWorld(t)
			registered(t, service, "parcel", alice)
			w.quiet()

			command := registration()
			command.Caller = caller
			product, err := service.RegisterProduct(ctx, command)
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("got %+v and %v, want %v", product, err, ErrForbidden)
			}
			if len(w.products) != 1 || len(w.events) != 0 {
				t.Errorf("a refused registration left %d products and the events %v", len(w.products), w.events)
			}

			// The refusal is recorded on its own, in the platform scope: it
			// concerns no product and left no resource behind.
			want := []audited{{ports.AuditEntry{
				Actor: caller, Action: "product.register", Rule: "DENY-03", ResourceType: "Product", CorrelationID: "correlation-1",
			}, false}}
			if !slices.Equal(w.audited, want) {
				t.Errorf("audited\n %+v\nwant, outside any transaction,\n %+v", w.audited, want)
			}
		})
	}
}

func TestRegisterProductRefusesARequestWithoutAPrincipal(t *testing.T) {
	w, service := newWorld(t)

	command := registration()
	command.Caller = domain.Principal{}
	if _, err := service.RegisterProduct(ctx, command); !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v, want %v", err, ErrForbidden)
	}
	if len(w.products) != 0 || len(w.audited) != 0 {
		t.Errorf("a request without a principal left %d products and %d audit entries", len(w.products), len(w.audited))
	}
}

func TestRegisterProductRefusesWhatCannotBeAProduct(t *testing.T) {
	cases := map[string]struct {
		change func(command *RegisterProduct)
		want   error
	}{
		"a key that is not a key":       {func(c *RegisterProduct) { c.Key = "Cargo Pai" }, domain.ErrInvalid},
		"no display name":               {func(c *RegisterProduct) { c.DisplayName = "" }, domain.ErrInvalid},
		"no owning organization":        {func(c *RegisterProduct) { c.Owner = domain.OrganizationRef{} }, domain.ErrInvalid},
		"no owner":                      {func(c *RegisterProduct) { c.Owners = nil }, domain.ErrInvalid},
		"the same owner twice":          {func(c *RegisterProduct) { c.Owners = []domain.Principal{alice, alice} }, domain.ErrInvalid},
		"an owner that is no principal": {func(c *RegisterProduct) { c.Owners = []domain.Principal{{Issuer: trustedIssuer}} }, domain.ErrInvalid},
		// A role granted to a principal that can never authenticate would
		// leave the product without anybody to manage it.
		"an owner under an issuer nobody trusts": {func(c *RegisterProduct) {
			c.Owners = []domain.Principal{alice, {Issuer: "https://other.example", Subject: "bob"}}
		}, ErrOwnerIssuerNotTrusted},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w, service := newWorld(t)

			command := registration()
			c.change(&command)
			if product, err := service.RegisterProduct(ctx, command); !errors.Is(err, c.want) {
				t.Fatalf("got %+v and %v, want %v", product, err, c.want)
			}
			if len(w.products)+len(w.roles)+len(w.audited)+len(w.events) != 0 {
				t.Errorf("a refused registration left products %v, roles %v, audit %v, events %v", w.products, w.roles, w.audited, w.events)
			}
		})
	}
}

// FR-001: a key that is taken is refused, and the product that holds it is
// not touched.
func TestRegisterProductRefusesAKeyThatIsTaken(t *testing.T) {
	w, service := newWorld(t)
	first := registered(t, service, "cargopai", alice)
	w.quiet()

	command := registration()
	command.DisplayName, command.Owners = "Another CargoPai", []domain.Principal{bob}
	if product, err := service.RegisterProduct(ctx, command); !errors.Is(err, ports.ErrProductKeyTaken) {
		t.Fatalf("got %+v and %v, want %v", product, err, ports.ErrProductKeyTaken)
	}
	if len(w.products) != 1 || w.products[first.ID] != first {
		t.Errorf("the products are %+v, want only the first as it was", w.products)
	}
	if len(w.roles) != 1 || len(w.audited) != 0 || len(w.events) != 0 {
		t.Errorf("a refused registration left roles %v, audit %v, events %v", w.roles, w.audited, w.events)
	}
}

// INV-13: the product, its audit fact and its event are one transaction.
// Whatever fails takes all of it down.
func TestRegisterProductLeavesNothingWhenAnyPartOfItFails(t *testing.T) {
	down := errors.New("down")
	for _, step := range []string{"audit", "events", "commit"} {
		t.Run(step+" fails", func(t *testing.T) {
			w, service := newWorld(t)
			w.failing[step] = down

			if product, err := service.RegisterProduct(ctx, registration()); !errors.Is(err, down) {
				t.Fatalf("got %+v and %v, want the failure", product, err)
			}
			if len(w.products)+len(w.roles)+len(w.audited)+len(w.events) != 0 {
				t.Errorf("a failed registration left products %v, roles %v, audit %v, events %v", w.products, w.roles, w.audited, w.events)
			}
		})
	}

	t.Run("a commit of unknown outcome is reported as such", func(t *testing.T) {
		w, service := newWorld(t)
		w.failing["commit"] = ports.ErrOutcomeUncertain

		if _, err := service.RegisterProduct(ctx, registration()); !errors.Is(err, ports.ErrOutcomeUncertain) {
			t.Fatalf("got %v, want %v", err, ports.ErrOutcomeUncertain)
		}
	})
}

func TestGetProductAnswersTheAdministratorAndTheOwners(t *testing.T) {
	w, service := newWorld(t)
	product := registered(t, service, "cargopai", alice)
	w.quiet()

	for name, caller := range map[string]domain.Principal{"the platform administrator": admin, "an owner": alice} {
		t.Run(name, func(t *testing.T) {
			got, err := service.GetProduct(ctx, GetProduct{Caller: caller, CorrelationID: "correlation-2", ProductID: string(product.ID)})
			if err != nil || got != product {
				t.Fatalf("got %+v and %v, want the product", got, err)
			}
		})
	}
	if len(w.audited) != 0 {
		t.Errorf("reads that were allowed left the audit entries %+v", w.audited)
	}
}

// To whoever holds no role in a product, the product does not exist: the
// answer is the one a missing product gets (F05 §5.2). Only the audit trail
// tells them apart.
func TestGetProductHidesAProductFromAStrangerAsIfItWereMissing(t *testing.T) {
	w, service := newWorld(t)
	cargopai := registered(t, service, "cargopai", alice)
	registered(t, service, "parcel", bob)
	w.quiet()

	missing := "00000000-0000-4000-8000-999999999999"
	_, errMissing := service.GetProduct(ctx, GetProduct{Caller: mallory, CorrelationID: "correlation-2", ProductID: missing})
	if errMissing != ErrProductNotFound {
		t.Fatalf("a missing product: got %v, want exactly %v", errMissing, ErrProductNotFound)
	}
	if len(w.audited) != 0 {
		t.Fatalf("a missing product left the audit entries %+v", w.audited)
	}

	for name, caller := range map[string]domain.Principal{"a stranger": mallory, "the owner of another product": bob} {
		t.Run(name, func(t *testing.T) {
			w.quiet()

			got, err := service.GetProduct(ctx, GetProduct{Caller: caller, CorrelationID: "correlation-2", ProductID: string(cargopai.ID)})
			if err != errMissing || got != (domain.Product{}) {
				t.Fatalf("got %+v and %v, want nothing and the error of a missing product", got, err)
			}
			want := []audited{{ports.AuditEntry{
				Actor: caller, Action: "product.read", Rule: "DENY-03",
				ProductID: cargopai.ID, ResourceType: "Product", ResourceID: string(cargopai.ID), CorrelationID: "correlation-2",
			}, false}}
			if !slices.Equal(w.audited, want) {
				t.Errorf("audited\n %+v\nwant\n %+v", w.audited, want)
			}
		})
	}

	t.Run("an identifier that is not one", func(t *testing.T) {
		w.quiet()
		if _, err := service.GetProduct(ctx, GetProduct{Caller: admin, ProductID: "cargopai"}); err != ErrProductNotFound {
			t.Fatalf("got %v, want exactly %v", err, ErrProductNotFound)
		}
	})
}

func TestCreateWorkspaceStoresTheWorkspaceTheAuditFactAndTheEventTogether(t *testing.T) {
	w, service := newWorld(t)
	product := registered(t, service, "cargopai", alice)
	w.quiet()
	w.now = started.Add(3600e9)

	workspace, err := service.CreateWorkspace(ctx, CreateWorkspace{
		Caller: alice, CorrelationID: "correlation-3", ProductID: string(product.ID), Key: "api-engineering", DisplayName: "API engineering",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// The product and the role of its owner took the first two identifiers.
	want := domain.Workspace{
		ID: "00000000-0000-4000-8000-000000000003", ProductID: product.ID, Key: "api-engineering", DisplayName: "API engineering",
		Status: domain.WorkspaceActive, Version: 1, CreatedAt: w.now, CreatedBy: alice, UpdatedAt: w.now,
	}
	if workspace != want || w.workspaces[workspace.ID] != want {
		t.Errorf("workspace\n %+v\nstored\n %+v\nwant\n %+v", workspace, w.workspaces[workspace.ID], want)
	}
	wantAudit := []audited{{ports.AuditEntry{
		Actor: alice, Action: "workspace.manage", Allowed: true, Rule: "AC-09",
		ProductID: product.ID, ResourceType: "Workspace", ResourceID: string(workspace.ID), CorrelationID: "correlation-3",
	}, true}}
	if !slices.Equal(w.audited, wantAudit) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", w.audited, wantAudit)
	}
	if want := []string{"WorkspaceCreated " + string(workspace.ID) + " correlation-3"}; !slices.Equal(w.events, want) {
		t.Errorf("events %v, want %v", w.events, want)
	}
	if w.transactions != 1 {
		t.Errorf("%d transactions, want 1", w.transactions)
	}
}

// AC-09: only an owner of the product manages its workspaces. Whoever may
// know the product exists is told it may not; to anyone else there is no such
// product. Either way nothing is written and the refusal is recorded.
func TestCreateWorkspaceRefusesWhoeverDoesNotOwnTheProduct(t *testing.T) {
	cases := map[string]struct {
		caller domain.Principal
		want   error
	}{
		// Registering a product gives no power inside it.
		"the platform administrator":   {admin, ErrForbidden},
		"a stranger":                   {mallory, ErrProductNotFound},
		"the owner of another product": {bob, ErrProductNotFound},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w, service := newWorld(t)
			cargopai := registered(t, service, "cargopai", alice)
			registered(t, service, "parcel", bob)
			w.quiet()

			workspace, err := service.CreateWorkspace(ctx, CreateWorkspace{
				Caller: c.caller, CorrelationID: "correlation-3", ProductID: string(cargopai.ID), Key: "intruder", DisplayName: "Intruder",
			})
			if err != c.want {
				t.Fatalf("got %+v and %v, want exactly %v", workspace, err, c.want)
			}
			if len(w.workspaces) != 0 || len(w.events) != 0 {
				t.Errorf("a refused creation left the workspaces %v and the events %v", w.workspaces, w.events)
			}
			// The decision is made inside the transaction and recorded once
			// that transaction is over: the record is not rolled back with it.
			want := []audited{{ports.AuditEntry{
				Actor: c.caller, Action: "workspace.manage", Rule: "DENY-03",
				ProductID: cargopai.ID, ResourceType: "Workspace", CorrelationID: "correlation-3",
			}, false}}
			if !slices.Equal(w.audited, want) {
				t.Errorf("audited\n %+v\nwant, outside the transaction,\n %+v", w.audited, want)
			}
		})
	}
}

func TestCreateWorkspaceRefusesWhatCannotBeAWorkspace(t *testing.T) {
	w, service := newWorld(t)
	cargopai := registered(t, service, "cargopai", alice)
	parcel := registered(t, service, "parcel", alice)
	existing := created(t, service, cargopai, alice, "api-engineering")
	w.quiet()

	create := func(productID, key, displayName string) error {
		_, err := service.CreateWorkspace(ctx, CreateWorkspace{
			Caller: alice, CorrelationID: "correlation-3", ProductID: productID, Key: key, DisplayName: displayName,
		})
		return err
	}
	cases := map[string]struct {
		err  error
		want error
	}{
		"a key that is not a key":                    {create(string(cargopai.ID), "API", "API"), domain.ErrInvalid},
		"no display name":                            {create(string(cargopai.ID), "api", ""), domain.ErrInvalid},
		"a key the product has already":              {create(string(cargopai.ID), "api-engineering", "Again"), ports.ErrWorkspaceKeyTaken},
		"a product that does not exist":              {create("00000000-0000-4000-8000-999999999999", "api", "API"), ErrProductNotFound},
		"a product identifier that is no identifier": {create("cargopai", "api", "API"), ErrProductNotFound},
	}
	for name, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: got %v, want %v", name, c.err, c.want)
		}
	}
	if len(w.workspaces) != 1 || w.workspaces[existing.ID] != existing || len(w.audited) != 0 || len(w.events) != 0 {
		t.Errorf("refused creations left workspaces %v, audit %v, events %v", w.workspaces, w.audited, w.events)
	}

	// A key is unique inside its product, not across products.
	if err := create(string(parcel.ID), "api-engineering", "API engineering"); err != nil {
		t.Errorf("the key of a workspace of another product was refused: %v", err)
	}
}

// INV-01: a suspended product takes no new workspace.
func TestCreateWorkspaceRefusesASuspendedProduct(t *testing.T) {
	w, service := newWorld(t)
	product := registered(t, service, "cargopai", alice)
	suspended := product
	suspended.Status = domain.ProductSuspended
	w.products[product.ID] = suspended
	w.quiet()

	_, err := service.CreateWorkspace(ctx, CreateWorkspace{
		Caller: alice, CorrelationID: "correlation-3", ProductID: string(product.ID), Key: "api", DisplayName: "API",
	})
	if !errors.Is(err, domain.ErrProductNotActive) {
		t.Fatalf("got %v, want %v", err, domain.ErrProductNotActive)
	}
	if len(w.workspaces) != 0 || len(w.events) != 0 {
		t.Errorf("a suspended product got the workspaces %v and the events %v", w.workspaces, w.events)
	}
}

func TestCreateWorkspaceLeavesNothingWhenAnyPartOfItFails(t *testing.T) {
	down := errors.New("down")
	for _, step := range []string{"audit", "events", "commit", "read"} {
		t.Run(step+" fails", func(t *testing.T) {
			w, service := newWorld(t)
			product := registered(t, service, "cargopai", alice)
			w.quiet()
			w.failing[step] = down

			_, err := service.CreateWorkspace(ctx, CreateWorkspace{
				Caller: alice, CorrelationID: "correlation-3", ProductID: string(product.ID), Key: "api", DisplayName: "API",
			})
			if !errors.Is(err, down) {
				t.Fatalf("got %v, want the failure", err)
			}
			if len(w.workspaces)+len(w.audited)+len(w.events) != 0 {
				t.Errorf("a failed creation left workspaces %v, audit %v, events %v", w.workspaces, w.audited, w.events)
			}
		})
	}
}

func TestGetWorkspaceAnswersWhoeverHoldsARoleInTheProduct(t *testing.T) {
	w, service := newWorld(t)
	cargopai := registered(t, service, "cargopai", alice)
	parcel := registered(t, service, "parcel", bob)
	workspace := created(t, service, cargopai, alice, "api-engineering")
	other := created(t, service, parcel, bob, "api-engineering")
	w.quiet()

	get := func(caller domain.Principal, productID domain.ProductID, workspaceID string) (domain.Workspace, error) {
		return service.GetWorkspace(ctx, GetWorkspace{
			Caller: caller, CorrelationID: "correlation-4", ProductID: string(productID), WorkspaceID: workspaceID,
		})
	}

	if got, err := get(alice, cargopai.ID, string(workspace.ID)); err != nil || got != workspace {
		t.Fatalf("an owner: got %+v and %v, want the workspace", got, err)
	}
	if len(w.audited) != 0 {
		t.Fatalf("a read that was allowed left the audit entries %+v", w.audited)
	}

	missing := map[string]struct {
		workspaceID string
	}{
		"a workspace that does not exist": {"00000000-0000-4000-8000-999999999999"},
		// The product named in the request is checked against the workspace:
		// an owner of CargoPai does not reach a workspace of Parcel by
		// putting its identifier under CargoPai.
		"a workspace of another product":      {string(other.ID)},
		"an identifier that is no identifier": {"api-engineering"},
	}
	for name, c := range missing {
		if got, err := get(alice, cargopai.ID, c.workspaceID); err != ErrWorkspaceNotFound || got != (domain.Workspace{}) {
			t.Errorf("%s: got %+v and %v, want exactly %v", name, got, err, ErrWorkspaceNotFound)
		}
	}
	if len(w.audited) != 0 {
		t.Errorf("an owner who asked for missing workspaces left the audit entries %+v", w.audited)
	}
}

// A workspace is inside its product. Whoever holds no role there learns
// nothing of it: a stranger not even that the product exists, the platform
// administrator only that.
func TestGetWorkspaceHidesAWorkspaceFromWhoeverHoldsNoRoleInItsProduct(t *testing.T) {
	cases := map[string]struct {
		caller domain.Principal
		want   error
	}{
		"a stranger":                   {mallory, ErrProductNotFound},
		"the owner of another product": {bob, ErrProductNotFound},
		"the platform administrator":   {admin, ErrWorkspaceNotFound},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w, service := newWorld(t)
			cargopai := registered(t, service, "cargopai", alice)
			registered(t, service, "parcel", bob)
			workspace := created(t, service, cargopai, alice, "api-engineering")
			w.quiet()

			for _, asked := range []struct{ id, recorded string }{
				{string(workspace.ID), string(workspace.ID)},
				// What does not exist is answered the same and names no
				// resource in the record.
				{"00000000-0000-4000-8000-999999999999", ""},
			} {
				w.quiet()
				got, err := service.GetWorkspace(ctx, GetWorkspace{
					Caller: c.caller, CorrelationID: "correlation-4", ProductID: string(cargopai.ID), WorkspaceID: asked.id,
				})
				if err != c.want || got != (domain.Workspace{}) {
					t.Fatalf("got %+v and %v, want nothing and exactly %v", got, err, c.want)
				}
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "workspace.read", Rule: "DENY-03",
					ProductID: cargopai.ID, ResourceType: "Workspace", ResourceID: asked.recorded, CorrelationID: "correlation-4",
				}, false}}
				if !slices.Equal(w.audited, want) {
					t.Errorf("audited\n %+v\nwant\n %+v", w.audited, want)
				}
			}
		})
	}

	t.Run("a product that does not exist", func(t *testing.T) {
		w, service := newWorld(t)
		_, err := service.GetWorkspace(ctx, GetWorkspace{
			Caller: mallory, ProductID: "00000000-0000-4000-8000-999999999999", WorkspaceID: "00000000-0000-4000-8000-999999999998",
		})
		if err != ErrProductNotFound || len(w.audited) != 0 {
			t.Fatalf("got %v and the audit entries %+v, want exactly %v and none", err, w.audited, ErrProductNotFound)
		}
	})
}

// A refusal stands whether or not it could be recorded: the caller is never
// let in because the audit trail is down.
func TestARefusalThatCannotBeAuditedIsStillARefusal(t *testing.T) {
	w, service := newWorld(t)
	product := registered(t, service, "cargopai", alice)
	w.quiet()
	w.failing["audit"] = errors.New("the audit trail is down")

	command := registration()
	command.Caller, command.Key = mallory, "intruder"
	_, err := service.RegisterProduct(ctx, command)
	if !errors.Is(err, ErrForbidden) || !errors.Is(err, ErrRefusalNotAudited) || !strings.Contains(err.Error(), "the audit trail is down") {
		t.Errorf("a registration: got %v, want a refusal that says why it could not be audited", err)
	}
	_, err = service.GetProduct(ctx, GetProduct{Caller: mallory, ProductID: string(product.ID)})
	if !errors.Is(err, ErrProductNotFound) || !errors.Is(err, ErrRefusalNotAudited) {
		t.Errorf("a read: got %v, want %v and %v", err, ErrProductNotFound, ErrRefusalNotAudited)
	}
	_, err = service.CreateWorkspace(ctx, CreateWorkspace{Caller: mallory, ProductID: string(product.ID), Key: "api", DisplayName: "API"})
	if !errors.Is(err, ErrProductNotFound) || !errors.Is(err, ErrRefusalNotAudited) {
		t.Errorf("a creation: got %v, want %v and %v", err, ErrProductNotFound, ErrRefusalNotAudited)
	}
	if len(w.products) != 1 || len(w.workspaces) != 0 {
		t.Errorf("refusals left %d products and %d workspaces, want 1 and 0", len(w.products), len(w.workspaces))
	}
}

func TestEveryUseCaseRefusesARequestWithoutAPrincipal(t *testing.T) {
	w, service := newWorld(t)
	product := registered(t, service, "cargopai", alice)
	workspace := created(t, service, product, alice, "api")
	w.quiet()

	membership := joined(t, service, workspace, alice, bob, domain.RoleInternalAuthor)
	second := named(t, service, product, alice, bob)
	w.quiet()
	roles := maps.Clone(w.roles)

	nobody := domain.Principal{}
	productID, workspaceID, membershipID := string(product.ID), string(workspace.ID), string(membership.ID)
	_, errGetProduct := service.GetProduct(ctx, GetProduct{Caller: nobody, ProductID: productID})
	_, errCreate := service.CreateWorkspace(ctx, CreateWorkspace{Caller: nobody, ProductID: productID, Key: "other", DisplayName: "Other"})
	_, errGetWorkspace := service.GetWorkspace(ctx, GetWorkspace{Caller: nobody, ProductID: productID, WorkspaceID: workspaceID})
	_, errGrant := service.GrantMembership(ctx, GrantMembership{
		Caller: nobody, ProductID: productID, WorkspaceID: workspaceID, Member: carol, Role: string(domain.RoleInternalAuthor),
	})
	_, errGetMembership := service.GetMembership(ctx, GetMembership{Caller: nobody, ProductID: productID, WorkspaceID: workspaceID, MembershipID: membershipID})
	errRevoke := service.RevokeMembership(ctx, RevokeMembership{Caller: nobody, ProductID: productID, WorkspaceID: workspaceID, MembershipID: membershipID})
	_, errGrantRole := service.GrantProductRole(ctx, GrantProductRole{
		Caller: nobody, ProductID: productID, Principal: carol, Role: string(domain.RoleProductOwner),
	})
	_, errGetRole := service.GetProductRole(ctx, GetProductRole{Caller: nobody, ProductID: productID, RoleAssignmentID: string(second.ID)})
	errRevokeRole := service.RevokeProductRole(ctx, RevokeProductRole{Caller: nobody, ProductID: productID, RoleAssignmentID: string(second.ID)})
	for name, err := range map[string]error{
		"GetProduct": errGetProduct, "CreateWorkspace": errCreate, "GetWorkspace": errGetWorkspace,
		"GrantMembership": errGrant, "GetMembership": errGetMembership, "RevokeMembership": errRevoke,
		"GrantProductRole": errGrantRole, "GetProductRole": errGetRole, "RevokeProductRole": errRevokeRole,
	} {
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: got %v, want %v", name, err, ErrForbidden)
		}
	}
	if len(w.workspaces) != 1 || len(w.audited) != 0 || w.transactions != 0 {
		t.Errorf("requests without a principal left %d workspaces, the audit entries %+v and %d transactions", len(w.workspaces), w.audited, w.transactions)
	}
	if len(w.memberships) != 1 || w.memberships[membership.ID] != membership {
		t.Errorf("requests without a principal left the memberships %+v, want the one there was, in force", w.memberships)
	}
	if !maps.Equal(w.roles, roles) {
		t.Errorf("requests without a principal left the roles %+v, want them as they were", w.roles)
	}
}

func TestTheUseCasesAreWiredCompletelyOrNotAtAll(t *testing.T) {
	w, _ := newWorld(t)
	complete := w.wiring()
	if _, err := New(complete); err != nil {
		t.Fatalf("a complete wiring was refused: %v", err)
	}

	for name, remove := range map[string]func(d *Dependencies){
		"transactor":     func(d *Dependencies) { d.Transactor = nil },
		"products":       func(d *Dependencies) { d.Products = nil },
		"workspaces":     func(d *Dependencies) { d.Workspaces = nil },
		"roles":          func(d *Dependencies) { d.Roles = nil },
		"memberships":    func(d *Dependencies) { d.Memberships = nil },
		"administrators": func(d *Dependencies) { d.Administrators = nil },
		"issuers":        func(d *Dependencies) { d.Issuers = nil },
		"audit":          func(d *Dependencies) { d.Audit = nil },
		"events":         func(d *Dependencies) { d.Events = nil },
		"clock":          func(d *Dependencies) { d.Clock = nil },
		"identifiers":    func(d *Dependencies) { d.Identifiers = nil },
	} {
		incomplete := complete
		remove(&incomplete)
		if _, err := New(incomplete); err == nil {
			t.Errorf("a wiring without %s was accepted", name)
		}
	}
}
