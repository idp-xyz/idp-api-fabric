package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	productID   = ProductID("1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6")
	workspaceID = WorkspaceID("7d2e9f10-3a4b-4c5d-8e6f-0a1b2c3d4e5f")
)

var (
	admin   = Principal{Issuer: "https://issuer.example", Subject: "platform-admin"}
	alice   = Principal{Issuer: "https://issuer.example", Subject: "alice"}
	bob     = Principal{Issuer: "https://issuer.example", Subject: "bob"}
	owner   = OrganizationRef{IdentityProvider: "https://issuer.example", Organization: "org-cargopai"}
	created = time.Date(2026, 11, 11, 17, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
)

func registered(t *testing.T) Product {
	t.Helper()

	product, err := NewProduct(productID, "cargopai", "CargoPai", owner, admin, created)
	if err != nil {
		t.Fatalf("register the product: %v", err)
	}
	return product
}

func TestANewProductIsActiveAtVersionOne(t *testing.T) {
	product := registered(t)

	want := Product{
		ID: productID, Key: "cargopai", DisplayName: "CargoPai", Status: ProductActive, Owner: owner,
		Version: 1, CreatedAt: created.UTC(), CreatedBy: admin, UpdatedAt: created.UTC(),
	}
	if product != want {
		t.Errorf("product\n %+v\nwant\n %+v", product, want)
	}
	if product.CreatedAt.Location() != time.UTC {
		t.Errorf("the product is timed in %s, want UTC", product.CreatedAt.Location())
	}
	if err := product.AdmitNewWork(); err != nil {
		t.Errorf("a new product refuses new work: %v", err)
	}
}

func TestKeys(t *testing.T) {
	accepted := []string{"ab", "cargopai", "idp-parcel", "a1", "api-engineering-2", "a" + strings.Repeat("b", 62)}
	for _, key := range accepted {
		if _, err := NewProduct(productID, key, "CargoPai", owner, admin, created); err != nil {
			t.Errorf("the key %q was refused: %v", key, err)
		}
	}

	refused := map[string]string{
		"empty":                "",
		"one character":        "a",
		"too long":             "a" + strings.Repeat("b", 63),
		"upper case":           "CargoPai",
		"starts with a digit":  "1cargo",
		"starts with a hyphen": "-cargo",
		"ends with a hyphen":   "cargo-",
		"an underscore":        "cargo_pai",
		"a space":              "cargo pai",
		"a dot":                "cargo.pai",
		"a slash":              "cargo/pai",
		"not ASCII":            "cargopaï",
		"a line break":         "cargo\npai",
	}
	for name, key := range refused {
		t.Run(name, func(t *testing.T) {
			if _, err := NewProduct(productID, key, "CargoPai", owner, admin, created); !errors.Is(err, ErrInvalid) {
				t.Errorf("a product key: got %v, want %v", err, ErrInvalid)
			}
			if _, err := NewWorkspace(workspaceID, registered(t), key, "API engineering", alice, created); !errors.Is(err, ErrInvalid) {
				t.Errorf("a workspace key: got %v, want %v", err, ErrInvalid)
			}
		})
	}
}

func TestDisplayNames(t *testing.T) {
	accepted := []string{"C", "CargoPai", "IDP 跨境小包", "Cargo Pai (EU)", strings.Repeat("名", 120)}
	for _, name := range accepted {
		if _, err := NewProduct(productID, "cargopai", name, owner, admin, created); err != nil {
			t.Errorf("the name %q was refused: %v", name, err)
		}
	}

	refused := map[string]string{
		"empty":            "",
		"too long":         strings.Repeat("名", 121),
		"a leading space":  " CargoPai",
		"a trailing space": "CargoPai ",
		"only spaces":      "   ",
		"a line break":     "Cargo\nPai",
		"a tab":            "Cargo\tPai",
		"not UTF-8":        "Cargo\xffPai",
	}
	for name, displayName := range refused {
		t.Run(name, func(t *testing.T) {
			if _, err := NewProduct(productID, "cargopai", displayName, owner, admin, created); !errors.Is(err, ErrInvalid) {
				t.Errorf("a product name: got %v, want %v", err, ErrInvalid)
			}
			if _, err := NewWorkspace(workspaceID, registered(t), "api", displayName, alice, created); !errors.Is(err, ErrInvalid) {
				t.Errorf("a workspace name: got %v, want %v", err, ErrInvalid)
			}
		})
	}
}

func TestAProductNeedsAnIdentifierAnOwnerARegistrantAndATime(t *testing.T) {
	cases := map[string]func() (Product, error){
		"an identifier that is not a UUID": func() (Product, error) {
			return NewProduct("product-cp", "cargopai", "CargoPai", owner, admin, created)
		},
		"an identifier in upper case": func() (Product, error) {
			return NewProduct(ProductID(strings.ToUpper(string(productID))), "cargopai", "CargoPai", owner, admin, created)
		},
		"no identity provider of the owner": func() (Product, error) {
			return NewProduct(productID, "cargopai", "CargoPai", OrganizationRef{Organization: "org"}, admin, created)
		},
		"no organization of the owner": func() (Product, error) {
			return NewProduct(productID, "cargopai", "CargoPai", OrganizationRef{IdentityProvider: "idp"}, admin, created)
		},
		"no registrant": func() (Product, error) {
			return NewProduct(productID, "cargopai", "CargoPai", owner, Principal{}, created)
		},
		"no time": func() (Product, error) {
			return NewProduct(productID, "cargopai", "CargoPai", owner, admin, time.Time{})
		},
	}
	for name, register := range cases {
		t.Run(name, func(t *testing.T) {
			if product, err := register(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %+v and %v, want %v", product, err, ErrInvalid)
			}
		})
	}
}

func TestPrincipals(t *testing.T) {
	accepted := []Principal{
		alice,
		{Issuer: "https://issuer.example", Subject: "auth0|5f7c8ec7c33c6c004bbafe82"},
		{Issuer: "https://issuer.example", Subject: "service account 7"},
		{Issuer: "https://issuer.example", Subject: strings.Repeat("s", 255)},
	}
	for _, principal := range accepted {
		if err := principal.Validate(); err != nil {
			t.Errorf("%+v was refused: %v", principal, err)
		}
	}

	refused := map[string]Principal{
		"no issuer":                   {Subject: "alice"},
		"no subject":                  {Issuer: "https://issuer.example"},
		"a subject that is too long":  {Issuer: "https://issuer.example", Subject: strings.Repeat("s", 256)},
		"an issuer that is too long":  {Issuer: strings.Repeat("i", 256), Subject: "alice"},
		"a subject with a line break": {Issuer: "https://issuer.example", Subject: "alice\nadmin"},
		"a subject that is not UTF-8": {Issuer: "https://issuer.example", Subject: "al\xffice"},
	}
	for name, principal := range refused {
		t.Run(name, func(t *testing.T) {
			if err := principal.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want %v", err, ErrInvalid)
			}
		})
	}
}

