package openapi_test

import (
	"reflect"
	"strings"
	"testing"
)

// noted is the contract no rule has anything to say of, with one line
// behind it as a comment. The scans read bytes, so a comment is read as
// anything else is, and the line is line 13.
func noted(line string) string { return minimal + "# " + line + "\n" }

const (
	jwt       = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	hexToken  = "8f2c1d9e7b6a5f4e3d2c1b0a9f8e7d6c"
	gitHubPAT = "ghp_0123456789abcdefghijklmnopqrstuvwxyzAB"
)

// F04-GOV-004: a line that holds what has the form of a credential is a
// finding. The finding names the line and the kind of what was found, and
// never what was found: a check must not be the second place a credential
// is stored in.
func TestALineThatHoldsACredentialIsAFinding(t *testing.T) {
	found := map[string]struct {
		line  string
		kinds string
	}{
		"a bearer token":                    {"Authorization: Bearer abcdefghijklmnopqrstuv1234567890", "bearer token"},
		"a bearer token in lower case":      {"authorization: bearer 0123456789abcdef", "bearer token"},
		"a bearer token in a command":       {`curl -H "Authorization: Bearer ` + hexToken + `" https://fabric.example.org`, "bearer token"},
		"a bearer token that is a JWT":      {"Authorization: Bearer " + jwt, "bearer token, JSON Web Token"},
		"a JWT by itself":                   {"example: " + jwt, "JSON Web Token"},
		"a secret of a client":              {"client_secret: s3cr3t-0f-the-cl1ent", "secret assigned to a name"},
		"a secret of a client in JSON":      {`"clientSecret": "s3cr3t-0f-the-cl1ent"`, "secret assigned to a name"},
		"an API key in an environment":      {"FABRIC_API_KEY=sk_live_51H8abcdefghijkl", "secret assigned to a name"},
		"an API key in a header":            {"x-api-key: 0123456789abcdef", "secret assigned to a name"},
		"a password":                        {"password = hunter2hunter2", "secret assigned to a name"},
		"a password of digits":              {"passwd: 123456789012", "secret assigned to a name"},
		"an access token":                   {"access_token: 2YotnFZFEjr1zCsicMWpAA", "secret assigned to a name"},
		"a refresh token":                   {"refresh-token='tGzv3JOkF0XG5Qx2TlKWIA'", "secret assigned to a name"},
		"a private key":                     {"-----BEGIN PRIVATE KEY-----", "private key"},
		"an RSA private key":                {"key: '-----BEGIN RSA PRIVATE KEY-----'", "private key"},
		"an OpenSSH private key":            {"-----BEGIN OPENSSH PRIVATE KEY-----", "private key"},
		"an AWS access key":                 {"aws: AKIAIOSFODNN7EXAMPLE", "AWS access key"},
		"a temporary AWS access key":        {"ASIAY34FZKBOKMUTVV7A", "AWS access key"},
		"a GitHub token":                    {"token " + gitHubPAT, "GitHub token"},
		"a fine-grained GitHub token":       {"github_pat_11ABCDEFG0abcdefghijkl_mnopqrstuvwxyz", "GitHub token"},
		"a GitLab token":                    {"glpat-0123456789abcdefghij", "GitLab token"},
		"a Slack token":                     {"xoxb-123456789012-abcdefghijkl", "Slack token"},
		"a password in a database URL":      {"postgres://fabric:s3cretpw@db.example.org/fabric", "password in a URL"},
		"a password in a web URL":           {"https://carol:hunter2@fabric.example.org/v1", "password in a URL"},
		"a password in a URL inside a URL":  {"https://fabric.example.org/go?to=https://carol:hunter2@partner.example.org/", "password in a URL"},
		"a password with an escape in it":   {"https://carol:p%40ssw0rd@fabric.example.org/", "password in a URL"},
		"two kinds on a line":               {"client_secret: s3cr3t-0f-the-cl1ent # or " + gitHubPAT, "secret assigned to a name, GitHub token"},
		"a secret that is also in a URL":    {"api_key=0123456789abcdef https://u:p4ssw0rd@fabric.example.org", "secret assigned to a name, password in a URL"},
		"a placeholder beside a credential": {"Bearer DESIGN_EXAMPLE_0123456789abcdef or Bearer " + hexToken, "bearer token"},
	}
	for name, c := range found {
		t.Run(name, func(t *testing.T) {
			said := evaluate(t, file("openapi.yaml", noted(c.line)))
			if got := said.of("004"); !reflect.DeepEqual(got, []string{"openapi.yaml:13"}) || !said.decided("004") {
				t.Fatalf("the findings are %v, want the one of line 13", got)
			}
			message := said.messages("004")[0]
			if !strings.Contains(message, "("+c.kinds+")") {
				t.Errorf("the finding says %q, want it to name %q", message, c.kinds)
			}
			// No run of the line that could be part of a credential is in
			// the finding.
			for _, part := range strings.FieldsFunc(c.line, func(r rune) bool { return strings.ContainsRune(" :='\"#/@?", r) }) {
				if len(part) >= 8 && strings.Contains(message, part) {
					t.Errorf("the finding repeats %q of the line: %q", part, message)
				}
			}
		})
	}

	passed := map[string]string{
		"a token to fill in":                     "Authorization: Bearer <token>",
		"a variable to fill in":                  "Authorization: Bearer {access_token}",
		"a sentence about bearer tokens":         "Bearer authentication is required for every operation",
		"a word behind Bearer":                   "use Bearer token-based-authentication with every request",
		"a bearer token that says it is none":    "Authorization: Bearer DESIGN_EXAMPLE_0123456789abcdef",
		"a secret that says it is none":          "client_secret: DESIGN_EXAMPLE-s3cr3t-0f-the-cl1ent",
		"a secret described in words":            "client_secret: your-client-secret",
		"a bearer token too short to be one":     "Authorization: Bearer abc123def456",
		"a secret too short to be one":           "client_secret: abc123",
		"the format of a bearer token":           "bearerFormat: JWT",
		"the bearer scheme":                      "{type: http, scheme: bearer, bearerFormat: JWT}",
		"the kind of a security scheme":          "type: apiKey",
		"a security scheme":                      "ApiKeyAuth: {type: apiKey, in: header, name: X-API-Key}",
		"a security requirement":                 "security: [{api_key: []}]",
		"a property that is a password":          "password: {type: string, format: password, minLength: 12}",
		"a property that refers to a schema":     "client_secret: {$ref: '#/components/schemas/Secret1234'}",
		"the password flow":                      "flows: {password: {tokenUrl: https://identity.example.org/oauth2/token}}",
		"a token URL":                            "tokenUrl: https://identity.example.org/oauth2/token-2026",
		"a way to authenticate a client":         "token_endpoint_auth_method: private_key_jwt",
		"a reference to a secret":                "secret_key_ref: tracking-secret-2026",
		"a rule about passwords":                 "password_min_length: 123456789012",
		"a certificate":                          "-----BEGIN CERTIFICATE-----",
		"a public key":                           "-----BEGIN PUBLIC KEY-----",
		"the start of an AWS key":                "AKIA1234",
		"an AWS key inside a longer word":        "XAKIAIOSFODNN7EXAMPLEX",
		"one part of a JWT":                      "alg: eyJhbGciOiJIUzI1NiJ9",
		"a digest":                               "source_digest: sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		"a URL with a user and no password":      "https://carol@fabric.example.org/v1",
		"a URL with a password to fill in":       "https://carol:{password}@fabric.example.org/v1",
		"the pattern of a URL with a user":       `pattern: '^https://[^:]+:[^@]+@fabric\.example\.org/'`,
		"a URL with a port":                      "https://fabric.example.org:8443/v1",
		"a URL with a colon and an at behind it": "https://fabric.example.org/v1/mail?to=ops:team@example.org",
		"an address to write to":                 "contact: ops@example.org",
	}
	for name, line := range passed {
		t.Run(name, func(t *testing.T) {
			if got := evaluate(t, file("openapi.yaml", noted(line))).of("004"); got != nil {
				t.Fatalf("the findings are %v, want none", got)
			}
		})
	}

	// Every file is scanned, and every line of it once.
	said := evaluate(t,
		file("openapi.yaml", minimal+"x-a: {$ref: 'schemas/a.json'}\n"),
		file("schemas/a.json", "{\"example\": {\r\n  \"password\": \"hunter2hunter2\",\r\n  \"token\": \""+jwt+"\"}}"))
	if got := said.of("004"); !reflect.DeepEqual(got, []string{"schemas/a.json:2", "schemas/a.json:3"}) {
		t.Errorf("the findings in a referenced file are %v, want its lines 2 and 3", got)
	}
}

