// Package devissuer is a token issuer for development machines and tests. It
// holds an RSA key, publishes the public half as a JWKS and signs RS256
// tokens on request, so that fabric-api verifies real signatures against a
// real key set where no identity provider is bound yet.
//
// It stands where the identity provider of a deployment stands and is not a
// product capability: it has no accounts, no login and no secret besides its
// key. No product binary links it, which internal/architecture enforces.
// Binding the real provider changes the issuer, the audience and the JWKS URL
// fabric-api is configured with, not its code.
package devissuer

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"time"
)

const (
	algorithm = "RS256"

	// keyBits is the shortest modulus the verifier accepts. A token signed
	// with a shorter key would be refused by it.
	keyBits = 2048

	// JWKSPath is where the issuer publishes its key set, relative to its
	// URL.
	JWKSPath = "/.well-known/jwks.json"

	// DefaultTTL is how long a token is valid when the request names no
	// lifetime.
	DefaultTTL = 10 * time.Minute

	pemType = "PRIVATE KEY"
)

// GenerateKey generates a signing key.
func GenerateKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, keyBits)
}

// EncodeKeyPEM encodes key as PKCS #8 PEM, so that a development machine
// keeps the same key, and with it the same key identifier, between runs.
func EncodeKeyPEM(key *rsa.PrivateKey) ([]byte, error) {
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("devissuer: encode the key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemType, Bytes: encoded}), nil
}

// DecodeKeyPEM reads back what EncodeKeyPEM wrote. It refuses another PEM
// type, another kind of key and a key that is too short.
func DecodeKeyPEM(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != pemType {
		return nil, fmt.Errorf("devissuer: a %q PEM block is required", pemType)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("devissuer: parse the key: %w", err)
	}
	key, isRSA := parsed.(*rsa.PrivateKey)
	if !isRSA {
		return nil, errors.New("devissuer: the key is not an RSA key")
	}
	if key.N.BitLen() < keyBits {
		return nil, fmt.Errorf("devissuer: the key has %d bits, at least %d are required", key.N.BitLen(), keyBits)
	}
	return key, nil
}

// Issuer is a token issuer under the control of whoever holds its key.
type Issuer struct {
	url   string
	key   *rsa.PrivateKey
	keyID string
	jwks  []byte
}

// New builds an issuer that names itself issuerURL and signs with key.
//
// The URL is written into every token as it is given and compared for
// equality by the verifier. It is a scheme and a host, with no path: the key
// set is served at JWKSPath under it.
func New(issuerURL string, key *rsa.PrivateKey) (*Issuer, error) {
	parsed, err := url.Parse(issuerURL)
	if err != nil {
		return nil, errors.New("devissuer: the issuer is not a URL")
	}
	switch {
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return nil, errors.New("devissuer: the issuer must use http or https")
	case parsed.Hostname() == "":
		return nil, errors.New("devissuer: the issuer has no host")
	case parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "":
		return nil, errors.New("devissuer: the issuer is a scheme and a host only, as in http://127.0.0.1:9450")
	}
	if key == nil {
		return nil, errors.New("devissuer: a signing key is required")
	}
	if key.N.BitLen() < keyBits {
		return nil, fmt.Errorf("devissuer: the key has %d bits, at least %d are required", key.N.BitLen(), keyBits)
	}

	public := publicJWK(&key.PublicKey)
	public.KeyID, err = thumbprint(public)
	if err != nil {
		return nil, err
	}
	jwks, err := json.Marshal(struct {
		Keys []jsonWebKey `json:"keys"`
	}{Keys: []jsonWebKey{public}})
	if err != nil {
		return nil, fmt.Errorf("devissuer: encode the key set: %w", err)
	}
	return &Issuer{url: issuerURL, key: key, keyID: public.KeyID, jwks: jwks}, nil
}

// URL is the value written into the iss claim.
func (i *Issuer) URL() string { return i.url }

// KeyID identifies the published key. It is the RFC 7638 thumbprint of the
// key, so the same key has the same identifier on every run.
func (i *Issuer) KeyID() string { return i.keyID }

// JWKSURL is the key set address a verifier is configured with.
func (i *Issuer) JWKSURL() string { return i.url + JWKSPath }

// Handler serves the key set. It serves nothing else: tokens are signed by
// whoever holds the key, through Mint, never over HTTP.
func (i *Issuer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+JWKSPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(i.jwks)
	})
	return mux
}

// TokenRequest is what a token is to say.
type TokenRequest struct {
	// Subject is the caller the token is about.
	Subject string
	// Audience names the resource servers the token is addressed to. At
	// least one is required.
	Audience []string
	// IssuedAt is the issue time and the start of validity. The zero value
	// means now.
	IssuedAt time.Time
	// TTL is the lifetime. The zero value means DefaultTTL.
	TTL time.Duration
}

// Mint signs an access token for request. Every token gets a random
// identifier.
func (i *Issuer) Mint(request TokenRequest) (string, error) {
	if request.Subject == "" {
		return "", errors.New("devissuer: a subject is required")
	}
	if len(request.Audience) == 0 {
		return "", errors.New("devissuer: at least one audience is required")
	}
	issuedAt := request.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = time.Now()
	}
	ttl := request.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if ttl < 0 {
		return "", errors.New("devissuer: the lifetime cannot be negative")
	}
	tokenID := make([]byte, 16)
	if _, err := rand.Read(tokenID); err != nil {
		return "", fmt.Errorf("devissuer: generate the token identifier: %w", err)
	}
	return i.Sign(nil, map[string]any{
		"iss": i.url,
		"sub": request.Subject,
		"aud": request.Audience,
		"iat": issuedAt.Unix(),
		"nbf": issuedAt.Unix(),
		"exp": issuedAt.Add(ttl).Unix(),
		"jti": hex.EncodeToString(tokenID),
	})
}

// Sign signs any header and payload with the key of the issuer. A nil header
// means the standard one. It exists so that a test can build a token whose
// header or claims are not what Mint would write.
func (i *Issuer) Sign(header, payload map[string]any) (string, error) {
	if header == nil {
		header = map[string]any{"alg": algorithm, "kid": i.keyID, "typ": "JWT"}
	}
	encodedHeader, err := encodeSegment(header)
	if err != nil {
		return "", err
	}
	encodedPayload, err := encodeSegment(payload)
	if err != nil {
		return "", err
	}
	signed := encodedHeader + "." + encodedPayload
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, i.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("devissuer: sign: %w", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func encodeSegment(value map[string]any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("devissuer: encode a token segment: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

// jsonWebKey is the published form of the verification key.
type jsonWebKey struct {
	KeyType   string `json:"kty"`
	KeyID     string `json:"kid"`
	Algorithm string `json:"alg"`
	Use       string `json:"use"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
}

func publicJWK(key *rsa.PublicKey) jsonWebKey {
	return jsonWebKey{
		KeyType:   "RSA",
		Algorithm: algorithm,
		Use:       "sig",
		Modulus:   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		Exponent:  base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

// thumbprint is the RFC 7638 thumbprint of key: the SHA-256 of its required
// members as JSON, in lexical order and without white space.
func thumbprint(key jsonWebKey) (string, error) {
	canonical, err := json.Marshal(struct {
		Exponent string `json:"e"`
		KeyType  string `json:"kty"`
		Modulus  string `json:"n"`
	}{Exponent: key.Exponent, KeyType: key.KeyType, Modulus: key.Modulus})
	if err != nil {
		return "", fmt.Errorf("devissuer: compute the key identifier: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return base64.RawURLEncoding.EncodeToString(digest[:]), nil
}
