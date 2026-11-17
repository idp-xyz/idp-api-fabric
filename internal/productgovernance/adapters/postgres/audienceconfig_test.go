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

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// entry is where the audience enters the documentation of the environment.
func entry(audience domain.Audience, environment domain.Environment) string {
	return "https://developers.cargopai.example/" + string(audience) + "/" + string(environment)
}

// configuredAt makes the product document its APIs for the audience in the
// environment, at its entry, as alice configured it at the instant.
func configuredAt(t *testing.T, of domain.Product, audience domain.Audience, environment domain.Environment, at time.Time) domain.AudienceConfig {
	t.Helper()

	config, err := domain.ConfigureAudience(domain.AudienceConfigID(ids.NewUUID()), of, audience, environment, entry(audience, environment), alice, at)
	if err != nil {
		t.Fatalf("build the audience configuration: %v", err)
	}
	return config
}

func configured(t *testing.T, of domain.Product, audience domain.Audience, environment domain.Environment) domain.AudienceConfig {
	t.Helper()

	return configuredAt(t, of, audience, environment, created)
}

// reentered is the configuration as it is once by has changed its entry to
// docsURL, an hour after its last change.
func reentered(t *testing.T, of domain.Product, config domain.AudienceConfig, docsURL string, by domain.Principal) domain.AudienceConfig {
	t.Helper()

	changed, err := config.ChangeDocsURL(of, docsURL, by, config.UpdatedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("change the entry: %v", err)
	}
	return changed
}

// withdrawn is the configuration as it is once by has removed it, an hour
// after its last change.
func withdrawn(t *testing.T, config domain.AudienceConfig, by domain.Principal) domain.AudienceConfig {
	t.Helper()

	removed, err := config.Remove(by, config.UpdatedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("remove the configuration: %v", err)
	}
	return removed
}

func (f fixture) configure(t *testing.T, configs ...domain.AudienceConfig) {
	t.Helper()

	f.within(t, func(ctx context.Context) error {
		for _, config := range configs {
			if err := f.configs.Add(ctx, config); err != nil {
				return err
			}
		}
		return nil
	})
}

func (f fixture) change(t *testing.T, changed domain.AudienceConfig) {
	t.Helper()

	f.within(t, func(ctx context.Context) error { return f.configs.Change(ctx, changed) })
}

func (f fixture) remove(t *testing.T, removed domain.AudienceConfig) {
	t.Helper()

	f.within(t, func(ctx context.Context) error { return f.configs.Remove(ctx, removed) })
}

// configRecord is what the table keeps of one configuration, in force or
// not.
type configRecord struct {
	Version   int64
	HasEntry  bool
	Entry     string
	CreatedBy string
	UpdatedAt time.Time
	UpdatedBy string
	Removed   bool
	RemovedAt time.Time
	RemovedBy string
}

func (f fixture) configRecord(t *testing.T, id domain.AudienceConfigID) configRecord {
	t.Helper()

	var kept configRecord
	if err := f.app.QueryRow(t.Context(), `
		SELECT audience_config_version, docs_url IS NOT NULL, coalesce(docs_url, ''), created_by_subject,
		       updated_at, updated_by_subject, removed_at IS NOT NULL,
		       coalesce(removed_at, 'epoch'), coalesce(removed_by_subject, '')
		FROM productgovernance.product_audience_config WHERE audience_config_id = $1`, string(id),
	).Scan(&kept.Version, &kept.HasEntry, &kept.Entry, &kept.CreatedBy, &kept.UpdatedAt, &kept.UpdatedBy,
		&kept.Removed, &kept.RemovedAt, &kept.RemovedBy); err != nil {
		t.Fatalf("read the record of the audience configuration: %v", err)
	}
	return kept
}

func (f fixture) listConfigs(t *testing.T, productID domain.ProductID) []domain.AudienceConfig {
	t.Helper()

	configs, err := f.configs.List(t.Context(), productID, ports.Page{Limit: 100})
	if err != nil {
		t.Fatalf("list the audience configurations: %v", err)
	}
	return configs
}

