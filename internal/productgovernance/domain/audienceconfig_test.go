package domain

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	audienceConfigID = AudienceConfigID("8c9d0e1f-2a3b-4c4d-9e5f-6a7b8c9d0e1f")
	partnerPortal    = "https://developers.cargopai.example/partner"
)

// configured makes the product document its APIs for partners in the
// sandbox, as alice configured it.
func configured(t *testing.T) AudienceConfig {
	t.Helper()

	config, err := ConfigureAudience(audienceConfigID, registered(t), AudiencePartner, EnvironmentSandbox, partnerPortal, alice, created)
	if err != nil {
		t.Fatalf("configure the audience: %v", err)
	}
	return config
}

// FR-005: a configuration names its product, its audience, its environment
// and its entry, and says who configured it and since when it is in force.
func TestAnAudienceIsConfiguredInForceAtVersionOne(t *testing.T) {
	at := time.Date(2026, 11, 16, 14, 0, 0, 123456789, time.FixedZone("UTC+8", 8*60*60))

	config, err := ConfigureAudience(audienceConfigID, registered(t), AudiencePartner, EnvironmentSandbox, partnerPortal, alice, at)
	if err != nil {
		t.Fatalf("configure the audience: %v", err)
	}
	when := time.Date(2026, 11, 16, 6, 0, 0, 123456000, time.UTC)
	want := AudienceConfig{
		ID: audienceConfigID, ProductID: productID, Audience: AudiencePartner, Environment: EnvironmentSandbox,
		DocsURL: partnerPortal, Version: 1, CreatedAt: when, CreatedBy: alice, UpdatedAt: when, UpdatedBy: alice,
	}
	if config != want {
		t.Errorf("configuration\n %+v\nwant\n %+v", config, want)
	}
	if config.CreatedAt.Location() != time.UTC {
		t.Errorf("the configuration is timed in %s, want UTC", config.CreatedAt.Location())
	}
	if !config.InForce() {
		t.Error("a configuration that was just added is not in force")
	}
}

// What each audience may read, and what each environment allows, is a rule
// of the platform. A product documents its APIs for those audiences in those
// environments, and for no other.
func TestAnAudienceIsOneOfThePlatformInOneOfItsEnvironments(t *testing.T) {
	product := registered(t)

	if want := []Audience{"public", "partner", "internal"}; !slices.Equal(Audiences(), want) {
		t.Fatalf("the audiences are %v, want %v", Audiences(), want)
	}
	if want := []Environment{"sandbox", "production"}; !slices.Equal(Environments(), want) {
		t.Fatalf("the environments are %v, want %v", Environments(), want)
	}
	for _, audience := range Audiences() {
		for _, environment := range Environments() {
			if _, err := ConfigureAudience(audienceConfigID, product, audience, environment, "", alice, created); err != nil {
				t.Errorf("%s in %s was refused: %v", audience, environment, err)
			}
		}
	}

	refused := map[string]struct {
		audience    Audience
		environment Environment
	}{
		"no audience":                   {"", EnvironmentSandbox},
		"no environment":                {AudiencePublic, ""},
		"an audience in upper case":     {"PUBLIC", EnvironmentSandbox},
		"an audience nobody defined":    {"external", EnvironmentSandbox},
		"an audience with a space":      {"partner ", EnvironmentSandbox},
		"a role for an audience":        {"product_owner", EnvironmentSandbox},
		"an environment nobody defined": {AudiencePublic, "staging"},
		"an environment abbreviated":    {AudiencePublic, "prod"},
		"an environment in upper case":  {AudiencePublic, "Production"},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			if config, err := ConfigureAudience(audienceConfigID, product, c.audience, c.environment, "", alice, created); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %+v and %v, want %v", config, err, ErrInvalid)
			}
		})
	}
}

