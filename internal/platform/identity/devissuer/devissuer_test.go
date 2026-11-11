package devissuer

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Generating a key is slow under the race detector, so the tests share two.
var (
	firstKey  = sync.OnceValue(mustGenerateKey)
	secondKey = sync.OnceValue(mustGenerateKey)
)

func mustGenerateKey() *rsa.PrivateKey {
	key, err := GenerateKey()
	if err != nil {
		panic(err)
	}
	return key
}

const issuerURL = "http://127.0.0.1:9450"

func newIssuer(t *testing.T, key *rsa.PrivateKey) *Issuer {
	t.Helper()

	issuer, err := New(issuerURL, key)
	if err != nil {
		t.Fatalf("build the issuer: %v", err)
	}
	return issuer
}

// segments decodes the header and the payload of a token.
func segments(t *testing.T, token string) (header, payload map[string]any) {
	t.Helper()

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("the token has %d segments, want 3", len(parts))
	}
	for index, target := range []*map[string]any{&header, &payload} {
		decoded, err := base64.RawURLEncoding.DecodeString(parts[index])
		if err != nil {
			t.Fatalf("decode segment %d: %v", index, err)
		}
		if err := json.Unmarshal(decoded, target); err != nil {
			t.Fatalf("segment %d is not a JSON object: %v", index, err)
		}
	}
	return header, payload
}

func TestAKeySurvivesItsPEMEncoding(t *testing.T) {
	encoded, err := EncodeKeyPEM(firstKey())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeKeyPEM(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded.Equal(firstKey()) {
		t.Fatal("the decoded key is not the key that was encoded")
	}
}

func TestOnlyALongEnoughRSAKeyIsDecoded(t *testing.T) {
	short, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate a short key: %v", err)
	}
	shortPEM, err := EncodeKeyPEM(short)
	if err != nil {
		t.Fatalf("encode the short key: %v", err)
	}
	elliptic, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate an elliptic key: %v", err)
	}
	ellipticDER, err := x509.MarshalPKCS8PrivateKey(elliptic)
	if err != nil {
		t.Fatalf("encode the elliptic key: %v", err)
	}

	cases := map[string][]byte{
		"not PEM":          []byte("not a key"),
		"another PEM type": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(firstKey())}),
		"not a key":        pem.EncodeToMemory(&pem.Block{Type: pemType, Bytes: []byte("garbage")}),
		"not an RSA key":   pem.EncodeToMemory(&pem.Block{Type: pemType, Bytes: ellipticDER}),
		"a short key":      shortPEM,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeKeyPEM(data); err == nil {
				t.Fatal("the key was decoded, want a refusal")
			}
		})
	}
}

func TestTheIssuerIsASchemeAndAHost(t *testing.T) {
	refused := []string{
		"", "127.0.0.1:9450", "ftp://127.0.0.1", "http://", "http://127.0.0.1:9450/",
		"http://127.0.0.1:9450/realm", "http://127.0.0.1:9450?x=1", "http://127.0.0.1:9450#x",
		"http://user@127.0.0.1:9450", "http://127.0.0.1:9450 ",
	}
	for _, value := range refused {
		if _, err := New(value, firstKey()); err == nil {
			t.Errorf("issuer %q was accepted, want a refusal", value)
		}
	}
	for _, value := range []string{"http://127.0.0.1:9450", "https://issuer.example"} {
		if _, err := New(value, firstKey()); err != nil {
			t.Errorf("issuer %q was refused: %v", value, err)
		}
	}
	if _, err := New(issuerURL, nil); err == nil {
		t.Error("an issuer without a key was built")
	}
}

func TestTheKeyIdentifierFollowsTheKey(t *testing.T) {
	first, again, second := newIssuer(t, firstKey()), newIssuer(t, firstKey()), newIssuer(t, secondKey())

	if first.KeyID() == "" {
		t.Fatal("the key has no identifier")
	}
	if first.KeyID() != again.KeyID() {
		t.Errorf("the same key got two identifiers: %q and %q", first.KeyID(), again.KeyID())
	}
	if first.KeyID() == second.KeyID() {
		t.Errorf("two keys share the identifier %q", first.KeyID())
	}
}

