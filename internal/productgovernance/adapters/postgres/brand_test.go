package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
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

// rebranded is the product as it is once by has changed its brand, an hour
// after its last change.
func rebranded(t *testing.T, product domain.Product, brand domain.Brand, by domain.Principal) domain.Product {
	t.Helper()

	changed, err := product.ChangeBrand(brand, by, product.UpdatedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("change the brand: %v", err)
	}
	if changed.Version != product.Version+1 {
		t.Fatalf("the brand of %s is %+v already: nothing to change", product.Key, brand)
	}
	return changed
}

func (f fixture) rebrand(t *testing.T, changed domain.Product) {
	t.Helper()

	f.within(t, func(ctx context.Context) error { return f.products.ChangeBrand(ctx, changed) })
}

// registeredBefore is how a build before the brand registered a product, and
// registers one while this build is rolled out: its row says nothing of a
// brand, or of who changed the product last.
const registeredBefore = `
	INSERT INTO productgovernance.product
	    (product_id, product_key, display_name, status, owner_identity_provider, owner_organization,
	     aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at)
	VALUES ($1, $2, $3, 'ACTIVE', $4, $5, 1, $6, $7, $8, $6)`

func (f fixture) addAsTheBuildBefore(t *testing.T, product domain.Product) {
	t.Helper()

	if _, err := f.app.Exec(t.Context(), registeredBefore, string(product.ID), product.Key, product.DisplayName,
		product.Owner.IdentityProvider, product.Owner.Organization,
		product.CreatedAt, product.CreatedBy.Issuer, product.CreatedBy.Subject); err != nil {
		t.Fatalf("register %s as the build before: %v", product.Key, err)
	}
}

// productRecord is what the table keeps of the brand of one product, and of
// who changed the product last.
type productRecord struct {
	Status       string
	Version      int64
	HasName      bool
	Name         string
	HasLogo      bool
	Logo         string
	HasColour    bool
	Colour       string
	UpdatedAt    time.Time
	HasUpdatedBy bool
	UpdatedBy    string
}

func (f fixture) productRecord(t *testing.T, id domain.ProductID) productRecord {
	t.Helper()

	var kept productRecord
	if err := f.app.QueryRow(t.Context(), `
		SELECT status, aggregate_version,
		       brand_name IS NOT NULL, coalesce(brand_name, ''),
		       brand_logo_url IS NOT NULL, coalesce(brand_logo_url, ''),
		       brand_primary_color IS NOT NULL, coalesce(brand_primary_color, ''),
		       updated_at, updated_by_issuer IS NOT NULL AND updated_by_subject IS NOT NULL, coalesce(updated_by_subject, '')
		FROM productgovernance.product WHERE product_id = $1`, string(id),
	).Scan(&kept.Status, &kept.Version, &kept.HasName, &kept.Name, &kept.HasLogo, &kept.Logo, &kept.HasColour, &kept.Colour,
		&kept.UpdatedAt, &kept.HasUpdatedBy, &kept.UpdatedBy); err != nil {
		t.Fatalf("read the record of the product: %v", err)
	}
	return kept
}

