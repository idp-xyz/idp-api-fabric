package identity

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"
)

var (
	// ErrKeyUnknown reports a token signed with a key the issuer does not
	// publish. It never degrades into "assume valid": without the key there
	// is nothing this process can verify the token with.
	ErrKeyUnknown = errors.New("identity: the issuer publishes no key with the key identifier of the token")

	// ErrKeySetUnavailable reports that the keys of the issuer could not be
	// obtained, or could not be refreshed for too long. No token can be
	// verified then. It says nothing about the token: the caller may hold a
	// perfectly good one.
	ErrKeySetUnavailable = errors.New("identity: the key set of the issuer is unavailable")
)

const (
	// keySetMaxBytes bounds the key set document. A key set is a few
	// kilobytes; anything larger is a misconfigured or hostile endpoint.
	keySetMaxBytes = 256 << 10

	// keySetMinRefresh is the shortest time between a fetch that succeeded
	// and the next one. Without it a stream of tokens with made-up key
	// identifiers would turn every request into a fetch, paid for by the
	// issuer.
	keySetMinRefresh = 30 * time.Second

	// keySetRetryAfterFailure is the shortest time between a fetch that
	// failed and the next one. It is shorter because until a fetch succeeds
	// every token whose key is not cached is refused, and after a cold start
	// that is every token.
	keySetRetryAfterFailure = 5 * time.Second

	// keySetFetchTimeout bounds one fetch whatever the HTTP client is
	// configured with. A fetch runs detached from the request that started
	// it, so nothing else would end it.
	keySetFetchTimeout = 30 * time.Second

	// keySetRefreshAfter is the age at which a cached key set is fetched
	// again even though it holds the key asked for. A key the issuer has
	// withdrawn is therefore trusted for at most this long while the issuer
	// can be reached (F05 §13).
	keySetRefreshAfter = 10 * time.Minute

	// keySetExpireAfter is the age at which a cached key set is no longer
	// trusted at all. Between the two ages a key set that cannot be refreshed
	// keeps answering, so that a short outage of the issuer does not refuse
	// every caller; past this age it does.
	keySetExpireAfter = time.Hour

	// keyMinModulusBits refuses keys too short to prove anything. A signature
	// made with a short key verifies like any other.
	keyMinModulusBits = 2048
)

// never is the age of a key set that was never fetched.
const never = time.Duration(math.MaxInt64)

// KeySet resolves the signature verification keys of one issuer. It caches
// them by key identifier and fetches again when a token names an identifier
// it has not seen, which is what makes a key rotation work without a restart.
type KeySet struct {
	url    string
	client *http.Client
	now    func() time.Time

	mu   sync.Mutex
	keys map[string]*rsa.PublicKey
	// fetchedAt is when the cached keys were fetched. It is zero until a
	// fetch succeeds.
	fetchedAt time.Time
	// lastAttempt is when the last fetch ended and lastErr how it ended. A
	// failed fetch is remembered like a successful one, so the interval
	// between fetches also holds for an issuer that is failing.
	lastAttempt time.Time
	lastErr     error
	// inFlight is the fetch in progress, if any. A lookup that finds one
	// waits for it instead of starting another, so a burst of requests after
	// a rotation costs the issuer one fetch.
	inFlight *keySetFetch
}

// keySetFetch is one fetch in progress. Its result is written before done is
// closed and read by every lookup that waited.
type keySetFetch struct {
	done chan struct{}
	err  error
}

// NewKeySet builds the key set of the JWKS endpoint at jwksURL. It fetches
// nothing: the first token that needs a key starts the first fetch, so an
// issuer that cannot be reached for a moment delays authentication and does
// not keep the process from starting. A URL no fetch could use does.
func NewKeySet(jwksURL string, client *http.Client, now func() time.Time) (*KeySet, error) {
	if err := checkKeySetURL(jwksURL); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("identity: an HTTP client is required")
	}
	if now == nil {
		return nil, errors.New("identity: a clock is required")
	}
	return &KeySet{url: jwksURL, client: client, now: now, keys: map[string]*rsa.PublicKey{}}, nil
}

// Key returns the verification key with the identifier keyID.
//
// A key of a recently fetched set is returned from the cache. A key of an
// older set is returned as well, while a fetch replaces the set behind that
// answer. An identifier the cache does not hold waits for a fetch, at most as
// long as ctx allows; the fetch itself does not end with ctx.
func (k *KeySet) Key(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	if keyID == "" {
		return nil, ErrKeyUnknown
	}

	k.mu.Lock()
	key, age := k.keys[keyID], k.ageLocked()
	if key != nil && age < keySetRefreshAfter {
		k.mu.Unlock()
		return key, nil
	}
	// The key is not cached, or the set is due for a refresh.
	fetch := k.startFetchLocked(ctx)
	if key != nil && age < keySetExpireAfter {
		k.mu.Unlock()
		return key, nil
	}
	lastErr := k.lastErr
	k.mu.Unlock()

	if fetch == nil {
		// It is too soon after the last fetch to start another one, so that
		// fetch answers. One that failed says the keys are unavailable; one
		// that succeeded did not contain this key.
		if lastErr != nil {
			return nil, fmt.Errorf("%w: the last fetch failed and it is too soon to retry: %w", ErrKeySetUnavailable, lastErr)
		}
		return nil, ErrKeyUnknown
	}

	select {
	case <-fetch.done:
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: waiting for the fetch: %w", ErrKeySetUnavailable, ctx.Err())
	}
	if fetch.err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeySetUnavailable, fetch.err)
	}

	k.mu.Lock()
	key = k.keys[keyID]
	k.mu.Unlock()
	if key == nil {
		return nil, ErrKeyUnknown
	}
	return key, nil
}