// The entry of the documentation is an https URL of a host, or nothing yet.
func TestTheEntryOfTheDocumentation(t *testing.T) {
	product := registered(t)
	const prefix = "https://docs.cargopai.example/"
	longest := prefix + strings.Repeat("文", DocsURLMaxLength-len(prefix))

	accepted := []string{
		"",
		"https://developers.cargopai.example",
		"https://developers.cargopai.example/",
		"https://Developers.CargoPai.example/Partner",
		"https://partners.cargopai.example:8443/sandbox/docs?lang=zh-CN#quick-start",
		"https://partners.cargopai.example?audience=partner",
		"https://partners.cargopai.example#/sandbox",
		"https://10.20.30.40/docs",
		"https://xn--fiq228c.example/文档/快速开始",
		// Past the host, an at sign or a backslash is part of the path.
		"https://docs.cargopai.example/a@b/c\\d",
		// The bound is in characters, whatever a character takes in bytes.
		longest,
	}
	for _, docsURL := range accepted {
		if _, err := ConfigureAudience(audienceConfigID, product, AudiencePublic, EnvironmentProduction, docsURL, alice, created); err != nil {
			t.Errorf("the entry %q was refused: %v", docsURL, err)
		}
	}

	refused := map[string]string{
		"plain http":                "http://developers.cargopai.example/",
		"the scheme in upper case":  "HTTPS://developers.cargopai.example/",
		"another scheme":            "ftp://developers.cargopai.example/",
		"a script":                  "javascript:alert(1)",
		"no scheme":                 "developers.cargopai.example/docs",
		"relative to the scheme":    "//developers.cargopai.example/docs",
		"no host":                   "https://",
		"no host before the path":   "https:///docs",
		"credentials":               "https://alice:secret@developers.cargopai.example/",
		"a user":                    "https://alice@developers.cargopai.example/",
		"an empty user":             "https://@developers.cargopai.example/",
		"a backslash in the host":   "https://developers.cargopai.example\\@evil.example/",
		"a host that is not ASCII":  "https://开发者.cargopai.example/",
		"a host ending in a hyphen": "https://developers-.cargopai.example/",
		"a host ending in a dot":    "https://developers.cargopai.example./",
		"a port that is no number":  "https://developers.cargopai.example:https/",
		"a port of six digits":      "https://developers.cargopai.example:443443/",
		"an IPv6 address":           "https://[2001:db8::1]/docs",
		"a space":                   "https://developers.cargopai.example/quick start",
		"an ideographic space":      "https://developers.cargopai.example/快速\u3000开始",
		"a line break":              "https://developers.cargopai.example/\nSet-Cookie: x",
		"a tab":                     "https://developers.cargopai.example/\tdocs",
		"a control character":       "https://developers.cargopai.example/\x00",
		"a space in front":          " https://developers.cargopai.example/",
		"too long":                  longest + "x",
		"not UTF-8":                 "https://developers.cargopai.example/\xff",
	}
	for name, docsURL := range refused {
		t.Run(name, func(t *testing.T) {
			if config, err := ConfigureAudience(audienceConfigID, product, AudiencePublic, EnvironmentProduction, docsURL, alice, created); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %+v and %v, want %v", config, err, ErrInvalid)
			}
		})
	}
}

func TestAnAudienceConfigurationNeedsAnIdentifierSomebodyAndATime(t *testing.T) {
	product := registered(t)

	cases := map[string]func() (AudienceConfig, error){
		"an identifier that is not a UUID": func() (AudienceConfig, error) {
			return ConfigureAudience("partner-sandbox", product, AudiencePartner, EnvironmentSandbox, "", alice, created)
		},
		"nobody": func() (AudienceConfig, error) {
			return ConfigureAudience(audienceConfigID, product, AudiencePartner, EnvironmentSandbox, "", Principal{}, created)
		},
		"somebody without a subject": func() (AudienceConfig, error) {
			return ConfigureAudience(audienceConfigID, product, AudiencePartner, EnvironmentSandbox, "", Principal{Issuer: alice.Issuer}, created)
		},
		"no time": func() (AudienceConfig, error) {
			return ConfigureAudience(audienceConfigID, product, AudiencePartner, EnvironmentSandbox, "", alice, time.Time{})
		},
	}
	for name, configure := range cases {
		t.Run(name, func(t *testing.T) {
			if config, err := configure(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %+v and %v, want %v", config, err, ErrInvalid)
			}
		})
	}
}

// INV-01: a suspended product takes no new configuration and no change. What
// it has can still be removed.
func TestASuspendedProductTakesNoAudienceConfigurationAndNoChange(t *testing.T) {
	before := configured(t)
	product := registered(t)
	product.Status = ProductSuspended

	if config, err := ConfigureAudience(audienceConfigID, product, AudiencePublic, EnvironmentSandbox, "", alice, created); !errors.Is(err, ErrProductNotActive) {
		t.Errorf("configure: got %+v and %v, want %v", config, err, ErrProductNotActive)
	}
	if changed, err := before.ChangeDocsURL(product, "https://elsewhere.example/", alice, created.Add(time.Hour)); !errors.Is(err, ErrProductNotActive) {
		t.Errorf("change: got %+v and %v, want %v", changed, err, ErrProductNotActive)
	}
	if _, err := before.Remove(alice, created.Add(time.Hour)); err != nil {
		t.Errorf("a configuration of a suspended product could not be removed: %v", err)
	}
}