// F04-GOV-010: a line that names a URL of a host inside a private network is
// a finding: an address that is loopback, private or link-local, localhost,
// a name that resolves only inside such a network, and under a web scheme a
// name without a dot or a host that is an address written another way.
func TestALineThatNamesAHostOfAPrivateNetworkIsAFinding(t *testing.T) {
	found := map[string]string{
		"localhost":                        "url: http://localhost",
		"localhost with a port and a path": "url: http://localhost:8080/v1",
		"localhost in upper case":          "url: https://LOCALHOST/",
		"localhost with a dot behind it":   "url: http://localhost./v1",
		"a name under localhost":           "url: http://fabric.localhost:8080",
		"the loopback address":             "url: http://127.0.0.1:9000",
		"another loopback address":         "url: http://127.8.9.10/",
		"a 10 address":                     "url: http://10.1.2.3/",
		"the first 172 address":            "url: http://172.16.0.1/",
		"the last 172 address":             "url: http://172.31.255.254/",
		"a 192.168 address":                "url: https://192.168.1.1",
		"the metadata address":             "url: http://169.254.169.254/latest/meta-data/",
		"the unspecified address":          "url: http://0.0.0.0:8080",
		"the IPv6 loopback address":        "url: http://[::1]:8080/",
		"a unique local IPv6 address":      "url: http://[fd00::1]/",
		"a link-local IPv6 address":        "url: http://[fe80::1]/",
		"a private address mapped to IPv6": "url: http://[::ffff:10.0.0.1]/",
		"a name that ends in internal":     "url: https://api.shipments.internal/v1",
		"a name that ends in local":        "url: https://tracking.local",
		"a name of a cluster":              "url: http://fabric.default.svc.cluster.local:8080",
		"a name that ends in lan":          "url: http://printer.lan/",
		"a name that ends in corp":         "url: https://wiki.corp/x",
		"a name that ends in home.arpa":    "url: https://nas.home.arpa",
		"a name without a dot":             "url: http://fabric-api:8080/healthz",
		"a name without a dot or a port":   "url: https://fabric-api/",
		"an address as one number":         "url: http://2130706433/",
		"an address of two parts":          "url: http://127.1/",
		"an address in hexadecimal":        "url: http://0x7f000001/",
		"a web socket":                     "url: ws://localhost:1234",
		"a database on a private address":  "dsn: postgres://10.0.0.5:5432/fabric",
		"a cache on localhost":             "dsn: redis://localhost:6379",
		"a broker with an internal name":   "dsn: amqp://broker.internal",
		"a URL in a sentence":              "see the console (http://10.0.0.1).",
		"a URL in a flow mapping":          "servers: [{url: http://10.0.0.1}]",
		"a URL in JSON":                    `{"url":"http://10.0.0.1"}`,
		"a URL inside a URL":               "url: https://fabric.example.org/login?next=http://10.0.0.1/admin",
		"a URL with a user":                "url: http://carol@10.0.0.1/",
		"a public URL and a private one":   "urls: [https://fabric.example.org, http://10.0.0.1]",
		"a scheme in upper case":           "url: HTTP://10.0.0.1/",
	}
	for name, line := range found {
		t.Run(name, func(t *testing.T) {
			said := evaluate(t, file("openapi.yaml", noted(line)))
			if got := said.of("010"); !reflect.DeepEqual(got, []string{"openapi.yaml:13"}) || !said.decided("010") {
				t.Fatalf("the findings are %v, want the one of line 13", got)
			}
			// The finding does not repeat the address.
			if message := said.messages("010")[0]; strings.Contains(message, "://") || strings.Contains(message, "10.0.0") {
				t.Errorf("the finding repeats the address: %q", message)
			}
		})
	}

	passed := map[string]string{
		"a public name":                         "url: https://fabric.example.org",
		"a public name with a port and a path":  "url: https://identity.example.org:8443/oauth2/token",
		"a public address":                      "url: http://8.8.8.8/",
		"the address behind the 172 range":      "url: http://172.32.0.1/",
		"the address before the 172 range":      "url: http://172.15.255.255/",
		"the address behind the 10 range":       "url: http://11.0.0.1/",
		"an address beside the 192.168 range":   "url: http://192.169.1.1/",
		"a public IPv6 address":                 "url: http://[2001:db8::1]/",
		"a host to fill in":                     "url: https://{tenant}.example.org/v1",
		"a host that is a variable":             "url: http://{host}:8080/",
		"a host from the environment":           "url: https://$FABRIC_HOST/v1",
		"the pattern of a URL":                  `pattern: '^https://[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]+)*(:[0-9]+)?$'`,
		"the pattern of a local URL":            `pattern: '^http://localhost(:[0-9]+)?$'`,
		"brackets that hold no address":         "url: http://[internal]/",
		"a host in another script":              "url: http://内部/",
		"the address of a mobile application":   "redirect_uri: myapp://callback",
		"a reverse name of an application":      "redirect_uri: com.example.app://oauth",
		"a number under a scheme of its own":    "link: myapp://123",
		"a name without a dot, not on the web":  "dsn: redis://cache:6379",
		"a name, not a URL":                     "urn:ietf:params:oauth:grant-type:jwt-bearer",
		"an address to write to":                "mailto:ops@example.org",
		"a private address that is no URL":      "the gateway is 10.0.0.1",
		"localhost that is no URL":              "listen on localhost:8080",
		"a public name that begins like one":    "url: https://localhost.example.org/",
		"a public name with internal in it":     "url: https://internal.example.org/",
		"a public name that ends like a suffix": "url: https://example.notlocal/",
		"a path that looks like a host":         "url: https://fabric.example.org/10.0.0.1/",
		"a public database":                     "dsn: postgres://db.example.org/fabric",
		"a relative URL":                        "url: /v1",
	}
	for name, line := range passed {
		t.Run(name, func(t *testing.T) {
			if got := evaluate(t, file("openapi.yaml", noted(line))).of("010"); got != nil {
				t.Fatalf("the findings are %v, want none", got)
			}
		})
	}
}