// What is stored is what is read back: the same configuration, timed in UTC,
// to the microsecond. An entry nobody gave is no entry, not an empty one.
func TestAnAudienceConfigurationIsReadBackAsItWasStored(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)
	partners := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox)
	public, err := domain.ConfigureAudience(domain.AudienceConfigID(ids.NewUUID()), cargopai,
		domain.AudiencePublic, domain.EnvironmentProduction, "", bob, created)
	if err != nil {
		t.Fatalf("build the audience configuration: %v", err)
	}

	f.within(t, func(ctx context.Context) error {
		if err := f.configs.Add(ctx, partners); err != nil {
			return err
		}
		if err := f.configs.Add(ctx, public); err != nil {
			return err
		}
		// Inside a transaction a read sees what that transaction wrote.
		if got, found, err := f.configs.Find(ctx, cargopai.ID, partners.ID); err != nil || !found || got != partners {
			t.Errorf("inside its transaction the configuration is %+v (found %t, %v)", got, found, err)
		}
		return nil
	})

	for _, want := range []domain.AudienceConfig{partners, public} {
		got, found, err := f.configs.Find(t.Context(), cargopai.ID, want.ID)
		if err != nil || !found || got != want {
			t.Errorf("configuration\n %+v (found %t, %v)\nwant\n %+v", got, found, err, want)
		}
		if got.CreatedAt.Location() != time.UTC || got.CreatedAt.Nanosecond() != 123456000 || got.UpdatedAt.Location() != time.UTC {
			t.Errorf("the configuration is timed %s and %s, want UTC to the microsecond", got.CreatedAt, got.UpdatedAt)
		}
	}
	// The record says who configured it, and keeps no entry as none.
	if kept := f.configRecord(t, partners.ID); kept.Version != 1 || kept.CreatedBy != alice.Subject || kept.UpdatedBy != alice.Subject ||
		!kept.HasEntry || kept.Entry != partners.DocsURL || kept.Removed {
		t.Errorf("the record is %+v, want version 1, configured by alice at its entry, in force", kept)
	}
	if kept := f.configRecord(t, public.ID); kept.HasEntry || kept.CreatedBy != bob.Subject {
		t.Errorf("the record is %+v, want no entry at all, configured by bob", kept)
	}
}

// A configuration is of one product. It is found nowhere else, by a read or
// by a lock.
func TestAnAudienceConfigurationIsFoundOnlyUnderItsProduct(t *testing.T) {
	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, cargopai)
	f.add(t, parcel)
	partners := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox)
	f.configure(t, partners)

	missing := map[string]struct {
		productID domain.ProductID
		id        domain.AudienceConfigID
	}{
		"a configuration nobody stored": {cargopai.ID, domain.AudienceConfigID(ids.NewUUID())},
		"under another product":         {parcel.ID, partners.ID},
		"under a product nobody stored": {domain.ProductID(ids.NewUUID()), partners.ID},
	}
	for name, asked := range missing {
		if got, found, err := f.configs.Find(t.Context(), asked.productID, asked.id); err != nil || found {
			t.Errorf("%s: found %t (%+v), %v", name, found, got, err)
		}
		f.within(t, func(ctx context.Context) error {
			got, found, err := f.configs.Lock(ctx, asked.productID, asked.id)
			if err == nil && found {
				t.Errorf("%s: locked %+v", name, got)
			}
			return err
		})
	}
	if configs := f.listConfigs(t, parcel.ID); len(configs) != 0 {
		t.Errorf("the other product lists %+v, want nothing", configs)
	}
}

// DC-003: a product documents its APIs for an audience in an environment at
// most once at a time, and what one product configures leaves every other
// as it was. A second configuration of the pair is refused, and the refusal
// does not fail the transaction.
func TestAProductDocumentsForAnAudienceInAnEnvironmentOnceAtATime(t *testing.T) {
	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, cargopai)
	f.add(t, parcel)
	first := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox)
	f.configure(t, first)

	elsewhere := configured(t, parcel, domain.AudiencePartner, domain.EnvironmentSandbox)
	production := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentProduction)
	public := configured(t, cargopai, domain.AudiencePublic, domain.EnvironmentSandbox)
	f.within(t, func(ctx context.Context) error {
		if err := f.configs.Add(ctx, configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox)); !errors.Is(err, ports.ErrAudienceConfigExists) {
			t.Errorf("a second configuration of the pair: got %v, want %v", err, ports.ErrAudienceConfigExists)
		}
		// The same transaction still writes: the pair in another product,
		// the audience in another environment, another audience.
		for _, other := range []domain.AudienceConfig{elsewhere, production, public} {
			if err := f.configs.Add(ctx, other); err != nil {
				return err
			}
		}
		return nil
	})

	for name, kept := range map[string]domain.AudienceConfig{
		"the first configuration": first, "the pair in another product": elsewhere,
		"the audience in another environment": production, "another audience": public,
	} {
		if got, found, err := f.configs.Find(t.Context(), kept.ProductID, kept.ID); err != nil || !found || got != kept {
			t.Errorf("%s is %+v (found %t, %v), want it as it was stored", name, got, found, err)
		}
	}
	if cargopais, parcels := f.listConfigs(t, cargopai.ID), f.listConfigs(t, parcel.ID); len(cargopais) != 3 || len(parcels) != 1 {
		t.Errorf("CargoPai has %d configurations and Parcel %d, want 3 and 1", len(cargopais), len(parcels))
	}
}