func TestAChangeOfTheEntrySaysWhoChangedItAndWhen(t *testing.T) {
	config := configured(t)
	at := time.Date(2026, 11, 17, 9, 30, 0, 987654321, time.FixedZone("UTC+8", 8*60*60))

	changed, err := config.ChangeDocsURL(registered(t), "https://partners.cargopai.example/docs", bob, at)
	if err != nil {
		t.Fatalf("change the entry: %v", err)
	}
	want := config
	want.DocsURL, want.Version = "https://partners.cargopai.example/docs", 2
	want.UpdatedAt, want.UpdatedBy = time.Date(2026, 11, 17, 1, 30, 0, 987654000, time.UTC), bob
	if changed != want {
		t.Errorf("changed\n %+v\nwant\n %+v", changed, want)
	}
	if changed.UpdatedAt.Location() != time.UTC {
		t.Errorf("the change is timed in %s, want UTC", changed.UpdatedAt.Location())
	}
	// Changing returns the configuration as it is from then on. The one it
	// was called on is a value and stays what it was.
	if config.DocsURL != partnerPortal || config.Version != 1 || config.UpdatedBy != alice {
		t.Errorf("the configuration that was changed changed in place: %+v", config)
	}

	// An entry can be taken away again, which is a change like any other.
	cleared, err := changed.ChangeDocsURL(registered(t), "", alice, at.Add(time.Hour))
	if err != nil {
		t.Fatalf("take the entry away: %v", err)
	}
	if cleared.DocsURL != "" || cleared.Version != 3 || cleared.UpdatedBy != alice || !cleared.InForce() {
		t.Errorf("cleared %+v, want no entry at version 3, by alice and in force", cleared)
	}
}

// Setting the entry to what it is already changes nothing: not the version,
// and not who changed the configuration last.
func TestTheSameEntryAgainIsNoChange(t *testing.T) {
	config := configured(t)

	same, err := config.ChangeDocsURL(registered(t), partnerPortal, bob, created.Add(time.Hour))
	if err != nil {
		t.Fatalf("change the entry: %v", err)
	}
	if same != config {
		t.Errorf("got\n %+v\nwant it as it was\n %+v", same, config)
	}
}

func TestAChangeNeedsTheProductOfTheConfigurationAnEntrySomebodyAndATime(t *testing.T) {
	config := configured(t)
	product := registered(t)
	other := product
	other.ID = ProductID("0f1e2d3c-4b5a-4968-8776-655443322110")

	cases := map[string]func() (AudienceConfig, error){
		"another product": func() (AudienceConfig, error) {
			return config.ChangeDocsURL(other, "https://elsewhere.example/", alice, created)
		},
		"an entry that is not https": func() (AudienceConfig, error) {
			return config.ChangeDocsURL(product, "http://elsewhere.example/", alice, created)
		},
		"nobody": func() (AudienceConfig, error) {
			return config.ChangeDocsURL(product, "https://elsewhere.example/", Principal{}, created)
		},
		"no time": func() (AudienceConfig, error) {
			return config.ChangeDocsURL(product, "https://elsewhere.example/", alice, time.Time{})
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			if changed, err := change(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %+v and %v, want %v", changed, err, ErrInvalid)
			}
		})
	}
}

func TestARemovedAudienceConfigurationIsARecord(t *testing.T) {
	config := configured(t)
	at := time.Date(2026, 11, 18, 1, 30, 0, 987654321, time.FixedZone("UTC+8", 8*60*60))

	removed, err := config.Remove(bob, at)
	if err != nil {
		t.Fatalf("remove the configuration: %v", err)
	}
	want := config
	want.Version, want.RemovedAt, want.RemovedBy = 2, time.Date(2026, 11, 17, 17, 30, 0, 987654000, time.UTC), bob
	if removed != want {
		t.Errorf("removed\n %+v\nwant\n %+v", removed, want)
	}
	if removed.InForce() {
		t.Error("a removed configuration is in force")
	}
	if removed.RemovedAt.Location() != time.UTC {
		t.Errorf("the removal is timed in %s, want UTC", removed.RemovedAt.Location())
	}
	if !config.InForce() || config.Version != 1 {
		t.Errorf("the configuration that was removed changed in place: %+v", config)
	}

	// Nothing about a record changes again, by anyone.
	if again, err := removed.Remove(alice, at.Add(time.Hour)); !errors.Is(err, ErrAudienceConfigRemoved) || again != (AudienceConfig{}) {
		t.Errorf("remove again: got %+v and %v, want nothing and %v", again, err, ErrAudienceConfigRemoved)
	}
	if changed, err := removed.ChangeDocsURL(registered(t), "https://elsewhere.example/", alice, at.Add(time.Hour)); !errors.Is(err, ErrAudienceConfigRemoved) || changed != (AudienceConfig{}) {
		t.Errorf("change: got %+v and %v, want nothing and %v", changed, err, ErrAudienceConfigRemoved)
	}
}

