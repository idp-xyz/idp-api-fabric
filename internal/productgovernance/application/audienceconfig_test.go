package application

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// partnerPortal and partnerDocs are where the partners of CargoPai enter its
// documentation.
const (
	partnerPortal = "https://developers.cargopai.example/partner"
	partnerDocs   = "https://docs.cargopai.example/partner"
)

// configured makes the product document its APIs for the audience in the
// environment, as one of its owners.
func configured(t *testing.T, service *ProductGovernance, product domain.Product, owner domain.Principal,
	audience domain.Audience, environment domain.Environment, docsURL string) domain.AudienceConfig {
	t.Helper()

	config, err := service.ConfigureAudience(ctx, ConfigureAudience{
		Caller: owner, CorrelationID: "correlation-a", ProductID: string(product.ID),
		Audience: string(audience), Environment: string(environment), DocsURL: docsURL,
	})
	if err != nil {
		t.Fatalf("configure %s in %s for %s: %v", audience, environment, product.Key, err)
	}
	return config
}

// portal is the board with CargoPai documenting its APIs for its partners
// in the sandbox, as alice configured it. Parcel documents its APIs for
// nobody.
type portal struct {
	board

	partners domain.AudienceConfig
}

func newPortal(t *testing.T) portal {
	t.Helper()

	s := portal{board: newBoard(t)}
	s.partners = configured(t, s.service, s.cargopai, alice, domain.AudiencePartner, domain.EnvironmentSandbox, partnerPortal)
	s.w.quiet()
	return s
}

func (s team) configure(caller domain.Principal, product domain.Product, audience, environment, docsURL string) (domain.AudienceConfig, error) {
	return s.service.ConfigureAudience(ctx, ConfigureAudience{
		Caller: caller, CorrelationID: "correlation-15", ProductID: string(product.ID),
		Audience: audience, Environment: environment, DocsURL: docsURL,
	})
}

func (s team) config(caller domain.Principal, product domain.Product, id domain.AudienceConfigID) (domain.AudienceConfig, error) {
	return s.service.GetAudienceConfig(ctx, GetAudienceConfig{
		Caller: caller, CorrelationID: "correlation-16", ProductID: string(product.ID), AudienceConfigID: string(id),
	})
}

func (s team) changeConfig(caller domain.Principal, product domain.Product, id domain.AudienceConfigID, docsURL string, version int64) (domain.AudienceConfig, error) {
	return s.service.ChangeAudienceConfig(ctx, ChangeAudienceConfig{
		Caller: caller, CorrelationID: "correlation-17", ProductID: string(product.ID), AudienceConfigID: string(id),
		DocsURL: docsURL, Version: version,
	})
}

func (s team) removeConfig(caller domain.Principal, product domain.Product, id domain.AudienceConfigID) error {
	return s.service.RemoveAudienceConfig(ctx, RemoveAudienceConfig{
		Caller: caller, CorrelationID: "correlation-18", ProductID: string(product.ID), AudienceConfigID: string(id),
	})
}

func (s team) listConfigs(caller domain.Principal, product domain.Product, size int, after *ports.Position) (Page[domain.AudienceConfig], error) {
	return s.service.ListAudienceConfigs(ctx, ListAudienceConfigs{
		Caller: caller, CorrelationID: "correlation-19", ProductID: string(product.ID), PageSize: size, After: after,
	})
}

// withoutPortal are the callers that do not own CargoPai, with what each is
// told when it asks to configure an audience of it: those of withoutSay,
// and the platform administrator, which registers a product and leaves its
// portal to its owners.
func withoutPortal() map[string]answered {
	callers := maps.Clone(withoutSay)
	callers["the platform administrator"] = answered{admin, ErrForbidden}
	return callers
}

func TestConfigureAudienceStoresTheConfigurationTheAuditFactAndTheEventTogether(t *testing.T) {
	s := newBoard(t)
	s.w.now = started.Add(time.Hour)

	config, err := s.configure(alice, s.cargopai, "partner", "sandbox", partnerPortal)
	if err != nil {
		t.Fatalf("configure: %v", err)
	}

	want := domain.AudienceConfig{
		ID: config.ID, ProductID: s.cargopai.ID, Audience: domain.AudiencePartner, Environment: domain.EnvironmentSandbox,
		DocsURL: partnerPortal, Version: 1, CreatedAt: s.w.now, CreatedBy: alice, UpdatedAt: s.w.now, UpdatedBy: alice,
	}
	if config != want || s.w.configs[config.ID] != want {
		t.Errorf("configuration\n %+v\nstored\n %+v\nwant\n %+v", config, s.w.configs[config.ID], want)
	}
	if _, err := domain.ParseAudienceConfigID(string(config.ID)); err != nil || len(s.w.configs) != 1 {
		t.Errorf("the configuration has the identifier %q and there are %d configurations, want a new one", config.ID, len(s.w.configs))
	}
	// AC-09: the portal of a product is its owner's to configure.
	wantAudit := []audited{{ports.AuditEntry{
		Actor: alice, Action: "audience_config.configure", Allowed: true, Rule: "AC-09",
		ProductID: s.cargopai.ID, ResourceType: "ProductAudienceConfig", ResourceID: string(config.ID), CorrelationID: "correlation-15",
	}, true}}
	if !slices.Equal(s.w.audited, wantAudit) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", s.w.audited, wantAudit)
	}
	if want := []string{"AudienceConfigChanged " + string(config.ID) + " configured at 1 correlation-15"}; !slices.Equal(s.w.events, want) {
		t.Errorf("events %v, want %v", s.w.events, want)
	}
	if s.w.transactions != 1 {
		t.Errorf("%d transactions, want 1", s.w.transactions)
	}

	// Every owner configures the audiences of its product, and an audience
	// is documented for before anybody says where it enters.
	internal, err := s.configure(frank, s.cargopai, "internal", "production", "")
	if err != nil || internal.DocsURL != "" || internal.CreatedBy != frank || s.w.configs[internal.ID] != internal {
		t.Errorf("another owner configures an audience without an entry: got %+v and %v", internal, err)
	}
}