func TestTwoConfigurationsOfOnePairAtOnceLeaveOne(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)

	const racers = 6
	results := make(chan error, racers)
	for range racers {
		candidate := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox)
		go func() {
			results <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				return f.configs.Add(ctx, candidate)
			})
		}()
	}
	won := 0
	for range racers {
		switch err := <-results; {
		case err == nil:
			won++
		case !errors.Is(err, ports.ErrAudienceConfigExists):
			t.Errorf("a configuration failed with %v, want it to win or to find the pair configured", err)
		}
	}
	if configs := f.listConfigs(t, cargopai.ID); won != 1 || len(configs) != 1 {
		t.Errorf("%d configurations of one pair succeeded and CargoPai has %d, want 1 and 1", won, len(configs))
	}
}

// A change of the entry is stored over the version it was made from. It says
// who changed it and when, and keeps who configured it. A change made from a
// version the configuration is not at any more finds nothing to change, and
// does not fail its transaction.
func TestAChangeOfTheEntryIsStoredOverTheVersionItWasMadeFrom(t *testing.T) {
	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, cargopai)
	f.add(t, parcel)
	partners := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox)
	f.configure(t, partners)

	changed := reentered(t, cargopai, partners, "https://partners.cargopai.example/docs", bob)
	f.within(t, func(ctx context.Context) error {
		if err := f.configs.Change(ctx, changed); err != nil {
			return err
		}
		// Inside the transaction that changed it, it is changed already.
		if got, found, err := f.configs.Find(ctx, cargopai.ID, partners.ID); err != nil || !found || got != changed {
			t.Errorf("inside the transaction that changed it the configuration is %+v (found %t, %v)", got, found, err)
		}
		return nil
	})
	if got, found, err := f.configs.Find(t.Context(), cargopai.ID, partners.ID); err != nil || !found || got != changed {
		t.Errorf("configuration\n %+v (found %t, %v)\nwant\n %+v", got, found, err, changed)
	}
	if kept := f.configRecord(t, partners.ID); kept.Version != 2 || kept.Entry != changed.DocsURL || kept.CreatedBy != alice.Subject ||
		kept.UpdatedBy != bob.Subject || !kept.UpdatedAt.Equal(changed.UpdatedAt) {
		t.Errorf("the record is %+v, want version 2 at the new entry, configured by alice and changed by bob at %s", kept, changed.UpdatedAt)
	}

	// From the version it is not at any more, and under another product,
	// there is nothing to change.
	stale := reentered(t, cargopai, partners, "https://stale.example/", mallory)
	abroad := reentered(t, cargopai, changed, "https://abroad.example/", mallory)
	abroad.ProductID = parcel.ID
	later := configured(t, cargopai, domain.AudiencePublic, domain.EnvironmentSandbox)
	f.within(t, func(ctx context.Context) error {
		for name, attempt := range map[string]domain.AudienceConfig{
			"a change made from the version before": stale,
			"a change under another product":        abroad,
			"a change of a configuration nobody stored": reentered(t, cargopai,
				configured(t, cargopai, domain.AudienceInternal, domain.EnvironmentSandbox), "https://nobody.example/", mallory),
		} {
			if err := f.configs.Change(ctx, attempt); !errors.Is(err, ports.ErrAudienceConfigGone) {
				t.Errorf("%s: got %v, want %v", name, err, ports.ErrAudienceConfigGone)
			}
		}
		// The same transaction still writes.
		return f.configs.Add(ctx, later)
	})
	if kept := f.configRecord(t, partners.ID); kept.Version != 2 || kept.UpdatedBy != bob.Subject {
		t.Errorf("the record is %+v, want it as the change of bob left it", kept)
	}

	// Taking the entry away leaves the column without one.
	cleared := reentered(t, cargopai, changed, "", alice)
	f.change(t, cleared)
	if got, found, err := f.configs.Find(t.Context(), cargopai.ID, partners.ID); err != nil || !found || got != cleared || got.DocsURL != "" {
		t.Errorf("without its entry the configuration is %+v (found %t, %v)", got, found, err)
	}
	if kept := f.configRecord(t, partners.ID); kept.HasEntry || kept.Version != 3 {
		t.Errorf("the record is %+v, want no entry at version 3", kept)
	}
}

func TestTwoChangesFromOneVersionAtOnceLeaveOne(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)
	partners := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox)
	f.configure(t, partners)

	const racers = 6
	results := make(chan error, racers)
	for index := range racers {
		changed := reentered(t, cargopai, partners, fmt.Sprintf("https://racer-%d.example/", index),
			domain.Principal{Issuer: alice.Issuer, Subject: fmt.Sprintf("owner-%d", index)})
		go func() {
			results <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				return f.configs.Change(ctx, changed)
			})
		}()
	}
	won := 0
	for range racers {
		switch err := <-results; {
		case err == nil:
			won++
		case !errors.Is(err, ports.ErrAudienceConfigGone):
			t.Errorf("a change failed with %v, want it to win or to find the configuration changed", err)
		}
	}
	kept := f.configRecord(t, partners.ID)
	winner := strings.TrimPrefix(kept.UpdatedBy, "owner-")
	if won != 1 || kept.Version != 2 || kept.Entry != "https://racer-"+winner+".example/" {
		t.Errorf("%d changes succeeded and the record is %+v, want 1 and the entry of the one that won", won, kept)
	}
}

