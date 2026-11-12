package identity

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// algorithmRS256 is the only signature algorithm accepted. The header of a
// token is compared with this constant and never obeyed: a verifier that runs
// whichever algorithm a token names can be talked into "none", or into a
// symmetric algorithm keyed with the public key.
const algorithmRS256 = "RS256"

// acceptedTypes are the values of the typ header a token may carry, compared
// without regard to case. The header is optional, JWT is what issuers send by
// default and at+jwt is the type of an RFC 9068 access token. Any other
// explicit type, such as a DPoP proof, a logout token or a security event,
// says the token was issued for something else.
//
// The type does not tell an ID token from an access token, since both are
// commonly typed JWT. The audience does: an ID token is addressed to the
// client that asked for it, not to this deployment (F05 §4.2).
var acceptedTypes = []string{"", "jwt", "at+jwt", "application/at+jwt"}

const (
	// tokenMaxBytes bounds the compact serialization that is parsed at all.
	// An access token is well under a kilobyte.
	tokenMaxBytes = 16 << 10

	// SubjectMaxLength bounds the subject, in characters. A principal is
	// stored and audited under it, so its length has to be known. It is the
	// bound the contract of the API puts on a reference: whoever can be
	// named as a principal can also authenticate as one.
	SubjectMaxLength = 255

	// leeway absorbs the clock drift between the issuer and this process on
	// the not-before and issued-at checks. The expiry check gets none: a
	// token is not accepted after the instant its issuer ended it.
	leeway = 30 * time.Second

	// numericDateMax is 9999-12-31T23:59:59Z in seconds. A date beyond it is
	// refused rather than converted, because the conversion of an
	// out-of-range number differs from one platform to the next.
	numericDateMax = 253402300799
)

var (
	// ErrTokenMalformed reports a token this build cannot parse or whose
	// header it does not accept.
	ErrTokenMalformed = errors.New("identity: the token is not a well formed RS256 JWT")

	// ErrSignatureInvalid reports a token whose signature does not verify.
	ErrSignatureInvalid = errors.New("identity: the token signature does not verify")

	// ErrClaimsRejected reports a well formed, correctly signed token whose
	// issuer, audience, subject or validity window is not acceptable.
	ErrClaimsRejected = errors.New("identity: the token claims were rejected")
)

// Principal is who a verified token is about: a subject as one issuer names
// it (F05 §2.2). The same subject under another issuer is another principal.
//
// Nothing else of the token is handed on. A claim no caller of this package
// can read is a claim that cannot quietly become an authorization input.
type Principal struct {
	Issuer  string
	Subject string
}