// An owner of the product configures its audiences, and nobody else. The
// answer to anyone else depends on the product alone: it is the same
// whatever the request says.
func TestConfigureAudienceRefusesWhoeverDoesNotOwnTheProduct(t *testing.T) {
	for name, c := range withoutPortal() {
		t.Run(name, func(t *testing.T) {
			s := newPortal(t)
			before := maps.Clone(s.w.configs)

			requests := map[string]ConfigureAudience{
				"an audience":                       {Audience: "public", Environment: "production", DocsURL: "https://developers.cargopai.example"},
				"an audience nobody defined":        {Audience: "everybody", Environment: "production"},
				"an entry that is none":             {Audience: "public", Environment: "production", DocsURL: "http://developers.cargopai.example"},
				"a pair that is configured already": {Audience: "partner", Environment: "sandbox"},
			}
			for what, command := range requests {
				s.w.quiet()
				command.Caller, command.CorrelationID, command.ProductID = c.caller, "correlation-15", string(s.cargopai.ID)

				config, err := s.service.ConfigureAudience(ctx, command)
				if err != c.want || config != (domain.AudienceConfig{}) {
					t.Fatalf("%s: got %+v and %v, want nothing and exactly %v", what, config, err, c.want)
				}
				// The decision is made inside the transaction and recorded
				// once that transaction is over.
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "audience_config.configure", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "ProductAudienceConfig", CorrelationID: "correlation-15",
				}, false}}
				if !slices.Equal(s.w.audited, want) {
					t.Errorf("%s: audited\n %+v\nwant, outside the transaction,\n %+v", what, s.w.audited, want)
				}
				if !maps.Equal(s.w.configs, before) || len(s.w.events) != 0 {
					t.Errorf("%s: a refused configuration left the configurations %+v and the events %v", what, s.w.configs, s.w.events)
				}
			}
		})
	}
}

func TestConfigureAudienceRefusesWhatCannotBeAConfiguration(t *testing.T) {
	s := newPortal(t)
	before := maps.Clone(s.w.configs)

	configure := func(caller domain.Principal, productID, audience, environment, docsURL string) error {
		_, err := s.service.ConfigureAudience(ctx, ConfigureAudience{
			Caller: caller, CorrelationID: "correlation-15", ProductID: productID,
			Audience: audience, Environment: environment, DocsURL: docsURL,
		})
		return err
	}
	cargopai := string(s.cargopai.ID)
	cases := map[string]struct {
		err  error
		want error
	}{
		"an audience nobody defined":    {configure(alice, cargopai, "everybody", "sandbox", ""), domain.ErrInvalid},
		"an audience in upper case":     {configure(alice, cargopai, "Public", "sandbox", ""), domain.ErrInvalid},
		"no audience":                   {configure(alice, cargopai, "", "sandbox", ""), domain.ErrInvalid},
		"an environment nobody defined": {configure(alice, cargopai, "public", "staging", ""), domain.ErrInvalid},
		"no environment":                {configure(alice, cargopai, "public", "", ""), domain.ErrInvalid},
		"an entry that is not https":    {configure(alice, cargopai, "public", "sandbox", "http://developers.cargopai.example"), domain.ErrInvalid},
		"an entry with credentials":     {configure(alice, cargopai, "public", "sandbox", "https://user:secret@developers.cargopai.example"), domain.ErrInvalid},
		"an entry that is too long": {configure(alice, cargopai, "public", "sandbox",
			"https://developers.cargopai.example/"+strings.Repeat("a", domain.DocsURLMaxLength)), domain.ErrInvalid},
		// A product documents its APIs for an audience in an environment
		// once at a time, whoever configured it.
		"a pair that is configured already": {configure(alice, cargopai, "partner", "sandbox", partnerDocs), ports.ErrAudienceConfigExists},
		"the same pair, by another owner":   {configure(frank, cargopai, "partner", "sandbox", ""), ports.ErrAudienceConfigExists},
		"a product that does not exist":     {configure(alice, missingID, "public", "sandbox", ""), ErrProductNotFound},
		"a product that is no identifier":   {configure(alice, "cargopai", "public", "sandbox", ""), ErrProductNotFound},
	}
	for name, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: got %v, want %v", name, c.err, c.want)
		}
	}
	if !maps.Equal(s.w.configs, before) || len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("refused configurations left the configurations %+v, audit %+v, events %v", s.w.configs, s.w.audited, s.w.events)
	}

	// The same audience in another environment, another audience in the
	// same one and the same pair in another product are configurations of
	// their own. Two of them may send their audiences to the same entry.
	accepted := map[string]error{
		"the same audience in production":    configure(alice, cargopai, "partner", "production", partnerDocs),
		"another audience in the sandbox":    configure(alice, cargopai, "public", "sandbox", ""),
		"the same pair in another product":   configure(bob, string(s.parcel.ID), "partner", "sandbox", partnerPortal),
		"an entry another configuration has": configure(alice, cargopai, "internal", "sandbox", partnerPortal),
	}
	for name, err := range accepted {
		if err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
	if len(s.w.configs) != len(before)+len(accepted) {
		t.Errorf("there are %d configurations, want %d more than the %d there were", len(s.w.configs), len(accepted), len(before))
	}
}

