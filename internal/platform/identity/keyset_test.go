package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// failing is a JWKS endpoint that is down.
var failing = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "down", http.StatusServiceUnavailable)
})

// publishedJWK is the entry the issuer of the fixture publishes.
func (f *fixture) publishedJWK(t *testing.T) map[string]any {
	t.Helper()

	recorder := httptest.NewRecorder()
	f.issuer.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, f.issuer.JWKSURL(), nil))
	var document struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil || len(document.Keys) != 1 {
		t.Fatalf("read the published key: %v (%d keys)", err, len(document.Keys))
	}
	return document.Keys[0]
}

// keySetOf serves a key set document with the given entries.
func keySetOf(t *testing.T, entries ...map[string]any) http.Handler {
	t.Helper()

	document, err := json.Marshal(map[string]any{"keys": entries})
	if err != nil {
		t.Fatalf("encode the key set: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(document)
	})
}

func TestKeysAreFetchedOnceAndThenServedFromTheCache(t *testing.T) {
	f := newFixture(t)

	for range 5 {
		if _, err := f.verify(f.token(t, f.issuer, "alice")); err != nil {
			t.Fatalf("verify: %v", err)
		}
	}
	if fetches := f.endpoint.count(); fetches != 1 {
		t.Errorf("%d fetches for five tokens of one key, want 1", fetches)
	}
}

func TestARotatedKeyIsPickedUpWithoutARestart(t *testing.T) {
	f := newFixture(t)
	before := f.token(t, f.issuer, "alice")
	if _, err := f.verify(before); err != nil {
		t.Fatalf("verify before the rotation: %v", err)
	}

	rotated := f.rotated(t)
	f.endpoint.serve(rotated.Handler())
	f.clock.Advance(keySetMinRefresh)

	if _, err := f.verify(f.token(t, rotated, "alice")); err != nil {
		t.Fatalf("verify a token of the new key: %v", err)
	}
	// The issuer no longer publishes the old key, so it is no longer trusted.
	if _, err := f.verify(before); !errors.Is(err, ErrKeyUnknown) {
		t.Fatalf("a token of the withdrawn key: got %v, want an unknown key", err)
	}
	if fetches := f.endpoint.count(); fetches != 2 {
		t.Errorf("%d fetches, want 2", fetches)
	}
}

func TestMadeUpKeyIdentifiersDoNotCostAFetchEach(t *testing.T) {
	f := newFixture(t)
	if _, err := f.verify(f.token(t, f.issuer, "alice")); err != nil {
		t.Fatalf("verify: %v", err)
	}
	unpublished := f.token(t, f.rotated(t), "mallory")

	for range 5 {
		if _, err := f.verify(unpublished); !errors.Is(err, ErrKeyUnknown) {
			t.Fatalf("got %v, want an unknown key", err)
		}
	}
	if fetches := f.endpoint.count(); fetches != 1 {
		t.Fatalf("%d fetches inside the refresh interval, want 1", fetches)
	}

	f.clock.Advance(keySetMinRefresh)
	if _, err := f.verify(unpublished); !errors.Is(err, ErrKeyUnknown) {
		t.Fatalf("got %v, want an unknown key", err)
	}
	if fetches := f.endpoint.count(); fetches != 2 {
		t.Errorf("%d fetches after the refresh interval, want 2", fetches)
	}
}

func TestAnIssuerThatIsDownMakesTheKeysUnavailableNotTheTokenBad(t *testing.T) {
	f := newFixture(t)
	f.endpoint.serve(failing)
	token := f.token(t, f.issuer, "alice")

	_, err := f.verify(token)
	if !errors.Is(err, ErrKeySetUnavailable) {
		t.Fatalf("got %v, want the key set unavailable", err)
	}
	for _, verdict := range []error{ErrKeyUnknown, ErrTokenMalformed, ErrSignatureInvalid, ErrClaimsRejected} {
		if errors.Is(err, verdict) {
			t.Errorf("an outage of the issuer was reported as a verdict on the token: %v", err)
		}
	}

	// The failure is remembered: asking again does not fetch again.
	if _, err := f.verify(token); !errors.Is(err, ErrKeySetUnavailable) {
		t.Fatalf("got %v, want the key set unavailable", err)
	}
	if fetches := f.endpoint.count(); fetches != 1 {
		t.Fatalf("%d fetches inside the retry interval, want 1", fetches)
	}

	// Once the issuer is back, the retry interval is all it takes.
	f.endpoint.serve(f.issuer.Handler())
	f.clock.Advance(keySetRetryAfterFailure)
	if _, err := f.verify(token); err != nil {
		t.Fatalf("verify after the issuer came back: %v", err)
	}
}