// Config binds a verifier to one issuer.
type Config struct {
	// Issuer is compared with the iss claim for equality. A token of another
	// issuer is refused even when its signature verifies.
	Issuer string
	// Audience identifies this deployment as a resource server. A token
	// addressed to someone else is refused.
	Audience string
	// JWKSURL is where the issuer publishes its verification keys.
	JWKSURL string
	// HTTPClient fetches the key set. Nil means a client with a five second
	// timeout that follows no redirect: the keys come from the configured
	// URL or from nowhere.
	HTTPClient *http.Client
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Verifier turns a bearer token into the principal it is about.
type Verifier struct {
	keys     *KeySet
	issuer   string
	audience string
	now      func() time.Time
}

// NewVerifier builds a verifier. The issuer, the audience and the JWKS URL
// are all required: a verifier without one of them either trusts the wrong
// issuer or lets in tokens addressed to someone else. A value with
// surrounding white space is refused as well, since it is compared for
// equality and no token would ever match it.
func NewVerifier(config Config) (*Verifier, error) {
	switch {
	case config.Issuer == "":
		return nil, errors.New("identity: the issuer is required")
	case strings.TrimSpace(config.Issuer) != config.Issuer:
		return nil, errors.New("identity: the issuer has surrounding white space")
	case config.Audience == "":
		return nil, errors.New("identity: the audience is required")
	case strings.TrimSpace(config.Audience) != config.Audience:
		return nil, errors.New("identity: the audience has surrounding white space")
	}

	client := config.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	keys, err := NewKeySet(config.JWKSURL, client, now)
	if err != nil {
		return nil, err
	}
	return &Verifier{keys: keys, issuer: config.Issuer, audience: config.Audience, now: now}, nil
}

// Verify parses the compact serialization, verifies the signature with the
// key the header names and then checks the claims. Nothing of the payload is
// read before the signature has verified.
//
// A token that is not accepted fails with ErrTokenMalformed,
// ErrSignatureInvalid, ErrClaimsRejected or ErrKeyUnknown. ErrKeySetUnavailable
// is different in kind: the token could not be judged at all.
//
// The errors describe the reason for an operator. They never quote the token,
// and none of them is meant to be sent to the caller.
func (v *Verifier) Verify(ctx context.Context, token string) (Principal, error) {
	if len(token) > tokenMaxBytes {
		return Principal{}, fmt.Errorf("%w: it is longer than %d bytes", ErrTokenMalformed, tokenMaxBytes)
	}
	if strings.Count(token, ".") != 2 {
		return Principal{}, fmt.Errorf("%w: it does not have three segments", ErrTokenMalformed)
	}
	signatureAt := strings.LastIndexByte(token, '.')
	signedPart, encodedSignature := token[:signatureAt], token[signatureAt+1:]
	encodedHeader, encodedPayload, _ := strings.Cut(signedPart, ".")

	header, err := decodeObject(encodedHeader)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: the header cannot be decoded", ErrTokenMalformed)
	}
	keyID, err := checkHeader(header)
	if err != nil {
		return Principal{}, err
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(encodedSignature)
	if err != nil || len(signature) == 0 {
		return Principal{}, fmt.Errorf("%w: the signature cannot be decoded", ErrTokenMalformed)
	}

	key, err := v.keys.Key(ctx, keyID)
	if err != nil {
		return Principal{}, err
	}
	digest := sha256.Sum256([]byte(signedPart))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return Principal{}, ErrSignatureInvalid
	}

	claims, err := decodeObject(encodedPayload)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: the payload cannot be decoded", ErrTokenMalformed)
	}
	return v.checkClaims(claims)
}

// checkHeader accepts the header of an RS256 token and returns the identifier
// of its key. The header is not signed yet when it is read, so nothing of it
// is repeated in an error.
func checkHeader(header map[string]json.RawMessage) (keyID string, err error) {
	if algorithm, _ := stringMember(header, "alg"); algorithm != algorithmRS256 {
		return "", fmt.Errorf("%w: the algorithm is not %s", ErrTokenMalformed, algorithmRS256)
	}
	// A crit header lists extensions the verifier must understand. This one
	// understands none, and RFC 7515 then requires the token to be refused.
	if _, present := header["crit"]; present {
		return "", fmt.Errorf("%w: the header requires an extension this build does not implement", ErrTokenMalformed)
	}
	tokenType, typed := stringMember(header, "typ")
	if _, present := header["typ"]; present && !typed {
		return "", fmt.Errorf("%w: the type is not a string", ErrTokenMalformed)
	}
	if !slices.Contains(acceptedTypes, strings.ToLower(tokenType)) {
		return "", fmt.Errorf("%w: the type is not that of an access token", ErrTokenMalformed)
	}
	keyID, _ = stringMember(header, "kid")
	if keyID == "" {
		return "", fmt.Errorf("%w: the header names no key", ErrTokenMalformed)
	}
	return keyID, nil
}