// The brand of a product is stored with the product and read back as it was
// stored. A change says who made it and when, and what a brand leaves out
// the row holds nothing of.
func TestTheBrandOfAProductIsReadBackAsItWasStored(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)

	// A product is stored as it was registered: under its display name,
	// without a logo and without a colour, last changed by its registrant.
	if kept := f.productRecord(t, cargopai.ID); kept.Version != 1 || !kept.HasName || kept.Name != cargopai.DisplayName ||
		kept.HasLogo || kept.HasColour || !kept.HasUpdatedBy || kept.UpdatedBy != admin.Subject || !kept.UpdatedAt.Equal(cargopai.CreatedAt) {
		t.Errorf("the record of a product that was just registered is %+v", kept)
	}

	changed := rebranded(t, cargopai, developers, alice)
	f.within(t, func(ctx context.Context) error {
		if err := f.products.ChangeBrand(ctx, changed); err != nil {
			return err
		}
		// Inside the transaction that changed it, it is changed already.
		if got, found, err := f.products.Find(ctx, cargopai.ID); err != nil || !found || got != changed {
			t.Errorf("inside the transaction that changed it the product is %+v (found %t, %v)", got, found, err)
		}
		return nil
	})
	if got, found, err := f.products.Find(t.Context(), cargopai.ID); err != nil || !found || got != changed {
		t.Errorf("product\n %+v (found %t, %v)\nwant\n %+v", got, found, err, changed)
	}
	if kept := f.productRecord(t, cargopai.ID); kept.Version != 2 || kept.Name != developers.Name || kept.Logo != developers.LogoURL ||
		kept.Colour != developers.PrimaryColor || kept.UpdatedBy != alice.Subject || !kept.UpdatedAt.Equal(changed.UpdatedAt) || kept.Status != "ACTIVE" {
		t.Errorf("the record is %+v, want version 2 with the brand alice gave it at %s", kept, changed.UpdatedAt)
	}

	// A brand is replaced as a whole. One without a logo and a colour
	// leaves the columns without one.
	plain := rebranded(t, changed, domain.Brand{Name: "CargoPai 开放平台"}, bob)
	f.rebrand(t, plain)
	if got, found, err := f.products.Find(t.Context(), cargopai.ID); err != nil || !found || got != plain {
		t.Errorf("under its name alone the product is %+v (found %t, %v), want\n %+v", got, found, err, plain)
	}
	if kept := f.productRecord(t, cargopai.ID); kept.Version != 3 || kept.Name != "CargoPai 开放平台" || kept.HasLogo || kept.HasColour || kept.UpdatedBy != bob.Subject {
		t.Errorf("the record is %+v, want version 3 with a name and nothing else, changed by bob", kept)
	}

	// A list reads a product as a single read does, and so does a lock.
	if products, err := f.products.List(t.Context(), ports.Page{Limit: 10}); err != nil || !slices.Equal(products, []domain.Product{plain}) {
		t.Errorf("the list holds %+v (%v), want the product as it is", products, err)
	}
	f.within(t, func(ctx context.Context) error {
		got, found, err := f.products.Lock(ctx, cargopai.ID)
		if err == nil && (!found || got != plain) {
			t.Errorf("locked, the product is %+v (found %t), want it as it is", got, found)
		}
		return err
	})
}

// A build before the brand registered products, and registers more while
// this build is rolled out. Such a product is as it was registered: under
// its display name, last changed by its registrant. Its brand is changed
// like that of any other product, and from then on its row says so.
func TestAProductABuildBeforeTheBrandRegisteredIsAsItWasRegistered(t *testing.T) {
	f := newFixture(t)
	legacy, other := product(t, "legacy"), product(t, "legacy-two")
	f.addAsTheBuildBefore(t, legacy)
	f.addAsTheBuildBefore(t, other)

	if kept := f.productRecord(t, legacy.ID); kept.HasName || kept.HasLogo || kept.HasColour || kept.HasUpdatedBy || kept.Version != 1 {
		t.Fatalf("the record is %+v, want a row that says nothing of a brand or of who changed it", kept)
	}
	if got, found, err := f.products.Find(t.Context(), legacy.ID); err != nil || !found || got != legacy {
		t.Errorf("product\n %+v (found %t, %v)\nwant it as it was registered\n %+v", got, found, err, legacy)
	}
	if products, err := f.products.List(t.Context(), ports.Page{Limit: 10}); err != nil || len(products) != 2 ||
		!slices.Contains(products, legacy) || !slices.Contains(products, other) {
		t.Errorf("the list holds %+v (%v), want both products as they were registered", products, err)
	}
	f.within(t, func(ctx context.Context) error {
		got, found, err := f.products.Lock(ctx, legacy.ID)
		if err == nil && (!found || got != legacy) {
			t.Errorf("locked, the product is %+v (found %t), want it as it was registered", got, found)
		}
		return err
	})

	changed := rebranded(t, legacy, developers, alice)
	f.rebrand(t, changed)
	if got, found, err := f.products.Find(t.Context(), legacy.ID); err != nil || !found || got != changed {
		t.Errorf("product\n %+v (found %t, %v)\nwant\n %+v", got, found, err, changed)
	}
	if kept := f.productRecord(t, legacy.ID); kept.Version != 2 || kept.Name != developers.Name || !kept.HasUpdatedBy || kept.UpdatedBy != alice.Subject {
		t.Errorf("the record is %+v, want version 2 under the name alice gave it", kept)
	}

	// A logo alone is a change of the brand. The name the product presents
	// itself under is still its display name, and the row now says so.
	withLogo := rebranded(t, other, domain.Brand{Name: other.DisplayName, LogoURL: developers.LogoURL}, bob)
	f.rebrand(t, withLogo)
	if got, found, err := f.products.Find(t.Context(), other.ID); err != nil || !found || got != withLogo {
		t.Errorf("product\n %+v (found %t, %v)\nwant\n %+v", got, found, err, withLogo)
	}
	if kept := f.productRecord(t, other.ID); !kept.HasName || kept.Name != other.DisplayName || kept.Logo != developers.LogoURL || kept.HasColour {
		t.Errorf("the record is %+v, want its display name for a name, and the logo", kept)
	}
}