func TestTheHandlerServesTheKeySetAndNothingElse(t *testing.T) {
	issuer := newIssuer(t, firstKey())

	recorder := httptest.NewRecorder()
	issuer.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, JWKSPath, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", JWKSPath, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type %q, want application/json", got)
	}
	var document struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatalf("the key set is not JSON: %v", err)
	}
	if len(document.Keys) != 1 {
		t.Fatalf("the key set holds %d keys, want 1", len(document.Keys))
	}
	published := document.Keys[0]
	if published["kid"] != issuer.KeyID() || published["kty"] != "RSA" || published["alg"] != "RS256" || published["use"] != "sig" {
		t.Errorf("published key %v does not describe the RS256 signing key %q", published, issuer.KeyID())
	}
	for _, member := range []string{"d", "p", "q", "dp", "dq", "qi"} {
		if _, private := published[member]; private {
			t.Errorf("the key set publishes the private member %q", member)
		}
	}
	if got, want := issuer.JWKSURL(), issuerURL+JWKSPath; got != want {
		t.Errorf("JWKS URL %q, want %q", got, want)
	}

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/", nil),
		httptest.NewRequest(http.MethodGet, "/token", nil),
		httptest.NewRequest(http.MethodPost, JWKSPath, nil),
	} {
		recorder := httptest.NewRecorder()
		issuer.Handler().ServeHTTP(recorder, request)
		if recorder.Code == http.StatusOK {
			t.Errorf("%s %s was served, want a refusal", request.Method, request.URL.Path)
		}
	}
}

func TestAMintedTokenSaysWhatWasRequested(t *testing.T) {
	issuer := newIssuer(t, firstKey())
	issuedAt := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

	token, err := issuer.Mint(TokenRequest{
		Subject: "alice", Audience: []string{"fabric-api"}, IssuedAt: issuedAt, TTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	header, payload := segments(t, token)

	if header["alg"] != "RS256" || header["kid"] != issuer.KeyID() || header["typ"] != "JWT" {
		t.Errorf("header %v, want RS256, the key %q and the type JWT", header, issuer.KeyID())
	}
	want := map[string]any{
		"iss": issuerURL,
		"sub": "alice",
		"iat": float64(issuedAt.Unix()),
		"nbf": float64(issuedAt.Unix()),
		"exp": float64(issuedAt.Add(time.Hour).Unix()),
	}
	for name, value := range want {
		if payload[name] != value {
			t.Errorf("claim %s is %v, want %v", name, payload[name], value)
		}
	}
	if audience, _ := payload["aud"].([]any); len(audience) != 1 || audience[0] != "fabric-api" {
		t.Errorf("claim aud is %v, want [fabric-api]", payload["aud"])
	}
	if identifier, _ := payload["jti"].(string); len(identifier) != 32 {
		t.Errorf("claim jti is %v, want 32 hexadecimal digits", payload["jti"])
	}

	another, err := issuer.Mint(TokenRequest{Subject: "alice", Audience: []string{"fabric-api"}, IssuedAt: issuedAt})
	if err != nil {
		t.Fatalf("mint again: %v", err)
	}
	_, anotherPayload := segments(t, another)
	if anotherPayload["jti"] == payload["jti"] {
		t.Error("two tokens share an identifier")
	}
	if got, want := anotherPayload["exp"], float64(issuedAt.Add(DefaultTTL).Unix()); got != want {
		t.Errorf("without a lifetime the expiry is %v, want %v", got, want)
	}
}

func TestMintRefusesARequestThatSaysTooLittle(t *testing.T) {
	issuer := newIssuer(t, firstKey())

	cases := map[string]TokenRequest{
		"no subject":          {Audience: []string{"fabric-api"}},
		"no audience":         {Subject: "alice"},
		"a negative lifetime": {Subject: "alice", Audience: []string{"fabric-api"}, TTL: -time.Second},
	}
	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			if token, err := issuer.Mint(request); err == nil {
				t.Fatalf("a token was minted: %s", token)
			}
		})
	}
}