func (v *Verifier) checkClaims(claims map[string]json.RawMessage) (Principal, error) {
	now := v.now()

	if issuer, _ := stringMember(claims, "iss"); issuer != v.issuer {
		return Principal{}, fmt.Errorf("%w: the issuer is not the configured one", ErrClaimsRejected)
	}
	if !slices.Contains(audienceMember(claims), v.audience) {
		return Principal{}, fmt.Errorf("%w: the token is not addressed to this deployment", ErrClaimsRejected)
	}
	subject, _ := stringMember(claims, "sub")
	switch {
	case subject == "":
		return Principal{}, fmt.Errorf("%w: the token names no subject", ErrClaimsRejected)
	case utf8.RuneCountInString(subject) > SubjectMaxLength:
		return Principal{}, fmt.Errorf("%w: the subject is longer than %d characters", ErrClaimsRejected, SubjectMaxLength)
	case strings.ContainsFunc(subject, unicode.IsControl):
		// A subject is written to logs and audit records as it is.
		return Principal{}, fmt.Errorf("%w: the subject contains a control character", ErrClaimsRejected)
	}

	expiresAt, present, valid := numericDateMember(claims, "exp")
	switch {
	case !present:
		return Principal{}, fmt.Errorf("%w: the token names no expiry", ErrClaimsRejected)
	case !valid:
		return Principal{}, fmt.Errorf("%w: the expiry is not a date", ErrClaimsRejected)
	case !now.Before(expiresAt):
		return Principal{}, fmt.Errorf("%w: the token has expired", ErrClaimsRejected)
	}
	if notBefore, present, valid := numericDateMember(claims, "nbf"); present {
		switch {
		case !valid:
			return Principal{}, fmt.Errorf("%w: the not-before time is not a date", ErrClaimsRejected)
		case now.Add(leeway).Before(notBefore):
			return Principal{}, fmt.Errorf("%w: the token is not valid yet", ErrClaimsRejected)
		}
	}
	if issuedAt, present, valid := numericDateMember(claims, "iat"); present {
		switch {
		case !valid:
			return Principal{}, fmt.Errorf("%w: the issue time is not a date", ErrClaimsRejected)
		case now.Add(leeway).Before(issuedAt):
			return Principal{}, fmt.Errorf("%w: the token was issued in the future", ErrClaimsRejected)
		}
	}
	return Principal{Issuer: v.issuer, Subject: subject}, nil
}

// decodeObject decodes one segment of the compact serialization into the
// members of its JSON object. The members are looked up by their exact names:
// decoding into a struct would also accept a name that differs in case.
func decodeObject(segment string) (map[string]json.RawMessage, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(segment)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(decoded, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("not a JSON object")
	}
	return object, nil
}

// stringMember returns the member name of object if it is a string.
func stringMember(object map[string]json.RawMessage, name string) (value string, isString bool) {
	raw, present := object[name]
	if !present {
		return "", false
	}
	if err := json.Unmarshal(raw, &value); err != nil || !strings.HasPrefix(strings.TrimSpace(string(raw)), `"`) {
		return "", false
	}
	return value, true
}

// audienceMember returns the audiences of the claims. The specification
// allows a single string and an array of strings; anything else is no
// audience at all.
func audienceMember(claims map[string]json.RawMessage) []string {
	if single, isString := stringMember(claims, "aud"); isString {
		return []string{single}
	}
	var several []json.RawMessage
	if err := json.Unmarshal(claims["aud"], &several); err != nil {
		return nil
	}
	audiences := make([]string, 0, len(several))
	for _, raw := range several {
		audience, isString := stringMember(map[string]json.RawMessage{"aud": raw}, "aud")
		if !isString {
			return nil
		}
		audiences = append(audiences, audience)
	}
	return audiences
}

// numericDateMember returns the member name of claims as an instant. The
// specification encodes a date as seconds since the epoch and allows a
// fraction, which is dropped.
func numericDateMember(claims map[string]json.RawMessage, name string) (instant time.Time, present, valid bool) {
	raw, present := claims[name]
	if !present {
		return time.Time{}, false, false
	}
	var seconds float64
	if err := json.Unmarshal(raw, &seconds); err != nil || strings.HasPrefix(strings.TrimSpace(string(raw)), `"`) {
		return time.Time{}, true, false
	}
	if math.IsNaN(seconds) || seconds < 0 || seconds > numericDateMax {
		return time.Time{}, true, false
	}
	return time.Unix(int64(seconds), 0).UTC(), true, true
}