func TestAnOldKeySetAnswersWhileItIsRefreshedBehind(t *testing.T) {
	f := newFixture(t)
	before := f.token(t, f.issuer, "alice")
	if _, err := f.verify(before); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// The issuer withdraws the key. Nothing tells this process until the
	// cached set is old enough to be fetched again.
	f.endpoint.serve(f.rotated(t).Handler())
	f.clock.Advance(keySetRefreshAfter - time.Second)
	if _, err := f.verify(before); err != nil {
		t.Fatalf("verify just before the set is due: %v", err)
	}
	if fetches := f.endpoint.count(); fetches != 1 {
		t.Fatalf("%d fetches before the set is due, want 1", fetches)
	}

	// The lookup that finds the set due is still answered from it.
	f.clock.Advance(time.Second)
	if _, err := f.verify(before); err != nil {
		t.Fatalf("verify when the set is due: %v", err)
	}
	waitIdle(t, f.verifier.keys)
	if fetches := f.endpoint.count(); fetches != 2 {
		t.Fatalf("%d fetches once the set is due, want 2", fetches)
	}
	// The refresh replaced the set, and the withdrawn key went with it.
	if _, err := f.verify(before); !errors.Is(err, ErrKeyUnknown) {
		t.Fatalf("a token of the withdrawn key after the refresh: got %v, want an unknown key", err)
	}
}

func TestAKeySetThatCannotBeRefreshedExpires(t *testing.T) {
	f := newFixture(t)
	token := f.token(t, f.issuer, "alice")
	if _, err := f.verify(token); err != nil {
		t.Fatalf("verify: %v", err)
	}
	f.endpoint.serve(failing)

	// A short outage does not refuse callers whose key is cached.
	f.clock.Advance(keySetRefreshAfter)
	if _, err := f.verify(token); err != nil {
		t.Fatalf("verify during the outage: %v", err)
	}
	waitIdle(t, f.verifier.keys)
	f.clock.Advance(keySetExpireAfter - keySetRefreshAfter - time.Second)
	if _, err := f.verify(token); err != nil {
		t.Fatalf("verify just before the set expires: %v", err)
	}
	waitIdle(t, f.verifier.keys)

	// An outage longer than the limit does: the keys are not trusted on the
	// word of a fetch that old.
	f.clock.Advance(time.Second)
	if _, err := f.verify(token); !errors.Is(err, ErrKeySetUnavailable) {
		t.Fatalf("verify with an expired set: got %v, want the key set unavailable", err)
	}

	f.endpoint.serve(f.issuer.Handler())
	f.clock.Advance(keySetRetryAfterFailure)
	if _, err := f.verify(token); err != nil {
		t.Fatalf("verify after the issuer came back: %v", err)
	}
}

func TestLookupsThatArriveTogetherShareOneFetch(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	published := f.issuer.Handler()
	f.endpoint.serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		published.ServeHTTP(w, r)
	}))
	token := f.token(t, f.issuer, "alice")

	const callers = 8
	results := make(chan error, callers)
	var started sync.WaitGroup
	for range callers {
		started.Add(1)
		go func() {
			started.Done()
			_, err := f.verify(token)
			results <- err
		}()
	}
	started.Wait()
	close(release)

	for range callers {
		if err := <-results; err != nil {
			t.Errorf("verify: %v", err)
		}
	}
	if fetches := f.endpoint.count(); fetches != 1 {
		t.Errorf("%d fetches for %d lookups that arrived together, want 1", fetches, callers)
	}
}

func TestACallerThatGivesUpDoesNotEndTheFetch(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	published := f.issuer.Handler()
	f.endpoint.serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		published.ServeHTTP(w, r)
	}))
	token := f.token(t, f.issuer, "alice")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.verifier.Verify(ctx, token)
	if !errors.Is(err, ErrKeySetUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the key set unavailable because the caller gave up", err)
	}

	// The fetch the caller started went on without it and serves the next.
	close(release)
	waitIdle(t, f.verifier.keys)
	if _, err := f.verify(token); err != nil {
		t.Fatalf("verify after the fetch ended: %v", err)
	}
	if fetches := f.endpoint.count(); fetches != 1 {
		t.Errorf("%d fetches, want 1", fetches)
	}
}