// ageLocked is how long ago the cached keys were fetched.
func (k *KeySet) ageLocked() time.Duration {
	if k.fetchedAt.IsZero() {
		return never
	}
	return k.now().Sub(k.fetchedAt)
}

// startFetchLocked returns the fetch in progress, starts one if the interval
// since the last one allows it, and returns nil otherwise.
//
// The fetch runs detached from the request that started it. A client that
// disconnects half way would otherwise cancel a fetch other requests are
// waiting for, record nothing and let the next request start another: the
// interval would be something any caller could defeat.
func (k *KeySet) startFetchLocked(ctx context.Context) *keySetFetch {
	if k.inFlight != nil {
		return k.inFlight
	}
	interval := keySetMinRefresh
	if k.lastErr != nil {
		interval = keySetRetryAfterFailure
	}
	if !k.lastAttempt.IsZero() && k.now().Sub(k.lastAttempt) < interval {
		return nil
	}
	k.inFlight = &keySetFetch{done: make(chan struct{})}
	go k.refresh(context.WithoutCancel(ctx), k.inFlight)
	return k.inFlight
}

// refresh runs one fetch and records how it ended, whichever way it ended.
// Only a fetch that succeeded replaces the cached keys.
func (k *KeySet) refresh(ctx context.Context, fetch *keySetFetch) {
	ctx, cancel := context.WithTimeout(ctx, keySetFetchTimeout)
	defer cancel()
	keys, err := k.fetch(ctx)

	k.mu.Lock()
	now := k.now()
	if err == nil {
		k.keys, k.fetchedAt = keys, now
	}
	k.lastAttempt, k.lastErr, k.inFlight = now, err, nil
	k.mu.Unlock()

	fetch.err = err
	close(fetch.done)
}

func (k *KeySet) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return nil, fmt.Errorf("build the key set request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := k.client.Do(request)
	if err != nil {
		// A url.Error repeats the URL; the cause is what an operator needs.
		var requestErr *url.Error
		if errors.As(err, &requestErr) {
			err = requestErr.Err
		}
		return nil, fmt.Errorf("fetch the key set: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the key set endpoint answered %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, keySetMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the key set: %w", err)
	}
	if len(body) > keySetMaxBytes {
		return nil, fmt.Errorf("the key set is larger than %d bytes", keySetMaxBytes)
	}

	var document struct {
		Keys []jsonWebKey `json:"keys"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("decode the key set: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(document.Keys))
	for _, candidate := range document.Keys {
		key, usable := candidate.verificationKey()
		if !usable {
			continue
		}
		// An identifier is expected once. Should it appear twice, the first
		// entry stands.
		if _, seen := keys[candidate.KeyID]; !seen {
			keys[candidate.KeyID] = key
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("the key set holds no usable RS256 verification key")
	}
	return keys, nil
}

type jsonWebKey struct {
	KeyType   string `json:"kty"`
	KeyID     string `json:"kid"`
	Algorithm string `json:"alg"`
	Use       string `json:"use"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
}

// verificationKey converts one entry of a key set and reports whether it can
// verify an RS256 signature. An entry that cannot is skipped rather than
// failing the document: a key set legitimately lists encryption keys and
// algorithms this build does not know beside the keys it uses.
func (j jsonWebKey) verificationKey() (*rsa.PublicKey, bool) {
	if j.KeyType != "RSA" || j.KeyID == "" {
		return nil, false
	}
	if j.Use != "" && j.Use != "sig" {
		return nil, false
	}
	if j.Algorithm != "" && j.Algorithm != algorithmRS256 {
		return nil, false
	}

	modulusBytes, err := base64.RawURLEncoding.DecodeString(j.Modulus)
	if err != nil || len(modulusBytes) == 0 {
		return nil, false
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(j.Exponent)
	if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 8 {
		return nil, false
	}

	var exponent uint64
	for _, b := range exponentBytes {
		exponent = exponent<<8 | uint64(b)
	}
	// The exponent is odd and fits an int on every supported platform, which
	// is how crypto/rsa stores it.
	if exponent < 3 || exponent > math.MaxInt32 || exponent%2 == 0 {
		return nil, false
	}

	modulus := new(big.Int).SetBytes(modulusBytes)
	if modulus.BitLen() < keyMinModulusBits {
		return nil, false
	}
	return &rsa.PublicKey{N: modulus, E: int(exponent)}, true
}