// INV-01: a suspended product takes no new configuration and no change of
// one. What it has can still be removed.
func TestASuspendedProductTakesNoAudienceConfigurationAndLetsOneGo(t *testing.T) {
	s := newPortal(t)
	suspended := s.cargopai
	suspended.Status = domain.ProductSuspended
	s.w.products[s.cargopai.ID] = suspended
	before := maps.Clone(s.w.configs)

	if config, err := s.configure(alice, s.cargopai, "public", "production", ""); !errors.Is(err, domain.ErrProductNotActive) || config != (domain.AudienceConfig{}) {
		t.Errorf("a configuration: got %+v and %v, want nothing and %v", config, err, domain.ErrProductNotActive)
	}
	if config, err := s.changeConfig(alice, s.cargopai, s.partners.ID, partnerDocs, 1); !errors.Is(err, domain.ErrProductNotActive) || config != (domain.AudienceConfig{}) {
		t.Errorf("a change: got %+v and %v, want nothing and %v", config, err, domain.ErrProductNotActive)
	}
	if !maps.Equal(s.w.configs, before) || len(s.w.events) != 0 || len(s.w.audited) != 0 {
		t.Errorf("a suspended product got the configurations %+v, events %v, audit %+v", s.w.configs, s.w.events, s.w.audited)
	}

	if err := s.removeConfig(alice, s.cargopai, s.partners.ID); err != nil {
		t.Fatalf("remove a configuration of a suspended product: %v", err)
	}
	if s.w.configs[s.partners.ID].InForce() {
		t.Error("the configuration of a suspended product is still in force")
	}
	// Whoever may read the product still reads what is left: nothing.
	if page, err := s.listConfigs(carol, s.cargopai, 0, nil); err != nil || len(page.Items) != 0 {
		t.Errorf("the configurations of the suspended product: got %+v and %v, want none", page, err)
	}
}

// INV-13: the configuration, its audit fact and its event are one
// transaction. Whatever fails takes all of it down.
func TestConfigureAudienceLeavesNothingWhenAnyPartOfItFails(t *testing.T) {
	down := errors.New("down")
	for _, step := range []string{"audit", "events", "commit", "read", "held", "roles"} {
		t.Run(step+" fails", func(t *testing.T) {
			s := newPortal(t)
			before := maps.Clone(s.w.configs)
			s.w.failing[step] = down

			if config, err := s.configure(alice, s.cargopai, "public", "production", ""); !errors.Is(err, down) || config != (domain.AudienceConfig{}) {
				t.Fatalf("got %+v and %v, want nothing and the failure", config, err)
			}
			if !maps.Equal(s.w.configs, before) || len(s.w.audited)+len(s.w.events) != 0 {
				t.Errorf("a failed configuration left the configurations %+v, audit %+v, events %v", s.w.configs, s.w.audited, s.w.events)
			}
		})
	}

	t.Run("a commit of unknown outcome is reported as such", func(t *testing.T) {
		s := newPortal(t)
		s.w.failing["commit"] = ports.ErrOutcomeUncertain

		if _, err := s.configure(alice, s.cargopai, "public", "production", ""); !errors.Is(err, ports.ErrOutcomeUncertain) {
			t.Fatalf("got %v, want %v", err, ports.ErrOutcomeUncertain)
		}
	})
}

// Whoever may read a product reads its audience configurations: its owners,
// the members of its workspaces and a platform administrator. For whom a
// product documents its APIs is part of what the product is.
func TestWhoeverMayReadTheProductReadsItsAudienceConfigurations(t *testing.T) {
	s := newPortal(t)
	s.w.now = started.Add(time.Minute)
	production := configured(t, s.service, s.cargopai, frank, domain.AudiencePublic, domain.EnvironmentProduction, "https://developers.cargopai.example")
	s.w.quiet()

	readers := map[string]domain.Principal{
		"the owner that configured it":  alice,
		"another owner":                 frank,
		"a member of a workspace":       carol,
		"a member of another workspace": dave,
		"the platform administrator":    admin,
	}
	for name, caller := range readers {
		if got, err := s.config(caller, s.cargopai, s.partners.ID); err != nil || got != s.partners {
			t.Errorf("%s reads a configuration: got %+v and %v, want %+v", name, got, err, s.partners)
		}
		page, err := s.listConfigs(caller, s.cargopai, 0, nil)
		if err != nil || !slices.Equal(page.Items, []domain.AudienceConfig{s.partners, production}) || page.Next != nil {
			t.Errorf("%s lists the configurations: got %+v and %v, want both and nothing to follow", name, page, err)
		}
	}
	s.untouched(t)

	// Whoever may read the product is told there is no such configuration
	// when none in force has the identifier under this product. That is no
	// refusal, and leaves no record.
	elsewhere := configured(t, s.service, s.parcel, bob, domain.AudiencePartner, domain.EnvironmentSandbox, "")
	s.w.quiet()
	missing := map[string]domain.AudienceConfigID{
		"an identifier that names nothing":            missingID,
		"an identifier that is none":                  "partner",
		"no identifier":                               "",
		"the identifier of a configuration of Parcel": elsewhere.ID,
	}
	for what, id := range missing {
		if got, err := s.config(carol, s.cargopai, id); err != ErrAudienceConfigNotFound || got != (domain.AudienceConfig{}) {
			t.Errorf("%s: got %+v and %v, want nothing and exactly %v", what, got, err, ErrAudienceConfigNotFound)
		}
	}
	// A product that is not there has no configuration to read.
	gone := domain.Product{ID: missingID}
	if _, err := s.config(admin, gone, s.partners.ID); err != ErrProductNotFound {
		t.Errorf("a configuration of a product that does not exist: got %v, want exactly %v", err, ErrProductNotFound)
	}
	if _, err := s.listConfigs(admin, gone, 0, nil); err != ErrProductNotFound {
		t.Errorf("the configurations of a product that does not exist: got %v, want exactly %v", err, ErrProductNotFound)
	}
	s.untouched(t)
}