// A removed configuration is in no read, no lock and no list. Its row is
// kept, with who removed it and when (F03 §13), and the pair can be
// configured again as a new configuration.
func TestARemovedAudienceConfigurationIsInNoReadAndIsKept(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)
	first, other := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox), configured(t, cargopai, domain.AudiencePublic, domain.EnvironmentSandbox)
	f.configure(t, first, other)
	changed := reentered(t, cargopai, first, "https://partners.cargopai.example/docs", bob)
	f.change(t, changed)

	removed := withdrawn(t, changed, bob)
	f.within(t, func(ctx context.Context) error {
		if err := f.configs.Remove(ctx, removed); err != nil {
			return err
		}
		// Inside the transaction that removed it, it is gone already.
		if _, found, err := f.configs.Find(ctx, cargopai.ID, first.ID); err != nil || found {
			t.Errorf("inside the transaction that removed it: found %t, %v", found, err)
		}
		return nil
	})

	if _, found, err := f.configs.Find(t.Context(), cargopai.ID, first.ID); err != nil || found {
		t.Errorf("the removed configuration: found %t, %v", found, err)
	}
	f.within(t, func(ctx context.Context) error {
		_, found, err := f.configs.Lock(ctx, cargopai.ID, first.ID)
		if err == nil && found {
			t.Error("the removed configuration was locked")
		}
		return err
	})
	if configs := f.listConfigs(t, cargopai.ID); !slices.Equal(configs, []domain.AudienceConfig{other}) {
		t.Errorf("the product lists %+v, want only what was not removed", configs)
	}

	// The record keeps what the configuration was, who changed it last,
	// and who removed it.
	kept := f.configRecord(t, first.ID)
	if kept.Version != 3 || kept.Entry != changed.DocsURL || kept.UpdatedBy != bob.Subject || !kept.Removed ||
		kept.RemovedBy != bob.Subject || !kept.RemovedAt.Equal(removed.RemovedAt) {
		t.Errorf("the record is %+v, want version 3, at the entry bob gave it, removed by bob at %s", kept, removed.RemovedAt)
	}

	// The pair is configured again as a new configuration. The record of
	// the first stays.
	again := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox)
	f.configure(t, again)
	if got, found, err := f.configs.Find(t.Context(), cargopai.ID, again.ID); err != nil || !found || got != again {
		t.Errorf("the new configuration is %+v (found %t, %v)", got, found, err)
	}
	if kept := f.configRecord(t, first.ID); !kept.Removed || kept.Version != 3 {
		t.Errorf("the record of the first configuration is now %+v, want it as the removal left it", kept)
	}
}

// A configuration is removed once. A removal that finds nothing in force at
// the version it was made from changes nothing and does not fail its
// transaction.
func TestAnAudienceConfigurationIsRemovedOnce(t *testing.T) {
	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, cargopai)
	f.add(t, parcel)
	first, inForce := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox), configured(t, cargopai, domain.AudiencePublic, domain.EnvironmentSandbox)
	f.configure(t, first, inForce)
	f.remove(t, withdrawn(t, first, bob))
	f.change(t, reentered(t, cargopai, inForce, "https://public.cargopai.example/", bob))

	elsewhere := withdrawn(t, reentered(t, cargopai, inForce, "https://public.cargopai.example/", bob), admin)
	elsewhere.ProductID = parcel.ID
	later := configured(t, cargopai, domain.AudienceInternal, domain.EnvironmentSandbox)
	f.within(t, func(ctx context.Context) error {
		for name, removed := range map[string]domain.AudienceConfig{
			"a second removal":                       withdrawn(t, first, admin),
			"a removal made from the version before": withdrawn(t, inForce, admin),
			"a configuration nobody stored":          withdrawn(t, configured(t, cargopai, domain.AudienceInternal, domain.EnvironmentProduction), admin),
			"a configuration under another product":  elsewhere,
		} {
			if err := f.configs.Remove(ctx, removed); !errors.Is(err, ports.ErrAudienceConfigGone) {
				t.Errorf("%s: got %v, want %v", name, err, ports.ErrAudienceConfigGone)
			}
		}
		// The same transaction still writes.
		return f.configs.Add(ctx, later)
	})

	// Who removed it first is who removed it.
	if kept := f.configRecord(t, first.ID); kept.Version != 2 || kept.RemovedBy != bob.Subject {
		t.Errorf("the record is %+v, want it as the first removal left it", kept)
	}
	if kept := f.configRecord(t, inForce.ID); kept.Removed || kept.Version != 2 {
		t.Errorf("the record of the configuration in force is %+v, want it in force at version 2", kept)
	}
	if _, found, err := f.configs.Find(t.Context(), cargopai.ID, later.ID); err != nil || !found {
		t.Errorf("what the transaction wrote after the refusals: found %t, %v", found, err)
	}
}