func TestTheFirstOwnersAreGrantedByTheRegistrantAtRegistration(t *testing.T) {
	product := registered(t)

	assignments, err := product.AssignFirstOwners([]Principal{alice, bob}, admin)
	if err != nil {
		t.Fatalf("assign the owners: %v", err)
	}
	if len(assignments) != 2 {
		t.Fatalf("%d assignments, want 2", len(assignments))
	}
	for index, principal := range []Principal{alice, bob} {
		want := RoleAssignment{
			ProductID: productID, Principal: principal, Role: RoleProductOwner, GrantedAt: product.CreatedAt, GrantedBy: admin,
		}
		if assignments[index] != want {
			t.Errorf("assignment %d is %+v, want %+v", index, assignments[index], want)
		}
	}
}

func TestAProductIsRegisteredWithOneToTenDistinctOwners(t *testing.T) {
	product := registered(t)

	eleven := make([]Principal, 11)
	for index := range eleven {
		eleven[index] = Principal{Issuer: alice.Issuer, Subject: "owner-" + string(rune('a'+index))}
	}
	cases := map[string][]Principal{
		"no owner":                       nil,
		"eleven owners":                  eleven,
		"the same owner twice":           {alice, bob, alice},
		"an owner that is no principal":  {alice, {Issuer: alice.Issuer}},
		"an owner with a control letter": {{Issuer: alice.Issuer, Subject: "al\x00ice"}},
	}
	for name, owners := range cases {
		t.Run(name, func(t *testing.T) {
			if assignments, err := product.AssignFirstOwners(owners, admin); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %+v and %v, want %v", assignments, err, ErrInvalid)
			}
		})
	}
	if _, err := product.AssignFirstOwners(eleven[:10], admin); err != nil {
		t.Errorf("ten owners were refused: %v", err)
	}
	// The same subject under another issuer is another principal.
	if _, err := product.AssignFirstOwners([]Principal{alice, {Issuer: "https://other.example", Subject: alice.Subject}}, admin); err != nil {
		t.Errorf("one subject under two issuers was refused: %v", err)
	}
}