// A change of the brand is stored over the version it was made from. One
// made from a version the product is not at any more finds nothing to
// change, and does not fail its transaction.
func TestAChangeOfTheBrandIsStoredOverTheVersionItWasMadeFrom(t *testing.T) {
	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, cargopai)
	f.add(t, parcel)

	changed := rebranded(t, cargopai, developers, alice)
	f.rebrand(t, changed)

	stale := rebranded(t, cargopai, domain.Brand{Name: "Stale"}, mallory)
	ahead := rebranded(t, changed, domain.Brand{Name: "Ahead"}, mallory)
	ahead.Version++
	later := product(t, "later")
	f.within(t, func(ctx context.Context) error {
		for name, attempt := range map[string]domain.Product{
			"a change made from the version before": stale,
			"a change that skips a version":         ahead,
			"a change of a product nobody stored":   rebranded(t, product(t, "nobody"), domain.Brand{Name: "Nobody"}, mallory),
		} {
			if err := f.products.ChangeBrand(ctx, attempt); !errors.Is(err, ports.ErrProductChanged) {
				t.Errorf("%s: got %v, want %v", name, err, ports.ErrProductChanged)
			}
		}
		// The same transaction still writes.
		return f.products.Add(ctx, later)
	})

	if got, found, err := f.products.Find(t.Context(), cargopai.ID); err != nil || !found || got != changed {
		t.Errorf("the product is %+v (found %t, %v), want it as the change of alice left it", got, found, err)
	}
	if got, found, err := f.products.Find(t.Context(), parcel.ID); err != nil || !found || got != parcel {
		t.Errorf("the other product is %+v (found %t, %v), want it untouched", got, found, err)
	}
	if _, found, err := f.products.Find(t.Context(), later.ID); err != nil || !found {
		t.Errorf("what the transaction wrote after the refusals: found %t, %v", found, err)
	}

	// A product is at version 1 until its brand changes. The store refuses
	// a change that claims to have left it there, before it sends anything.
	err := f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error { return f.products.ChangeBrand(ctx, parcel) })
	if err == nil || errors.Is(err, ports.ErrProductChanged) {
		t.Errorf("a change at the version the product was registered at: got %v, want it refused for what it is", err)
	}
}

func TestTwoChangesOfTheBrandFromOneVersionAtOnceLeaveOne(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)

	const racers = 6
	results := make(chan error, racers)
	for index := range racers {
		changed := rebranded(t, cargopai, domain.Brand{Name: fmt.Sprintf("Racer %d", index)},
			domain.Principal{Issuer: alice.Issuer, Subject: fmt.Sprintf("owner-%d", index)})
		go func() {
			results <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				return f.products.ChangeBrand(ctx, changed)
			})
		}()
	}
	won := 0
	for range racers {
		switch err := <-results; {
		case err == nil:
			won++
		case !errors.Is(err, ports.ErrProductChanged):
			t.Errorf("a change failed with %v, want it to win or to find the product changed", err)
		}
	}
	kept := f.productRecord(t, cargopai.ID)
	winner := strings.TrimPrefix(kept.UpdatedBy, "owner-")
	if won != 1 || kept.Version != 2 || kept.Name != "Racer "+winner {
		t.Errorf("%d changes succeeded and the record is %+v, want 1 and the brand of the one that won", won, kept)
	}
}