// To whoever holds nothing in the product, the product does not exist, and
// neither do its configurations. The refusal is on record, and names the
// configuration when there is one.
func TestAudienceConfigurationsAreWithheldFromWhoeverHoldsNothingInTheProduct(t *testing.T) {
	strangers := map[string]domain.Principal{
		"a stranger":                               mallory,
		"the owner of another product":             bob,
		"whoever was a member and is not any more": erin,
		"whoever was an owner and is not any more": grace,
	}
	for name, caller := range strangers {
		t.Run(name, func(t *testing.T) {
			s := newPortal(t)

			for what, c := range map[string]struct {
				id    domain.AudienceConfigID
				named string
			}{
				"a configuration in force":         {s.partners.ID, string(s.partners.ID)},
				"an identifier that names nothing": {missingID, ""},
				"an identifier that is none":       {"partner", ""},
			} {
				s.w.quiet()
				got, err := s.config(caller, s.cargopai, c.id)
				if err != ErrProductNotFound || got != (domain.AudienceConfig{}) {
					t.Fatalf("%s: got %+v and %v, want nothing and exactly %v", what, got, err, ErrProductNotFound)
				}
				want := []audited{{ports.AuditEntry{
					Actor: caller, Action: "audience_config.read", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "ProductAudienceConfig", ResourceID: c.named, CorrelationID: "correlation-16",
				}, false}}
				if !slices.Equal(s.w.audited, want) {
					t.Errorf("%s: audited\n %+v\nwant\n %+v", what, s.w.audited, want)
				}
			}

			// A list is refused before anything of the page is looked at.
			for what, size := range map[string]int{"the first page": 0, "a page it cannot be": MaxPageSize + 1} {
				s.w.quiet()
				s.w.asked = nil
				page, err := s.listConfigs(caller, s.cargopai, size, nil)
				if err != ErrProductNotFound || page.Items != nil || page.Next != nil {
					t.Fatalf("%s: got %+v and %v, want nothing and exactly %v", what, page, err, ErrProductNotFound)
				}
				want := []audited{{ports.AuditEntry{
					Actor: caller, Action: "audience_config.read", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "ProductAudienceConfig", CorrelationID: "correlation-19",
				}, false}}
				if !slices.Equal(s.w.audited, want) || len(s.w.asked) != 0 {
					t.Errorf("%s: audited\n %+v\nand the store was asked %+v; want\n %+v\nand no read", what, s.w.audited, s.w.asked, want)
				}
			}
		})
	}
}

// The configurations of a product are listed a page at a time, oldest
// first: those in force, and those of that product alone. A product that
// documents its APIs for nobody has none, and a new product is one.
func TestListAudienceConfigsReadsTheConfigurationsInForceAPageAtATime(t *testing.T) {
	s := newPortal(t)
	want := []domain.AudienceConfig{s.partners}
	pairs := []struct {
		audience    domain.Audience
		environment domain.Environment
	}{
		{domain.AudiencePublic, domain.EnvironmentSandbox},
		{domain.AudiencePublic, domain.EnvironmentProduction},
		{domain.AudiencePartner, domain.EnvironmentProduction},
		{domain.AudienceInternal, domain.EnvironmentSandbox},
	}
	for index, pair := range pairs {
		s.w.now = started.Add(time.Duration(index+1) * time.Minute)
		want = append(want, configured(t, s.service, s.cargopai, alice, pair.audience, pair.environment, ""))
	}
	removed := configured(t, s.service, s.cargopai, frank, domain.AudienceInternal, domain.EnvironmentProduction, "")
	if err := s.removeConfig(frank, s.cargopai, removed.ID); err != nil {
		t.Fatalf("remove a configuration: %v", err)
	}
	configured(t, s.service, s.parcel, bob, domain.AudiencePublic, domain.EnvironmentProduction, "")
	s.w.quiet()

	read := func(size int, after *ports.Position) (Page[domain.AudienceConfig], error) {
		return s.listConfigs(dave, s.cargopai, size, after)
	}
	for _, size := range []int{1, 2, 4, 5, 6, MaxPageSize} {
		s.w.asked = nil
		pages := whole(t, size, read)
		if got := slices.Concat(pages...); !slices.Equal(got, want) {
			t.Errorf("in pages of %d the configurations are\n %+v\nwant\n %+v", size, got, want)
		}
		if wantPages := (len(want) + size - 1) / size; len(pages) != wantPages {
			t.Errorf("in pages of %d the list has %d pages, want %d", size, len(pages), wantPages)
		}
		// Each page was read behind the last configuration of the one
		// before, one configuration more than it holds.
		if len(s.w.asked) != len(pages) {
			t.Fatalf("in pages of %d the store was asked %d times for %d pages", size, len(s.w.asked), len(pages))
		}
		for index, asked := range s.w.asked {
			var behind *ports.Position
			if index > 0 {
				last := ports.PositionOfAudienceConfig(pages[index-1][len(pages[index-1])-1])
				behind = &last
			}
			if asked.Limit != size+1 || (asked.After == nil) != (behind == nil) || (behind != nil && *asked.After != *behind) {
				t.Errorf("in pages of %d, page %d was asked as %+v (behind %+v), want %d items behind %+v",
					size, index+1, asked, asked.After, size+1, behind)
			}
		}
	}

	if page, err := s.listConfigs(bob, s.parcel, 0, nil); err != nil || len(page.Items) != 1 || page.Next != nil {
		t.Errorf("the configurations of Parcel: got %+v and %v, want its own one", page, err)
	}
	fresh := registered(t, s.service, "fresh", alice)
	s.w.quiet()
	if page, err := s.listConfigs(alice, fresh, 0, nil); err != nil || len(page.Items) != 0 || page.Next != nil {
		t.Errorf("the configurations of a new product: got %+v and %v, want none", page, err)
	}
	s.untouched(t)
}