func TestANewWorkspaceBelongsToItsProduct(t *testing.T) {
	product := registered(t)

	workspace, err := NewWorkspace(workspaceID, product, "api-engineering", "API engineering", alice, created)
	if err != nil {
		t.Fatalf("create the workspace: %v", err)
	}
	want := Workspace{
		ID: workspaceID, ProductID: productID, Key: "api-engineering", DisplayName: "API engineering",
		Status: WorkspaceActive, Version: 1, CreatedAt: created.UTC(), CreatedBy: alice, UpdatedAt: created.UTC(),
	}
	if workspace != want {
		t.Errorf("workspace\n %+v\nwant\n %+v", workspace, want)
	}
}

// INV-01: a suspended product refuses new registrations. What it holds stays.
func TestASuspendedProductTakesNoWorkspace(t *testing.T) {
	product := registered(t)
	product.Status = ProductSuspended

	if err := product.AdmitNewWork(); !errors.Is(err, ErrProductNotActive) {
		t.Fatalf("got %v, want %v", err, ErrProductNotActive)
	}
	if workspace, err := NewWorkspace(workspaceID, product, "api-engineering", "API engineering", alice, created); !errors.Is(err, ErrProductNotActive) {
		t.Fatalf("got %+v and %v, want %v", workspace, err, ErrProductNotActive)
	}
}

func TestAWorkspaceNeedsAnIdentifierACreatorAndATime(t *testing.T) {
	product := registered(t)

	cases := map[string]func() (Workspace, error){
		"an identifier that is not a UUID": func() (Workspace, error) {
			return NewWorkspace("ws-cp-api", product, "api", "API", alice, created)
		},
		"no creator": func() (Workspace, error) {
			return NewWorkspace(workspaceID, product, "api", "API", Principal{}, created)
		},
		"no time": func() (Workspace, error) {
			return NewWorkspace(workspaceID, product, "api", "API", alice, time.Time{})
		},
	}
	for name, create := range cases {
		t.Run(name, func(t *testing.T) {
			if workspace, err := create(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %+v and %v, want %v", workspace, err, ErrInvalid)
			}
		})
	}
}

func TestIdentifiersAreCanonicalUUIDs(t *testing.T) {
	if id, err := ParseProductID(string(productID)); err != nil || id != productID {
		t.Errorf("got %q and %v, want the identifier back", id, err)
	}
	if id, err := ParseWorkspaceID(string(workspaceID)); err != nil || id != workspaceID {
		t.Errorf("got %q and %v, want the identifier back", id, err)
	}
	for _, value := range []string{"", "cargopai", strings.ToUpper(string(productID)), string(productID) + "0", "{" + string(productID) + "}", strings.ReplaceAll(string(productID), "-", "")} {
		if _, err := ParseProductID(value); !errors.Is(err, ErrInvalid) {
			t.Errorf("product identifier %q: got %v, want %v", value, err, ErrInvalid)
		}
		if _, err := ParseWorkspaceID(value); !errors.Is(err, ErrInvalid) {
			t.Errorf("workspace identifier %q: got %v, want %v", value, err, ErrInvalid)
		}
	}
}

// The errors are written for a log and for a caller. They repeat nothing of
// the value they refuse.
func TestAnErrorRepeatsNothingOfTheValue(t *testing.T) {
	_, err := NewProduct(productID, "Canary-Key", "CargoPai", owner, admin, created)
	if err == nil || strings.Contains(err.Error(), "Canary") {
		t.Errorf("the error of a refused key is %v", err)
	}
	_, err = NewProduct(productID, "cargopai", " canary name ", owner, admin, created)
	if err == nil || strings.Contains(err.Error(), "canary") {
		t.Errorf("the error of a refused name is %v", err)
	}
}