// A change or a removal decides from the configuration as it is locked. Who
// locks it second waits for the first, and then reads what the first left:
// the configuration at its next version, or none once it is removed. A read
// waits for nobody.
func TestWhoeverLocksAConfigurationSecondWaitsAndReadsWhatTheFirstLeft(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)

	type read struct {
		config domain.AudienceConfig
		found  bool
		err    error
	}
	// The first round changes the configuration, the second removes one.
	for round, environment := range []domain.Environment{domain.EnvironmentSandbox, domain.EnvironmentProduction} {
		removing := round == 1
		partners := configured(t, cargopai, domain.AudiencePartner, environment)
		f.configure(t, partners)
		changed := reentered(t, cargopai, partners, "https://partners.cargopai.example/docs", bob)
		removed := withdrawn(t, partners, bob)

		// The first transaction locks the configuration, says what it read
		// and holds it until it is told to go on. Then it changes or
		// removes it.
		locked, proceed, finished := make(chan read, 1), make(chan struct{}), make(chan error, 1)
		var once sync.Once
		release := func() { once.Do(func() { close(proceed) }) }
		defer release()
		go func() {
			finished <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				config, found, err := f.configs.Lock(ctx, cargopai.ID, partners.ID)
				locked <- read{config, found, err}
				if err != nil {
					return err
				}
				<-proceed
				if removing {
					return f.configs.Remove(ctx, removed)
				}
				return f.configs.Change(ctx, changed)
			})
		}()
		if held := <-locked; held.err != nil || !held.found || held.config != partners {
			t.Fatalf("the first transaction locked %+v (found %t, %v)", held.config, held.found, held.err)
		}

		waited := make(chan read, 1)
		go func() {
			var second read
			second.err = f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) (err error) {
				second.config, second.found, err = f.configs.Lock(ctx, cargopai.ID, partners.ID)
				return err
			})
			waited <- second
		}()
		f.waiting(t, 1)
		select {
		case early := <-waited:
			t.Fatalf("the second transaction read %+v while the first held the configuration, want it to wait", early)
		default:
		}
		// A read is not a lock, and waits for nobody.
		if got, found, err := f.configs.Find(t.Context(), cargopai.ID, partners.ID); err != nil || !found || got != partners {
			t.Errorf("while it is locked the configuration reads %+v (found %t, %v), want it as it was", got, found, err)
		}

		release()
		if err := <-finished; err != nil {
			t.Fatalf("the first transaction: %v", err)
		}
		after := <-waited
		switch {
		case after.err != nil:
			t.Errorf("the second transaction: %v", after.err)
		case removing && after.found:
			t.Errorf("once the first had removed the configuration the second locked %+v, want nothing", after.config)
		case !removing && (!after.found || after.config != changed):
			t.Errorf("once the first had changed the configuration the second locked %+v (found %t), want\n %+v", after.config, after.found, changed)
		}
	}
}

func TestChangingAnAudienceConfigurationNeedsATransaction(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)
	partners := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox)
	f.configure(t, partners)

	_, _, errLock := f.configs.Lock(t.Context(), cargopai.ID, partners.ID)
	for what, err := range map[string]error{
		"an addition": f.configs.Add(t.Context(), configured(t, cargopai, domain.AudiencePublic, domain.EnvironmentSandbox)),
		"a change":    f.configs.Change(t.Context(), reentered(t, cargopai, partners, "https://elsewhere.example/", bob)),
		"a removal":   f.configs.Remove(t.Context(), withdrawn(t, partners, bob)),
		// A lock no transaction holds would be given back at once: what was
		// decided from it could change before it is stored.
		"a lock": errLock,
	} {
		if !errors.Is(err, bentopostgres.ErrTransactionRequired) {
			t.Errorf("%s outside a transaction: got %v, want %v", what, err, bentopostgres.ErrTransactionRequired)
		}
	}
	if configs := f.listConfigs(t, cargopai.ID); !slices.Equal(configs, []domain.AudienceConfig{partners}) {
		t.Errorf("the product lists %+v, want the configuration as it was", configs)
	}
}