func TestAKeySetWithoutAUsableKeyIsUnavailable(t *testing.T) {
	// A 1024 bit modulus: long enough to look like a key, too short to be
	// trusted as one.
	shortModulus := make([]byte, 128)
	if _, err := rand.Read(shortModulus); err != nil {
		t.Fatalf("random bytes: %v", err)
	}
	shortModulus[0] |= 0x80
	encode := base64.RawURLEncoding.EncodeToString

	// Each case changes the one published key so that it can no longer be
	// used, or replaces the document altogether.
	entries := map[string]func(key map[string]any){
		"an encryption key":            func(k map[string]any) { k["use"] = "enc" },
		"a key for another algorithm":  func(k map[string]any) { k["alg"] = "RS512" },
		"a key of another type":        func(k map[string]any) { k["kty"] = "EC" },
		"a key without an identifier":  func(k map[string]any) { delete(k, "kid") },
		"a short modulus":              func(k map[string]any) { k["n"] = encode(shortModulus) },
		"a modulus that is not base64": func(k map[string]any) { k["n"] = "!!!" },
		"no modulus":                   func(k map[string]any) { delete(k, "n") },
		"an even exponent":             func(k map[string]any) { k["e"] = encode([]byte{0x01, 0x00, 0x00}) },
		"an exponent of one":           func(k map[string]any) { k["e"] = encode([]byte{0x01}) },
		"an exponent beyond an int":    func(k map[string]any) { k["e"] = encode([]byte{0x01, 0x00, 0x00, 0x00, 0x01}) },
		"no exponent":                  func(k map[string]any) { delete(k, "e") },
	}
	for name, change := range entries {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			key := f.publishedJWK(t)
			change(key)
			f.endpoint.serve(keySetOf(t, key))

			if _, err := f.verify(f.token(t, f.issuer, "alice")); !errors.Is(err, ErrKeySetUnavailable) {
				t.Fatalf("got %v, want the key set unavailable", err)
			}
		})
	}

	documents := map[string]http.Handler{
		"not JSON":  http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }),
		"no keys":   keySetOf(t),
		"not found": http.NotFoundHandler(),
		"larger than the limit": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"keys":[],"padding":"` + strings.Repeat("a", keySetMaxBytes) + `"}`))
		}),
	}
	for name, handler := range documents {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.endpoint.serve(handler)

			if _, err := f.verify(f.token(t, f.issuer, "alice")); !errors.Is(err, ErrKeySetUnavailable) {
				t.Fatalf("got %v, want the key set unavailable", err)
			}
		})
	}
}

func TestEntriesThatCannotBeUsedDoNotSpoilTheOnesThatCan(t *testing.T) {
	f := newFixture(t)
	encryption := f.publishedJWK(t)
	encryption["use"], encryption["kid"] = "enc", "encryption-key"
	elliptic := map[string]any{"kty": "EC", "kid": "elliptic-key", "crv": "P-256", "x": "AA", "y": "AA"}
	f.endpoint.serve(keySetOf(t, encryption, elliptic, f.publishedJWK(t)))

	if _, err := f.verify(f.token(t, f.issuer, "alice")); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestARedirectIsNotFollowed(t *testing.T) {
	f := newFixture(t)
	elsewhere := httptest.NewServer(f.issuer.Handler())
	t.Cleanup(elsewhere.Close)
	f.endpoint.serve(http.RedirectHandler(elsewhere.URL+"/.well-known/jwks.json", http.StatusFound))

	// The keys behind the redirect are the right ones. They are still not
	// taken: keys come from the configured URL or from nowhere.
	if _, err := f.verify(f.token(t, f.issuer, "alice")); !errors.Is(err, ErrKeySetUnavailable) {
		t.Fatalf("got %v, want the key set unavailable", err)
	}
}

func TestAKeySetNeedsAURLAClientAndAClock(t *testing.T) {
	const url = "https://issuer.example/jwks"
	client, clock := &http.Client{}, time.Now

	if _, err := NewKeySet(url, client, clock); err != nil {
		t.Fatalf("a complete key set was refused: %v", err)
	}
	if _, err := NewKeySet("", client, clock); err == nil {
		t.Error("a key set without a URL was built")
	}
	if _, err := NewKeySet(url, nil, clock); err == nil {
		t.Error("a key set without a client was built")
	}
	if _, err := NewKeySet(url, client, nil); err == nil {
		t.Error("a key set without a clock was built")
	}

	keys, err := NewKeySet(url, client, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Key(context.Background(), ""); !errors.Is(err, ErrKeyUnknown) {
		t.Errorf("an empty key identifier: got %v, want an unknown key", err)
	}
}