// A change is made from the version the caller read. It changes the entry
// and nothing else, and says who changed it and when.
func TestChangeAudienceConfigChangesTheEntryFromTheVersionItWasMadeFrom(t *testing.T) {
	s := newPortal(t)
	s.w.now = started.Add(time.Hour)

	changed, err := s.changeConfig(frank, s.cargopai, s.partners.ID, partnerDocs, 1)
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	want := s.partners
	want.DocsURL, want.Version, want.UpdatedAt, want.UpdatedBy = partnerDocs, 2, s.w.now, frank
	if changed != want || s.w.configs[s.partners.ID] != want {
		t.Errorf("changed\n %+v\nstored\n %+v\nwant\n %+v", changed, s.w.configs[s.partners.ID], want)
	}
	wantAudit := []audited{{ports.AuditEntry{
		Actor: frank, Action: "audience_config.change", Allowed: true, Rule: "AC-09",
		ProductID: s.cargopai.ID, ResourceType: "ProductAudienceConfig", ResourceID: string(s.partners.ID), CorrelationID: "correlation-17",
	}, true}}
	if !slices.Equal(s.w.audited, wantAudit) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", s.w.audited, wantAudit)
	}
	if want := []string{"AudienceConfigChanged " + string(s.partners.ID) + " changed at 2 correlation-17"}; !slices.Equal(s.w.events, want) {
		t.Errorf("events %v, want %v", s.w.events, want)
	}
	if s.w.transactions != 1 {
		t.Errorf("%d transactions, want 1", s.w.transactions)
	}

	// A change made from the version before is refused: somebody changed
	// the configuration since it was read. So is one made from a version
	// it never had. Nothing is recorded: the caller reads it again and
	// decides anew.
	for _, version := range []int64{1, 3, 0, -1} {
		s.w.quiet()
		if got, err := s.changeConfig(alice, s.cargopai, s.partners.ID, partnerPortal, version); !errors.Is(err, ErrVersionMismatch) || got != (domain.AudienceConfig{}) {
			t.Errorf("a change from version %d: got %+v and %v, want nothing and %v", version, got, err, ErrVersionMismatch)
		}
		if s.w.configs[s.partners.ID] != want || len(s.w.audited)+len(s.w.events) != 0 {
			t.Errorf("a change from version %d left the configuration %+v, audit %+v, events %v", version, s.w.configs[s.partners.ID], s.w.audited, s.w.events)
		}
	}

	// From the version in force the entry is taken away. The audience is
	// still documented for; nobody says where it enters.
	cleared, err := s.changeConfig(alice, s.cargopai, s.partners.ID, "", 2)
	if err != nil || cleared.DocsURL != "" || cleared.Version != 3 || cleared.UpdatedBy != alice || !cleared.InForce() {
		t.Errorf("take the entry away: got %+v and %v, want the configuration at version 3 without an entry", cleared, err)
	}
	if cleared.Audience != s.partners.Audience || cleared.Environment != s.partners.Environment || cleared.CreatedBy != alice {
		t.Errorf("a change of the entry changed what it is an entry of: %+v", cleared)
	}
}

// A change to the entry the configuration has changes nothing: it is
// answered with the configuration as it is, and nothing is stored, recorded
// or announced.
func TestTheSameEntryAgainChangesNothing(t *testing.T) {
	s := newPortal(t)
	s.w.now = started.Add(time.Hour)

	got, err := s.changeConfig(alice, s.cargopai, s.partners.ID, partnerPortal, 1)
	if err != nil || got != s.partners || s.w.configs[s.partners.ID] != s.partners {
		t.Errorf("the same entry again: got %+v and %v, stored %+v, want the configuration as it was", got, err, s.w.configs[s.partners.ID])
	}
	if len(s.w.audited)+len(s.w.events) != 0 {
		t.Errorf("a change that changed nothing left audit %+v and events %v", s.w.audited, s.w.events)
	}
}

// An owner of the product changes and removes its configurations, and
// nobody else. The answer to anyone else depends on the product alone: it is
// the same whether the configuration is there or not, and whatever the
// change says.
func TestChangeAndRemoveRefuseWhoeverDoesNotOwnTheProduct(t *testing.T) {
	for name, c := range withoutPortal() {
		t.Run(name, func(t *testing.T) {
			s := newPortal(t)
			before := maps.Clone(s.w.configs)

			for what, r := range map[string]struct {
				id      domain.AudienceConfigID
				docsURL string
				version int64
				named   string
			}{
				"a configuration, from its version":         {s.partners.ID, partnerDocs, 1, string(s.partners.ID)},
				"a configuration, from another version":     {s.partners.ID, partnerDocs, 7, string(s.partners.ID)},
				"a configuration, to an entry that is none": {s.partners.ID, "ftp://docs.cargopai.example", 1, string(s.partners.ID)},
				"a configuration that is not there":         {missingID, partnerDocs, 1, ""},
			} {
				s.w.quiet()
				changed, err := s.changeConfig(c.caller, s.cargopai, r.id, r.docsURL, r.version)
				if err != c.want || changed != (domain.AudienceConfig{}) {
					t.Fatalf("change %s: got %+v and %v, want nothing and exactly %v", what, changed, err, c.want)
				}
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "audience_config.change", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "ProductAudienceConfig", ResourceID: r.named, CorrelationID: "correlation-17",
				}, false}}
				if !slices.Equal(s.w.audited, want) {
					t.Errorf("change %s: audited\n %+v\nwant, outside the transaction,\n %+v", what, s.w.audited, want)
				}

				s.w.quiet()
				if err := s.removeConfig(c.caller, s.cargopai, r.id); err != c.want {
					t.Fatalf("remove %s: got %v, want exactly %v", what, err, c.want)
				}
				want[0].entry.Action, want[0].entry.CorrelationID = "audience_config.remove", "correlation-18"
				if !slices.Equal(s.w.audited, want) {
					t.Errorf("remove %s: audited\n %+v\nwant, outside the transaction,\n %+v", what, s.w.audited, want)
				}
			}
			if !maps.Equal(s.w.configs, before) || len(s.w.events) != 0 {
				t.Errorf("refused changes and removals left the configurations %+v and the events %v", s.w.configs, s.w.events)
			}
		})
	}
}

