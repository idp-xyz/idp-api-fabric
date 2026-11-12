package static

import (
	"errors"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

func TestAnAdministratorIsAnIssuerAndASubjectTogether(t *testing.T) {
	admin := domain.Principal{Issuer: "https://issuer.example", Subject: "platform-admin"}
	administrators, err := NewAdministrators([]domain.Principal{admin})
	if err != nil {
		t.Fatalf("build the administrators: %v", err)
	}

	if !administrators.Includes(admin) || administrators.Count() != 1 {
		t.Error("the administrator is not among the administrators")
	}
	for name, principal := range map[string]domain.Principal{
		"the subject under another issuer": {Issuer: "https://other.example", Subject: admin.Subject},
		"another subject":                  {Issuer: admin.Issuer, Subject: "alice"},
		"the subject in another case":      {Issuer: admin.Issuer, Subject: "Platform-Admin"},
		"nobody":                           {},
	} {
		if administrators.Includes(principal) {
			t.Errorf("%s administers the platform", name)
		}
	}
}

func TestNobodyAdministersAPlatformConfiguredWithNobody(t *testing.T) {
	administrators, err := NewAdministrators(nil)
	if err != nil {
		t.Fatalf("build the administrators: %v", err)
	}
	if administrators.Count() != 0 || administrators.Includes(domain.Principal{}) ||
		administrators.Includes(domain.Principal{Issuer: "https://issuer.example", Subject: "alice"}) {
		t.Error("somebody administers a platform configured with nobody")
	}
}

func TestAnAdministratorThatIsNoPrincipalIsRefused(t *testing.T) {
	for name, principal := range map[string]domain.Principal{
		"no subject": {Issuer: "https://issuer.example"},
		"no issuer":  {Subject: "platform-admin"},
	} {
		if _, err := NewAdministrators([]domain.Principal{principal}); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: got %v, want %v", name, err, domain.ErrInvalid)
		}
	}
}

func TestTheListOfAdministratorsIsFixedWhenItIsBuilt(t *testing.T) {
	principals := []domain.Principal{{Issuer: "https://issuer.example", Subject: "platform-admin"}}
	administrators, err := NewAdministrators(principals)
	if err != nil {
		t.Fatal(err)
	}
	principals[0].Subject = "mallory"

	if administrators.Includes(domain.Principal{Issuer: "https://issuer.example", Subject: "mallory"}) {
		t.Error("changing the list that was passed in changed who administers the platform")
	}
}

func TestOnlyTheConfiguredIssuersAreTrusted(t *testing.T) {
	issuers := NewIssuers("https://issuer.example")

	if !issuers.Trusts("https://issuer.example") {
		t.Error("the configured issuer is not trusted")
	}
	for _, issuer := range []string{"", "https://other.example", "https://issuer.example/", "HTTPS://ISSUER.EXAMPLE"} {
		if issuers.Trusts(issuer) {
			t.Errorf("%q is trusted", issuer)
		}
	}
	if NewIssuers().Trusts("") || NewIssuers("").Trusts("") {
		t.Error("no issuer at all is trusted")
	}
}
