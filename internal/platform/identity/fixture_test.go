package identity

import (
	"context"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/identity/devissuer"
)

// Generating a key is slow under the race detector, so the tests share two:
// the key the issuer publishes and a key it does not.
var (
	publishedKey = sync.OnceValue(mustGenerateKey)
	otherKey     = sync.OnceValue(mustGenerateKey)
)

func mustGenerateKey() *rsa.PrivateKey {
	key, err := devissuer.GenerateKey()
	if err != nil {
		panic(err)
	}
	return key
}

// audience is how the deployment under test is addressed.
const audience = "fabric-api"

// fakeClock is a clock a test moves by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(by)
}

// keyEndpoint is the JWKS endpoint of the test issuer. It counts the fetches
// and lets a test replace what is served.
type keyEndpoint struct {
	mu      sync.Mutex
	handler http.Handler
	fetches int
}

func (e *keyEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	e.fetches++
	handler := e.handler
	e.mu.Unlock()
	handler.ServeHTTP(w, r)
}

func (e *keyEndpoint) serve(handler http.Handler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handler = handler
}

func (e *keyEndpoint) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.fetches
}

// fixture is a development issuer on a loopback address and a verifier bound
// to it, on a clock the test controls.
type fixture struct {
	issuer   *devissuer.Issuer
	endpoint *keyEndpoint
	clock    *fakeClock
	verifier *Verifier
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	f := &fixture{
		endpoint: &keyEndpoint{handler: http.NotFoundHandler()},
		clock:    &fakeClock{now: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)},
	}
	server := httptest.NewServer(f.endpoint)
	t.Cleanup(server.Close)

	f.issuer = f.issuerWith(t, server.URL, publishedKey())
	f.endpoint.serve(f.issuer.Handler())

	// No HTTP client is given, so the tests exercise the default one.
	verifier, err := NewVerifier(Config{
		Issuer: f.issuer.URL(), Audience: audience, JWKSURL: f.issuer.JWKSURL(), Now: f.clock.Now,
	})
	if err != nil {
		t.Fatalf("build the verifier: %v", err)
	}
	f.verifier = verifier
	return f
}

func (f *fixture) issuerWith(t *testing.T, url string, key *rsa.PrivateKey) *devissuer.Issuer {
	t.Helper()

	issuer, err := devissuer.New(url, key)
	if err != nil {
		t.Fatalf("build the issuer: %v", err)
	}
	return issuer
}

// rotated is the same issuer after it replaced its key.
func (f *fixture) rotated(t *testing.T) *devissuer.Issuer {
	t.Helper()
	return f.issuerWith(t, f.issuer.URL(), otherKey())
}

// claims are claims the verifier accepts. A test changes what it is about.
func (f *fixture) claims() map[string]any {
	now := f.clock.Now()
	return map[string]any{
		"iss": f.issuer.URL(),
		"sub": "alice",
		"aud": audience,
		"iat": now.Unix(),
		"nbf": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
	}
}

// header is the header the issuer writes. A test changes what it is about.
func (f *fixture) header() map[string]any {
	return map[string]any{"alg": "RS256", "kid": f.issuer.KeyID(), "typ": "JWT"}
}

func sign(t *testing.T, issuer *devissuer.Issuer, header, claims map[string]any) string {
	t.Helper()

	token, err := issuer.Sign(header, claims)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return token
}

// token is a token of subject that outlives every clock movement of a test.
func (f *fixture) token(t *testing.T, issuer *devissuer.Issuer, subject string) string {
	t.Helper()

	token, err := issuer.Mint(devissuer.TokenRequest{
		Subject: subject, Audience: []string{audience}, IssuedAt: f.clock.Now(), TTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return token
}

func (f *fixture) verify(token string) (Principal, error) {
	return f.verifier.Verify(context.Background(), token)
}

// waitIdle waits for the fetch in progress, if any, to end.
func waitIdle(t *testing.T, keys *KeySet) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		keys.mu.Lock()
		idle := keys.inFlight == nil
		keys.mu.Unlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the key set fetch did not end")
		}
		time.Sleep(time.Millisecond)
	}
}