// A change of the brand decides from the product as it is locked. Who locks
// it second waits for the first, and then reads what the first left: the
// product at its next version. A read waits for nobody, and neither does
// what is added to the product in the meantime.
func TestWhoeverLocksAProductSecondWaitsAndReadsWhatTheFirstLeft(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)
	changed := rebranded(t, cargopai, developers, bob)

	type read struct {
		product domain.Product
		found   bool
		err     error
	}
	// The first transaction locks the product, says what it read and holds
	// it until it is told to go on. Then it changes the brand.
	locked, proceed, finished := make(chan read, 1), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	release := func() { once.Do(func() { close(proceed) }) }
	defer release()
	go func() {
		finished <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
			product, found, err := f.products.Lock(ctx, cargopai.ID)
			locked <- read{product, found, err}
			if err != nil {
				return err
			}
			<-proceed
			return f.products.ChangeBrand(ctx, changed)
		})
	}()
	if held := <-locked; held.err != nil || !held.found || held.product != cargopai {
		t.Fatalf("the first transaction locked %+v (found %t, %v)", held.product, held.found, held.err)
	}

	waited := make(chan read, 1)
	go func() {
		var second read
		second.err = f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) (err error) {
			second.product, second.found, err = f.products.Lock(ctx, cargopai.ID)
			return err
		})
		waited <- second
	}()
	f.waiting(t, 1)
	select {
	case early := <-waited:
		t.Fatalf("the second transaction read %+v while the first held the product, want it to wait", early)
	default:
	}
	// A read is not a lock, and waits for nobody.
	if got, found, err := f.products.Find(t.Context(), cargopai.ID); err != nil || !found || got != cargopai {
		t.Errorf("while it is locked the product reads %+v (found %t, %v), want it as it was", got, found, err)
	}

	// The lock keeps another change of the product out and nothing else. A
	// workspace and an owner need the product to stay, not to stay as it
	// is: they are added while it is held.
	engineering, owner := workspace(t, cargopai, "api-engineering"), owning(t, cargopai, alice)
	added := make(chan error, 1)
	go func() {
		added <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
			if err := f.workspaces.Add(ctx, engineering); err != nil {
				return err
			}
			return f.roles.Add(ctx, owner)
		})
	}()
	select {
	case err := <-added:
		if err != nil {
			t.Errorf("a workspace and an owner added while the product is locked: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a workspace and an owner of the product wait for the lock of its brand")
	}

	release()
	if err := <-finished; err != nil {
		t.Fatalf("the first transaction: %v", err)
	}
	if after := <-waited; after.err != nil || !after.found || after.product != changed {
		t.Errorf("once the first had changed the brand the second locked %+v (found %t, %v), want\n %+v", after.product, after.found, after.err, changed)
	}
	// A product nobody stored is not there to lock.
	f.within(t, func(ctx context.Context) error {
		_, found, err := f.products.Lock(ctx, product(t, "nobody").ID)
		if err == nil && found {
			t.Error("a product nobody stored was locked")
		}
		return err
	})
}

func TestChangingTheBrandNeedsATransaction(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)

	_, _, errLock := f.products.Lock(t.Context(), cargopai.ID)
	for what, err := range map[string]error{
		"a change": f.products.ChangeBrand(t.Context(), rebranded(t, cargopai, developers, bob)),
		// A lock no transaction holds would be given back at once: what was
		// decided from it could change before it is stored.
		"a lock": errLock,
	} {
		if !errors.Is(err, bentopostgres.ErrTransactionRequired) {
			t.Errorf("%s outside a transaction: got %v, want %v", what, err, bentopostgres.ErrTransactionRequired)
		}
	}
	if got, found, err := f.products.Find(t.Context(), cargopai.ID); err != nil || !found || got != cargopai {
		t.Errorf("the product is %+v (found %t, %v), want it as it was", got, found, err)
	}
}