// What is changed or removed is a configuration in force of the product the
// request names. Anything else is not found, and nothing is recorded: that
// is no refusal of the caller.
func TestChangeAndRemoveFindOnlyAConfigurationInForceOfTheProduct(t *testing.T) {
	s := newPortal(t)
	elsewhere := configured(t, s.service, s.parcel, bob, domain.AudiencePartner, domain.EnvironmentSandbox, "")
	removed := configured(t, s.service, s.cargopai, alice, domain.AudiencePublic, domain.EnvironmentSandbox, "")
	if err := s.removeConfig(alice, s.cargopai, removed.ID); err != nil {
		t.Fatalf("remove a configuration: %v", err)
	}
	s.w.quiet()
	before := maps.Clone(s.w.configs)

	cargopai := string(s.cargopai.ID)
	for what, c := range map[string]struct {
		productID string
		id        domain.AudienceConfigID
		want      error
	}{
		"an identifier that names nothing":   {cargopai, missingID, ErrAudienceConfigNotFound},
		"an identifier that is none":         {cargopai, "partner", ErrAudienceConfigNotFound},
		"no identifier":                      {cargopai, "", ErrAudienceConfigNotFound},
		"a configuration of another product": {cargopai, elsewhere.ID, ErrAudienceConfigNotFound},
		"a configuration that was removed":   {cargopai, removed.ID, ErrAudienceConfigNotFound},
		"a product that does not exist":      {missingID, s.partners.ID, ErrProductNotFound},
		"a product that is no identifier":    {"cargopai", s.partners.ID, ErrProductNotFound},
	} {
		changed, errChange := s.service.ChangeAudienceConfig(ctx, ChangeAudienceConfig{
			Caller: alice, CorrelationID: "correlation-17", ProductID: c.productID, AudienceConfigID: string(c.id),
			DocsURL: partnerDocs, Version: 1,
		})
		errRemove := s.service.RemoveAudienceConfig(ctx, RemoveAudienceConfig{
			Caller: alice, CorrelationID: "correlation-18", ProductID: c.productID, AudienceConfigID: string(c.id),
		})
		if !errors.Is(errChange, c.want) || changed != (domain.AudienceConfig{}) || !errors.Is(errRemove, c.want) {
			t.Errorf("%s: the change got %+v and %v, the removal %v; want %v", what, changed, errChange, errRemove, c.want)
		}
	}
	if !maps.Equal(s.w.configs, before) || len(s.w.audited)+len(s.w.events) != 0 {
		t.Errorf("what was not found left the configurations %+v, audit %+v, events %v", s.w.configs, s.w.audited, s.w.events)
	}
}

func TestRemoveAudienceConfigEndsTheConfigurationAndKeepsItsRecord(t *testing.T) {
	s := newPortal(t)
	s.w.now = started.Add(time.Hour)

	if err := s.removeConfig(frank, s.cargopai, s.partners.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	want := s.partners
	want.Version, want.RemovedAt, want.RemovedBy = 2, s.w.now, frank
	if got := s.w.configs[s.partners.ID]; got != want || got.InForce() {
		t.Errorf("stored\n %+v\nwant, not in force,\n %+v", got, want)
	}
	wantAudit := []audited{{ports.AuditEntry{
		Actor: frank, Action: "audience_config.remove", Allowed: true, Rule: "AC-09",
		ProductID: s.cargopai.ID, ResourceType: "ProductAudienceConfig", ResourceID: string(s.partners.ID), CorrelationID: "correlation-18",
	}, true}}
	if !slices.Equal(s.w.audited, wantAudit) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", s.w.audited, wantAudit)
	}
	if want := []string{"AudienceConfigChanged " + string(s.partners.ID) + " removed at 2 correlation-18"}; !slices.Equal(s.w.events, want) {
		t.Errorf("events %v, want %v", s.w.events, want)
	}
	if s.w.transactions != 1 {
		t.Errorf("%d transactions, want 1", s.w.transactions)
	}

	// From then on it is in no answer. A removal sent again finds nothing
	// to remove, and neither does a change: that is no refusal, and leaves
	// no record.
	s.w.quiet()
	if got, err := s.config(alice, s.cargopai, s.partners.ID); err != ErrAudienceConfigNotFound {
		t.Errorf("read the removed configuration: got %+v and %v, want exactly %v", got, err, ErrAudienceConfigNotFound)
	}
	if page, err := s.listConfigs(alice, s.cargopai, 0, nil); err != nil || len(page.Items) != 0 {
		t.Errorf("list the configurations: got %+v and %v, want none", page, err)
	}
	if err := s.removeConfig(alice, s.cargopai, s.partners.ID); !errors.Is(err, ErrAudienceConfigNotFound) {
		t.Errorf("remove it again: got %v, want %v", err, ErrAudienceConfigNotFound)
	}
	if _, err := s.changeConfig(alice, s.cargopai, s.partners.ID, partnerDocs, 2); !errors.Is(err, ErrAudienceConfigNotFound) {
		t.Errorf("change it once removed: got %v, want %v", err, ErrAudienceConfigNotFound)
	}
	if s.w.configs[s.partners.ID] != want || len(s.w.audited)+len(s.w.events) != 0 {
		t.Errorf("what found nothing left the configuration %+v, audit %+v, events %v", s.w.configs[s.partners.ID], s.w.audited, s.w.events)
	}

	// The pair can be configured again. That is another configuration, and
	// the record of the one removed stays as the removal left it.
	again, err := s.configure(alice, s.cargopai, "partner", "sandbox", partnerDocs)
	if err != nil || again.ID == s.partners.ID || again.Version != 1 || !again.InForce() {
		t.Errorf("configure the pair again: got %+v and %v, want a new configuration", again, err)
	}
	if s.w.configs[s.partners.ID] != want {
		t.Errorf("the record of the removed configuration is now %+v, want it as it was", s.w.configs[s.partners.ID])
	}
}