func TestARemovalNeedsSomebodyAndATime(t *testing.T) {
	config := configured(t)

	if removed, err := config.Remove(Principal{}, created.Add(time.Hour)); !errors.Is(err, ErrInvalid) {
		t.Errorf("nobody: got %+v and %v, want %v", removed, err, ErrInvalid)
	}
	if removed, err := config.Remove(alice, time.Time{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("no time: got %+v and %v, want %v", removed, err, ErrInvalid)
	}
}

// A change or a removal is never refused for the time it carries. A process
// whose clock runs behind the one that changed the configuration last
// changes or ends it at the instant of that change.
func TestAClockThatRunsBehindDoesNotKeepAConfigurationFromChanging(t *testing.T) {
	config := configured(t)
	changed, err := config.ChangeDocsURL(registered(t), "https://elsewhere.example/", bob, created.Add(time.Hour))
	if err != nil {
		t.Fatalf("change the entry: %v", err)
	}

	again, err := changed.ChangeDocsURL(registered(t), "https://again.example/", alice, changed.UpdatedAt.Add(-time.Minute))
	if err != nil {
		t.Fatalf("change the entry again: %v", err)
	}
	if !again.UpdatedAt.Equal(changed.UpdatedAt) || again.Version != 3 {
		t.Errorf("changed again at %s and version %d, want at %s and version 3", again.UpdatedAt, again.Version, changed.UpdatedAt)
	}
	removed, err := again.Remove(alice, config.CreatedAt.Add(-time.Hour))
	if err != nil {
		t.Fatalf("remove the configuration: %v", err)
	}
	if !removed.RemovedAt.Equal(again.UpdatedAt) || removed.InForce() {
		t.Errorf("removed at %s and in force %t, want at %s and not in force", removed.RemovedAt, removed.InForce(), again.UpdatedAt)
	}
}

func TestAnAudienceConfigIdentifierIsACanonicalUUID(t *testing.T) {
	if id, err := ParseAudienceConfigID(string(audienceConfigID)); err != nil || id != audienceConfigID {
		t.Errorf("got %q and %v, want the identifier back", id, err)
	}
	for _, value := range []string{"", "partner-sandbox", strings.ToUpper(string(audienceConfigID)), string(audienceConfigID) + "0", "{" + string(audienceConfigID) + "}"} {
		if _, err := ParseAudienceConfigID(value); !errors.Is(err, ErrInvalid) {
			t.Errorf("audience configuration identifier %q: got %v, want %v", value, err, ErrInvalid)
		}
	}
}

// The errors are written for a log and for a caller. They repeat nothing of
// the value they refuse.
func TestAnErrorOfAnAudienceConfigurationRepeatsNothingOfTheValue(t *testing.T) {
	product := registered(t)

	cases := map[string]func() (AudienceConfig, error){
		"an identifier": func() (AudienceConfig, error) {
			return ConfigureAudience("canary", product, AudiencePublic, EnvironmentSandbox, "", alice, created)
		},
		"an audience": func() (AudienceConfig, error) {
			return ConfigureAudience(audienceConfigID, product, "canary", EnvironmentSandbox, "", alice, created)
		},
		"an environment": func() (AudienceConfig, error) {
			return ConfigureAudience(audienceConfigID, product, AudiencePublic, "canary", "", alice, created)
		},
		"an entry": func() (AudienceConfig, error) {
			return ConfigureAudience(audienceConfigID, product, AudiencePublic, EnvironmentSandbox, "https://canary.example/ canary", alice, created)
		},
		"a changed entry": func() (AudienceConfig, error) {
			return configured(t).ChangeDocsURL(product, "http://canary.example/", alice, created)
		},
	}
	for name, refuse := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := refuse(); err == nil || strings.Contains(err.Error(), "canary") {
				t.Errorf("the error is %v", err)
			}
		})
	}
}