// A configuration is added in force, changed in force and removed. The store
// refuses one in another state before it sends anything.
func TestTheStoreTakesAnAudienceConfigurationInTheStateItsStatementIsFor(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)
	inForce := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox)
	f.configure(t, inForce)
	never := configured(t, cargopai, domain.AudiencePublic, domain.EnvironmentSandbox)

	f.within(t, func(ctx context.Context) error {
		for name, err := range map[string]error{
			"a configuration added removed":           f.configs.Add(ctx, withdrawn(t, never, bob)),
			"a change of a removed configuration":     f.configs.Change(ctx, withdrawn(t, inForce, bob)),
			"a change at the version it was added at": f.configs.Change(ctx, inForce),
			"a removal that says nothing of when":     f.configs.Remove(ctx, inForce),
		} {
			if err == nil || errors.Is(err, ports.ErrAudienceConfigExists) || errors.Is(err, ports.ErrAudienceConfigGone) {
				t.Errorf("%s: got %v, want it refused for what it is", name, err)
			}
		}
		return nil
	})
	if configs := f.listConfigs(t, cargopai.ID); !slices.Equal(configs, []domain.AudienceConfig{inForce}) {
		t.Errorf("the product lists %+v, want the configuration in force as it was", configs)
	}
	if kept := f.configRecord(t, inForce.ID); kept.Removed || kept.Version != 1 {
		t.Errorf("the record is %+v, want it in force at version 1", kept)
	}
}

// The configurations of a product are listed oldest first: those in force of
// that product, and no other. A change of the entry leaves one where it is;
// one that was removed is in no list, and the pair configured again is
// listed as the new configuration it is.
func TestTheAudienceConfigurationsInForceOfAProductAreListed(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)

	want := []domain.AudienceConfig{
		configuredAt(t, s.cargopai, domain.AudiencePublic, domain.EnvironmentSandbox, later(1)),
		configuredAt(t, s.cargopai, domain.AudiencePublic, domain.EnvironmentProduction, later(2)),
		configuredAt(t, s.cargopai, domain.AudiencePartner, domain.EnvironmentSandbox, later(2)),
		configuredAt(t, s.cargopai, domain.AudiencePartner, domain.EnvironmentProduction, later(2)),
		configuredAt(t, s.cargopai, domain.AudienceInternal, domain.EnvironmentSandbox, later(3)),
	}
	slices.SortFunc(want[1:4], func(a, b domain.AudienceConfig) int { return strings.Compare(string(a.ID), string(b.ID)) })
	former := configuredAt(t, s.cargopai, domain.AudienceInternal, domain.EnvironmentProduction, later(2))
	abroad := configuredAt(t, s.parcel, domain.AudiencePartner, domain.EnvironmentSandbox, later(2))
	f.configure(t, want[3], former, want[0], abroad, want[4], want[2], want[1])
	f.remove(t, withdrawn(t, former, alice))
	again := configuredAt(t, s.cargopai, domain.AudienceInternal, domain.EnvironmentProduction, later(5))
	f.configure(t, again)
	want = append(want, again)
	want[0] = reentered(t, s.cargopai, want[0], "https://public.cargopai.example/", bob)
	f.change(t, want[0])

	list := func(of domain.Product) func(ports.Page) ([]domain.AudienceConfig, error) {
		return func(page ports.Page) ([]domain.AudienceConfig, error) {
			return f.configs.List(t.Context(), of.ID, page)
		}
	}
	inOrder(t, "the configurations of the product", want, ports.PositionOfAudienceConfig, list(s.cargopai))
	inOrder(t, "the configurations of the other product", []domain.AudienceConfig{abroad}, ports.PositionOfAudienceConfig, list(s.parcel))
	inOrder(t, "the configurations of a product that is missing", nil, ports.PositionOfAudienceConfig, list(product(t, "missing")))
}

// A page takes at least one row, and inside a transaction a list holds what
// that transaction wrote.
func TestAListOfAudienceConfigurationsTakesAPageOfAtLeastOneRow(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)

	for _, limit := range []int{0, -1} {
		if _, err := f.configs.List(t.Context(), cargopai.ID, ports.Page{Limit: limit}); err == nil || !strings.Contains(err.Error(), "at least one row") {
			t.Errorf("a page of %d: got %v, want it refused", limit, err)
		}
	}
	partners := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox)
	f.within(t, func(ctx context.Context) error {
		if err := f.configs.Add(ctx, partners); err != nil {
			return err
		}
		configs, err := f.configs.List(ctx, cargopai.ID, ports.Page{Limit: 10})
		if err == nil && !slices.Equal(configs, []domain.AudienceConfig{partners}) {
			t.Errorf("inside its transaction the list holds %+v", configs)
		}
		return err
	})
}