// What a change or a removal decides from is read once the configuration is
// locked, and one that finds it locked waits. So it decides from what the
// one it waited for left behind, not from what was there when it arrived.
func TestAChangeOrARemovalThatWaitedDecidesFromWhatItFindsThen(t *testing.T) {
	// rewritten and ended are the configuration as it is once by changed
	// its entry or removed it, in a transaction of its own that committed.
	rewritten := func(s portal, docsURL string, by domain.Principal) func() {
		return func() {
			changed, err := s.partners.ChangeDocsURL(s.cargopai, docsURL, by, s.w.now)
			if err != nil {
				t.Fatalf("change the entry: %v", err)
			}
			s.w.configs[s.partners.ID] = changed
		}
	}
	ended := func(s portal, by domain.Principal) func() {
		return func() {
			removed, err := s.partners.Remove(by, s.w.now)
			if err != nil {
				t.Fatalf("remove the configuration: %v", err)
			}
			s.w.configs[s.partners.ID] = removed
		}
	}

	t.Run("two owners change the entry from one version at once", func(t *testing.T) {
		s := newPortal(t)
		// While alice waits to change the entry, frank changes it.
		s.w.meanwhile = rewritten(s, partnerDocs, frank)

		if got, err := s.changeConfig(alice, s.cargopai, s.partners.ID, "https://partners.cargopai.example", 1); !errors.Is(err, ErrVersionMismatch) || got != (domain.AudienceConfig{}) {
			t.Fatalf("got %+v and %v, want nothing and %v", got, err, ErrVersionMismatch)
		}
		// frank's change stands, and alice's left nothing.
		if got := s.w.configs[s.partners.ID]; got.DocsURL != partnerDocs || got.Version != 2 || got.UpdatedBy != frank {
			t.Errorf("the configuration is %+v, want it as frank changed it", got)
		}
		if len(s.w.audited)+len(s.w.events) != 0 {
			t.Errorf("the change that waited left audit %+v and events %v", s.w.audited, s.w.events)
		}
	})

	t.Run("the configuration was removed in the meantime", func(t *testing.T) {
		s := newPortal(t)
		s.w.meanwhile = ended(s, frank)

		if _, err := s.changeConfig(alice, s.cargopai, s.partners.ID, partnerDocs, 1); !errors.Is(err, ErrAudienceConfigNotFound) {
			t.Fatalf("a change: got %v, want %v", err, ErrAudienceConfigNotFound)
		}
		// In force again, it is removed once more while alice waits to
		// remove it herself.
		s.w.configs[s.partners.ID] = s.partners
		if err := s.removeConfig(alice, s.cargopai, s.partners.ID); !errors.Is(err, ErrAudienceConfigNotFound) {
			t.Fatalf("a removal: got %v, want %v", err, ErrAudienceConfigNotFound)
		}
		// It is removed, by frank, and what alice asked for left nothing.
		if got := s.w.configs[s.partners.ID]; got.InForce() || got.RemovedBy != frank || len(s.w.audited)+len(s.w.events) != 0 {
			t.Errorf("the configuration is %+v, audit %+v, events %v, want it removed by frank and nothing recorded", got, s.w.audited, s.w.events)
		}
	})

	t.Run("a removal removes the configuration as the change it waited for left it", func(t *testing.T) {
		s := newPortal(t)
		s.w.meanwhile = rewritten(s, partnerDocs, frank)

		if err := s.removeConfig(alice, s.cargopai, s.partners.ID); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if got := s.w.configs[s.partners.ID]; got.InForce() || got.Version != 3 || got.DocsURL != partnerDocs || got.UpdatedBy != frank || got.RemovedBy != alice {
			t.Errorf("the configuration is %+v, want frank's change removed by alice at version 3", got)
		}
		if want := []string{"AudienceConfigChanged " + string(s.partners.ID) + " removed at 3 correlation-18"}; !slices.Equal(s.w.events, want) {
			t.Errorf("events %v, want %v", s.w.events, want)
		}
	})
}