// The schema refuses what the model never produces, whoever writes it. The
// application role holds UPDATE on the table, to change the brand of a
// product, and still changes nothing else of one. The role that owns the
// table sets the status of a product by hand, which no statement of a
// process does yet, and nothing with it.
func TestTheSchemaHoldsTheLineOfAProduct(t *testing.T) {
	const (
		checkViolation = "23514"
		// What the trigger of the table raises.
		integrityViolation    = "23000"
		insufficientPrivilege = "42501"
	)

	f := newFixture(t)
	held, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, held)
	f.add(t, parcel)
	owner := pgtest.Connect(t, f.database.Migrator)

	const (
		update  = `UPDATE productgovernance.product SET `
		counted = `, aggregate_version = aggregate_version + 1 WHERE product_id = $1`
		later   = `updated_at = updated_at + interval '1 hour'`
		// branded is a new brand under a name, by somebody, later than the
		// last change: what the table takes of a product, once it is
		// counted.
		branded = `brand_name = 'CargoPai Developers', ` + later + `, updated_by_issuer = 'https://issuer.example', updated_by_subject = 'mallory'`
	)

	// A new brand, counted, is what the table takes of a product. The cases
	// below that say what a change does besides are built on it, so that
	// each is refused for what it adds and for nothing else.
	attempt, err := f.app.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := attempt.Exec(t.Context(), update+branded+counted, string(held.ID)); err != nil {
		t.Errorf("a new brand, counted: got %v, want the table to take it", err)
	}
	if err := attempt.Rollback(t.Context()); err != nil {
		t.Fatalf("roll back: %v", err)
	}

	statements := []struct {
		name      string
		statement string
		state     string
	}{
		{"give it another key", update + branded + `, product_key = 'renamed'` + counted, integrityViolation},
		{"rename it", update + branded + `, display_name = 'Renamed'` + counted, integrityViolation},
		{"give it another identifier", update + branded + `, product_id = gen_random_uuid()` + counted, integrityViolation},
		{"hand it to another organization", update + branded + `, owner_organization = 'org-elsewhere'` + counted, integrityViolation},
		{"hand it to an organization of another identity provider", update + branded + `, owner_identity_provider = 'https://elsewhere.example'` + counted, integrityViolation},
		{"change when it was registered", update + branded + `, created_at = created_at - interval '1 year'` + counted, integrityViolation},
		{"change under which issuer it was registered", update + branded + `, created_by_issuer = 'https://elsewhere.example'` + counted, integrityViolation},
		{"change who registered it", update + branded + `, created_by_subject = 'mallory'` + counted, integrityViolation},
		{"suspend it", update + `status = 'SUSPENDED' WHERE product_id = $1`, integrityViolation},
		{"suspend it with a new brand", update + branded + `, status = 'SUSPENDED'` + counted, integrityViolation},
		{"change its brand without counting the change", update + branded + ` WHERE product_id = $1`, integrityViolation},
		{"skip a version", update + branded + `, aggregate_version = aggregate_version + 2 WHERE product_id = $1`, integrityViolation},
		{"count a change that did not happen", update + later + `, updated_by_subject = 'mallory'` + counted, integrityViolation},
		{"change its brand before its last change", update + `brand_name = 'CargoPai Developers', updated_at = updated_at - interval '1 second'` + counted, integrityViolation},
		{"change its brand without saying who did", update + `brand_name = 'CargoPai Developers', ` + later + `, updated_by_issuer = NULL, updated_by_subject = NULL` + counted,
			integrityViolation},
		{"take its name away", update + `brand_name = NULL, ` + later + counted, integrityViolation},
		{"say half of who changed it", update + `brand_name = 'CargoPai Developers', ` + later + `, updated_by_subject = NULL` + counted, checkViolation},
		{"give it an empty name", update + `brand_name = '', ` + later + counted, checkViolation},
		{"give it a name too long", update + `brand_name = repeat('n', 121), ` + later + counted, checkViolation},
		{"give it a logo over plain http", update + branded + `, brand_logo_url = 'http://static.cargopai.example/logo.svg'` + counted, checkViolation},
		{"give it an empty logo", update + branded + `, brand_logo_url = ''` + counted, checkViolation},
		{"give it a logo too long", update + branded + `, brand_logo_url = 'https://static.cargopai.example/' || repeat('a', 2048)` + counted, checkViolation},
		{"give it a colour of three digits", update + branded + `, brand_primary_color = '#0A7'` + counted, checkViolation},
		{"give it a colour by its name", update + branded + `, brand_primary_color = 'green'` + counted, checkViolation},
		{"give it a colour and a line after it", update + branded + `, brand_primary_color = E'#0A7B3C\n'` + counted, checkViolation},
		{"remove it", `DELETE FROM productgovernance.product WHERE product_id = $1`, insufficientPrivilege},
	}
	for _, c := range statements {
		t.Run(c.name, func(t *testing.T) {
			if _, err := f.app.Exec(t.Context(), c.statement, string(held.ID)); sqlState(err) != c.state {
				t.Errorf("%s\n got %v, want it refused with SQLSTATE %s", c.statement, err, c.state)
			}
		})
	}
	for _, statement := range []string{
		`TRUNCATE productgovernance.product CASCADE`,
		`ALTER TABLE productgovernance.product DISABLE TRIGGER product_only_changes_its_brand`,
		`DROP TRIGGER product_only_changes_its_brand ON productgovernance.product`,
		`ALTER TABLE productgovernance.product DROP CONSTRAINT product_brand_logo_https`,
		`CREATE OR REPLACE FUNCTION productgovernance.product_only_changes_its_brand() RETURNS trigger
		     LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'`,
	} {
		if _, err := f.app.Exec(t.Context(), statement); sqlState(err) != insufficientPrivilege {
			t.Errorf("%s\n got %v, want it refused for lack of privilege", statement, err)
		}
	}

	// Nothing of it took effect.
	if got, found, err := f.products.Find(t.Context(), held.ID); err != nil || !found || got != held {
		t.Errorf("the product is %+v (found %t, %v), want it untouched", got, found, err)
	}

	// The role that owns the table is held to the same line, but for the
	// status: it suspends a product and resumes it, and nothing with it.
	for name, c := range map[string]struct{ statement, state string }{
		"give it another key":             {update + branded + `, product_key = 'renamed'` + counted, integrityViolation},
		"suspend it with a new brand":     {update + branded + `, status = 'SUSPENDED'` + counted, integrityViolation},
		"suspend it and count that":       {update + `status = 'SUSPENDED'` + counted, integrityViolation},
		"suspend it and say who did":      {update + `status = 'SUSPENDED', updated_by_subject = 'mallory' WHERE product_id = $1`, integrityViolation},
		"give it a status nobody defined": {update + `status = 'ARCHIVED' WHERE product_id = $1`, checkViolation},
	} {
		if _, err := owner.Exec(t.Context(), c.statement, string(held.ID)); sqlState(err) != c.state {
			t.Errorf("the owner of the table, to %s:\n %s\n got %v, want it refused with SQLSTATE %s", name, c.statement, err, c.state)
		}
	}
	suspended := held
	suspended.Status = domain.ProductSuspended
	for _, step := range []struct {
		status string
		want   domain.Product
	}{{"SUSPENDED", suspended}, {"ACTIVE", held}} {
		if _, err := owner.Exec(t.Context(), update+`status = $2 WHERE product_id = $1`, string(held.ID), step.status); err != nil {
			t.Fatalf("the owner of the table sets the status %s: %v", step.status, err)
		}
		if got, found, err := f.products.Find(t.Context(), held.ID); err != nil || !found || got != step.want {
			t.Errorf("once its status is %s the product is %+v (found %t, %v), want nothing else to have moved:\n %+v", step.status, got, found, err, step.want)
		}
	}
	if got, found, err := f.products.Find(t.Context(), parcel.ID); err != nil || !found || got != parcel {
		t.Errorf("the other product is %+v (found %t, %v), want it untouched", got, found, err)
	}
}