// The schema refuses what the model never produces, whoever writes it. The
// application role holds UPDATE on the table, to change the entry of a
// configuration and to remove one, and still changes nothing else of one.
func TestTheSchemaHoldsTheLineOfAnAudienceConfiguration(t *testing.T) {
	const (
		foreignKeyViolation = "23503"
		uniqueViolation     = "23505"
		checkViolation      = "23514"
		// What the trigger of the table raises.
		integrityViolation    = "23000"
		insufficientPrivilege = "42501"
	)

	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, cargopai)
	f.add(t, parcel)
	held, gone := configured(t, cargopai, domain.AudiencePartner, domain.EnvironmentSandbox), configured(t, cargopai, domain.AudiencePublic, domain.EnvironmentProduction)
	f.configure(t, held, gone)
	f.remove(t, withdrawn(t, gone, alice))

	// The store, handed what the model never produces.
	orphan := configured(t, cargopai, domain.AudienceInternal, domain.EnvironmentSandbox)
	orphan.ProductID = domain.ProductID(ids.NewUUID())
	err := f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error { return f.configs.Add(ctx, orphan) })
	if sqlState(err) != foreignKeyViolation {
		t.Errorf("a configuration of a product nobody stored: got %v, want a foreign key violation", err)
	}
	twin := configured(t, parcel, domain.AudiencePartner, domain.EnvironmentSandbox)
	twin.ID = held.ID
	err = f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error { return f.configs.Add(ctx, twin) })
	if sqlState(err) != uniqueViolation {
		t.Errorf("a configuration under the identifier of another: got %v, want a unique violation", err)
	}
	for name, config := range map[string]domain.AudienceConfig{
		"an audience nobody defined":    {Audience: "external", Environment: domain.EnvironmentSandbox},
		"an environment nobody defined": {Audience: domain.AudienceInternal, Environment: "staging"},
		"an entry that is not https":    {Audience: domain.AudienceInternal, Environment: domain.EnvironmentSandbox, DocsURL: "http://developers.cargopai.example/"},
	} {
		written := configured(t, cargopai, domain.AudienceInternal, domain.EnvironmentProduction)
		written.Audience, written.Environment, written.DocsURL = config.Audience, config.Environment, config.DocsURL
		err := f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error { return f.configs.Add(ctx, written) })
		if sqlState(err) != checkViolation {
			t.Errorf("%s: got %v, want a check violation", name, err)
		}
	}

	// The application role, writing by hand.
	const (
		update  = `UPDATE productgovernance.product_audience_config SET `
		counted = `, audience_config_version = audience_config_version + 1 WHERE audience_config_id = $1`
		removal = `removed_at = updated_at + interval '1 hour', removed_by_issuer = 'https://issuer.example', removed_by_subject = 'mallory'`
		entered = `docs_url = 'https://elsewhere.example/', updated_at = updated_at + interval '1 hour', updated_by_subject = 'mallory'`
		// A copy of a row, with what the case says instead.
		copied = `
			INSERT INTO productgovernance.product_audience_config
			    (audience_config_id, product_id, audience, environment, docs_url, audience_config_version,
			     created_at, created_by_issuer, created_by_subject, updated_at, updated_by_issuer, updated_by_subject)
			SELECT gen_random_uuid(), %s, %s, %s, docs_url, %s,
			       created_at, created_by_issuer, created_by_subject, %s, updated_by_issuer, updated_by_subject
			FROM productgovernance.product_audience_config WHERE audience_config_id = $1`
	)

	// A new entry, counted, and a removal, counted, are what the table takes
	// of a configuration in force. The cases below that say what a change
	// does besides are built on one of the two, so that each is refused for
	// what it adds and for nothing else. A statement that only moved a
	// configuration to another product would prove nothing: it changes no
	// entry, and the table refuses it for that already.
	for name, taken := range map[string]string{"a new entry": update + entered + counted, "a removal": update + removal + counted} {
		attempt, err := f.app.Begin(t.Context())
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := attempt.Exec(t.Context(), taken, string(held.ID)); err != nil {
			t.Errorf("%s, counted: got %v, want the table to take it", name, err)
		}
		if err := attempt.Rollback(t.Context()); err != nil {
			t.Fatalf("roll back: %v", err)
		}
	}

	statements := []struct {
		name      string
		statement string
		id        domain.AudienceConfigID
		state     string
	}{
		{"move it to another product", update + entered + `, product_id = '` + string(parcel.ID) + `'` + counted, held.ID, integrityViolation},
		{"open it to another audience", update + entered + `, audience = 'public'` + counted, held.ID, integrityViolation},
		{"move it to another environment", update + entered + `, environment = 'production'` + counted, held.ID, integrityViolation},
		{"change its identifier", update + entered + `, audience_config_id = gen_random_uuid()` + counted, held.ID, integrityViolation},
		{"change under which issuer it was configured", update + entered + `, created_by_issuer = 'https://elsewhere.example'` + counted, held.ID, integrityViolation},
		{"change who configured it", update + entered + `, created_by_subject = 'mallory'` + counted, held.ID, integrityViolation},
		{"change when it was configured", update + entered + `, created_at = created_at - interval '1 year'` + counted, held.ID, integrityViolation},
		{"change its entry without counting the change", update + entered + ` WHERE audience_config_id = $1`, held.ID, integrityViolation},
		{"count a change that did not happen", update + `updated_at = updated_at + interval '1 hour'` + counted, held.ID, integrityViolation},
		{"skip a version", update + entered + `, audience_config_version = audience_config_version + 2 WHERE audience_config_id = $1`, held.ID, integrityViolation},
		{"change its entry before the last change", update + `docs_url = 'https://elsewhere.example/', updated_at = updated_at - interval '1 second'` + counted,
			held.ID, integrityViolation},
		{"change its entry to plain http", update + `docs_url = 'http://elsewhere.example/', updated_at = updated_at + interval '1 hour'` + counted, held.ID, checkViolation},
		{"change its entry to an empty one", update + `docs_url = ''` + counted, held.ID, checkViolation},
		{"change its entry to one too long", update + `docs_url = 'https://elsewhere.example/' || repeat('a', 2048)` + counted, held.ID, checkViolation},
		{"remove it without counting the change", update + removal + ` WHERE audience_config_id = $1`, held.ID, integrityViolation},
		{"remove it without saying who did", update + `removed_at = updated_at + interval '1 hour'` + counted, held.ID, checkViolation},
		{"remove it before its last change", update + `removed_at = updated_at - interval '1 second', removed_by_issuer = 'i', removed_by_subject = 's'` + counted,
			held.ID, checkViolation},
		{"remove it and change its entry", update + removal + `, docs_url = 'https://elsewhere.example/'` + counted, held.ID, integrityViolation},
		{"remove it and change who changed it last", update + removal + `, updated_by_subject = 'mallory'` + counted, held.ID, integrityViolation},
		// A removed one changes in no way at all. Each of the first three
		// would be a change the table takes if a removed row were held to no
		// more than a row in force is. The one that brings it back is the
		// last of them: the two before it are tried on a row that is still
		// removed.
		{"count a change of a removed one that did not happen", update + `audience_config_version = audience_config_version + 1 WHERE audience_config_id = $1`,
			gone.ID, integrityViolation},
		{"remove a removed one again", update + `removed_at = removed_at + interval '1 hour', removed_by_subject = 'mallory'` + counted, gone.ID, integrityViolation},
		{"bring a removed one back", update + entered + `, removed_at = NULL, removed_by_issuer = NULL, removed_by_subject = NULL` + counted, gone.ID, integrityViolation},
		{"change the entry of a removed one", update + entered + counted, gone.ID, integrityViolation},
		{"configure the pair twice at a time", fmt.Sprintf(copied, `product_id`, `audience`, `environment`, `1`, `updated_at`), held.ID, uniqueViolation},
		{"configure it for a product nobody stored", fmt.Sprintf(copied, `gen_random_uuid()`, `'internal'`, `environment`, `1`, `updated_at`), held.ID, foreignKeyViolation},
		{"begin at a version that is none", fmt.Sprintf(copied, `product_id`, `'internal'`, `environment`, `0`, `updated_at`), held.ID, checkViolation},
		{"begin changed before it was configured", fmt.Sprintf(copied, `product_id`, `'internal'`, `environment`, `1`, `created_at - interval '1 second'`), held.ID,
			checkViolation},
		{"remove it for good", `DELETE FROM productgovernance.product_audience_config WHERE audience_config_id = $1`, held.ID, insufficientPrivilege},
		{"remove the record of a removed one", `DELETE FROM productgovernance.product_audience_config WHERE audience_config_id = $1`, gone.ID, insufficientPrivilege},
	}
	for _, c := range statements {
		t.Run(c.name, func(t *testing.T) {
			if _, err := f.app.Exec(t.Context(), c.statement, string(c.id)); sqlState(err) != c.state {
				t.Errorf("%s\n got %v, want it refused with SQLSTATE %s", c.statement, err, c.state)
			}
		})
	}
	for _, statement := range []string{
		`TRUNCATE productgovernance.product_audience_config`,
		`ALTER TABLE productgovernance.product_audience_config DISABLE TRIGGER product_audience_config_only_changes_entry_or_ends`,
		`DROP TRIGGER product_audience_config_only_changes_entry_or_ends ON productgovernance.product_audience_config`,
		`DROP INDEX productgovernance.product_audience_config_in_force_unique`,
		`CREATE OR REPLACE FUNCTION productgovernance.product_audience_config_only_changes_entry_or_ends() RETURNS trigger
		     LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'`,
	} {
		if _, err := f.app.Exec(t.Context(), statement); sqlState(err) != insufficientPrivilege {
			t.Errorf("%s\n got %v, want it refused for lack of privilege", statement, err)
		}
	}

	// Nothing of it took effect.
	if configs := f.listConfigs(t, cargopai.ID); !slices.Equal(configs, []domain.AudienceConfig{held}) {
		t.Errorf("the product lists %+v, want the configuration in force untouched", configs)
	}
	if kept := f.configRecord(t, gone.ID); !kept.Removed || kept.Version != 2 || kept.RemovedBy != alice.Subject {
		t.Errorf("the record of the removed configuration is now %+v, want it as the removal left it", kept)
	}
	if configs := f.listConfigs(t, parcel.ID); len(configs) != 0 {
		t.Errorf("the other product lists %+v, want nothing", configs)
	}
}