// INV-13: a change or a removal, its audit fact and its event are one
// transaction. Whatever fails takes all of it down, and the configuration
// is as it was.
func TestChangeAndRemoveLeaveTheConfigurationAsItWasWhenAnyPartOfThemFails(t *testing.T) {
	down := errors.New("down")
	operations := map[string]struct {
		steps []string
		run   func(t *testing.T, s portal) error
	}{
		"a change": {
			[]string{"audit", "events", "commit", "read", "held", "roles", "configs", "lock", "change"},
			func(t *testing.T, s portal) error {
				changed, err := s.changeConfig(alice, s.cargopai, s.partners.ID, partnerDocs, 1)
				if err != nil && changed != (domain.AudienceConfig{}) {
					t.Errorf("a change that failed returned %+v", changed)
				}
				return err
			},
		},
		"a removal": {
			[]string{"audit", "events", "commit", "read", "held", "roles", "configs", "lock", "remove"},
			func(_ *testing.T, s portal) error { return s.removeConfig(alice, s.cargopai, s.partners.ID) },
		},
	}
	for operation, c := range operations {
		for _, step := range c.steps {
			t.Run(operation+" with "+step+" failing", func(t *testing.T) {
				s := newPortal(t)
				before := maps.Clone(s.w.configs)
				s.w.failing[step] = down

				if err := c.run(t, s); !errors.Is(err, down) {
					t.Fatalf("got %v, want the failure", err)
				}
				if !maps.Equal(s.w.configs, before) || len(s.w.audited)+len(s.w.events) != 0 {
					t.Errorf("it left the configurations %+v, audit %+v, events %v", s.w.configs, s.w.audited, s.w.events)
				}
			})
		}
	}

	// A store that finds the configuration otherwise than it was locked is
	// reported as somebody else's change, or as a removal, and leaves
	// nothing of the store in the answer.
	for name, c := range map[string]struct {
		step string
		want error
	}{
		"a change":  {"change", ErrVersionMismatch},
		"a removal": {"remove", ErrAudienceConfigNotFound},
	} {
		s := newPortal(t)
		s.w.failing[c.step] = ports.ErrAudienceConfigGone
		if err := operations[name].run(t, s); !errors.Is(err, c.want) || errors.Is(err, ports.ErrAudienceConfigGone) {
			t.Errorf("%s that lost the race: got %v, want %v and nothing of the store", name, err, c.want)
		}
		if s.w.configs[s.partners.ID] != s.partners || len(s.w.audited)+len(s.w.events) != 0 {
			t.Errorf("%s that lost the race left the configuration %+v, audit %+v, events %v", name, s.w.configs[s.partners.ID], s.w.audited, s.w.events)
		}
	}

	s := newPortal(t)
	s.w.failing["commit"] = ports.ErrOutcomeUncertain
	if err := s.removeConfig(alice, s.cargopai, s.partners.ID); !errors.Is(err, ports.ErrOutcomeUncertain) {
		t.Errorf("a removal whose commit has an unknown outcome: got %v, want %v", err, ports.ErrOutcomeUncertain)
	}
}

// A refusal stands whether or not it could be recorded. What cannot be read
// is a failure, not an answer, and a request by nobody is refused.
func TestTheAudiencesRefuseAndFailAsEverythingElseDoes(t *testing.T) {
	s := newPortal(t)
	s.w.failing["audit"] = errors.New("the audit trail is down")

	_, errConfigure := s.configure(carol, s.cargopai, "public", "production", "")
	_, errChange := s.changeConfig(admin, s.cargopai, s.partners.ID, partnerDocs, 1)
	errRemove := s.removeConfig(mallory, s.cargopai, s.partners.ID)
	_, errGet := s.config(bob, s.cargopai, s.partners.ID)
	_, errList := s.listConfigs(erin, s.cargopai, 0, nil)
	for what, c := range map[string]struct {
		err  error
		want error
	}{
		"a configuration by a member":            {errConfigure, ErrForbidden},
		"a change by the administrator":          {errChange, ErrForbidden},
		"a removal by a stranger":                {errRemove, ErrProductNotFound},
		"a read by the owner of another product": {errGet, ErrProductNotFound},
		"a list by a former member":              {errList, ErrProductNotFound},
	} {
		if !errors.Is(c.err, c.want) || !errors.Is(c.err, ErrRefusalNotAudited) {
			t.Errorf("%s: got %v, want %v and %v", what, c.err, c.want, ErrRefusalNotAudited)
		}
	}
	delete(s.w.failing, "audit")

	down := errors.New("the database is down")
	for _, step := range []string{"read", "roles", "held", "configs", "list"} {
		s.w.failing[step] = down
		got, errGet := s.config(carol, s.cargopai, s.partners.ID)
		page, errList := s.listConfigs(carol, s.cargopai, 0, nil)
		if step != "list" && (!errors.Is(errGet, down) || got != (domain.AudienceConfig{})) {
			t.Errorf("a read with %q failing: got %+v and %v, want nothing and the failure", step, got, errGet)
		}
		if step != "configs" && (!errors.Is(errList, down) || page.Items != nil) {
			t.Errorf("a list with %q failing: got %+v and %v, want nothing and the failure", step, page, errList)
		}
		delete(s.w.failing, step)
	}

	s.w.quiet()
	nobody := domain.Principal{}
	_, errConfigure = s.configure(nobody, s.cargopai, "public", "production", "")
	_, errChange = s.changeConfig(nobody, s.cargopai, s.partners.ID, partnerDocs, 1)
	errRemove = s.removeConfig(nobody, s.cargopai, s.partners.ID)
	_, errGet = s.config(nobody, s.cargopai, s.partners.ID)
	_, errList = s.listConfigs(nobody, s.cargopai, 0, nil)
	for what, err := range map[string]error{
		"a configuration": errConfigure, "a change": errChange, "a removal": errRemove, "a read": errGet, "a list": errList,
	} {
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("%s by nobody: got %v, want %v", what, err, ErrForbidden)
		}
	}
	if s.w.configs[s.partners.ID] != s.partners || len(s.w.configs) != 1 {
		t.Errorf("what was refused or failed left the configurations %+v", s.w.configs)
	}
	s.untouched(t)
}
