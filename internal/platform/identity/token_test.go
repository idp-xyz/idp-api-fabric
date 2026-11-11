package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// saysNothingOfTheToken fails the test if the error repeats a segment of the
// token or a value a test planted in it. The errors are written for a log.
func saysNothingOfTheToken(t *testing.T, err error, token string) {
	t.Helper()

	message := err.Error()
	if strings.Contains(message, "canary") {
		t.Errorf("the error quotes a value of the token: %s", message)
	}
	for _, segment := range strings.Split(token, ".") {
		if len(segment) >= 16 && strings.Contains(message, segment) {
			t.Errorf("the error quotes a segment of the token: %s", message)
		}
	}
}

func encodeJSON(t *testing.T, value any) string {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func TestAValidTokenYieldsItsPrincipal(t *testing.T) {
	f := newFixture(t)

	principal, err := f.verify(f.token(t, f.issuer, "alice"))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if want := (Principal{Issuer: f.issuer.URL(), Subject: "alice"}); principal != want {
		t.Errorf("principal %+v, want %+v", principal, want)
	}
}

func TestUnacceptableClaimsAreRejected(t *testing.T) {
	f := newFixture(t)
	now := f.clock.Now()

	cases := map[string]func(claims map[string]any){
		"another issuer":                 func(c map[string]any) { c["iss"] = "https://canary.example" },
		"no issuer":                      func(c map[string]any) { delete(c, "iss") },
		"an issuer that is not a string": func(c map[string]any) { c["iss"] = 7 },

		// An ID token is addressed to the client that asked for it.
		"addressed to a client, as an ID token is": func(c map[string]any) { c["aud"] = "canary-client" },
		"no audience":                                      func(c map[string]any) { delete(c, "aud") },
		"a null audience":                                  func(c map[string]any) { c["aud"] = nil },
		"an audience that is not a string":                 func(c map[string]any) { c["aud"] = 7 },
		"an audience list without this deployment":         func(c map[string]any) { c["aud"] = []string{"canary-a", "canary-b"} },
		"an audience list with a member that is no string": func(c map[string]any) { c["aud"] = []any{audience, 7} },

		"no subject":                     func(c map[string]any) { delete(c, "sub") },
		"an empty subject":               func(c map[string]any) { c["sub"] = "" },
		"a subject that is not a string": func(c map[string]any) { c["sub"] = 7 },
		"a subject that is too long":     func(c map[string]any) { c["sub"] = strings.Repeat("s", subjectMaxBytes+1) },
		"a subject with a line break":    func(c map[string]any) { c["sub"] = "alice\nlevel=error" },

		"no expiry":                      func(c map[string]any) { delete(c, "exp") },
		"expiring at this instant":       func(c map[string]any) { c["exp"] = now.Unix() },
		"expired":                        func(c map[string]any) { c["exp"] = now.Add(-time.Second).Unix() },
		"an expiry that is not a number": func(c map[string]any) { c["exp"] = "canary" },
		"an expiry beyond the year 9999": func(c map[string]any) { c["exp"] = int64(numericDateMax + 1) },
		"a negative expiry":              func(c map[string]any) { c["exp"] = -1 },

		"not valid yet":                          func(c map[string]any) { c["nbf"] = now.Add(leeway + time.Second).Unix() },
		"a not-before time that is not a number": func(c map[string]any) { c["nbf"] = "canary" },
		"issued in the future":                   func(c map[string]any) { c["iat"] = now.Add(leeway + time.Second).Unix() },
		"an issue time that is not a number":     func(c map[string]any) { c["iat"] = true },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			claims := f.claims()
			change(claims)
			token := sign(t, f.issuer, f.header(), claims)

			principal, err := f.verify(token)
			if !errors.Is(err, ErrClaimsRejected) {
				t.Fatalf("got principal %+v and error %v, want the claims rejected", principal, err)
			}
			if principal != (Principal{}) {
				t.Errorf("a rejected token yielded the principal %+v", principal)
			}
			saysNothingOfTheToken(t, err, token)
		})
	}
}

func TestClaimsAndHeadersWithinWhatIsAllowedAreAccepted(t *testing.T) {
	f := newFixture(t)
	now := f.clock.Now()

	cases := map[string]func(header, claims map[string]any){
		"an audience list that names this deployment":    func(_, c map[string]any) { c["aud"] = []string{"another-service", audience} },
		"valid from a moment inside the tolerated drift": func(_, c map[string]any) { c["nbf"] = now.Add(leeway).Unix() },
		"issued a moment inside the tolerated drift":     func(_, c map[string]any) { c["iat"] = now.Add(leeway).Unix() },
		"no not-before time":                             func(_, c map[string]any) { delete(c, "nbf") },
		"no issue time":                                  func(_, c map[string]any) { delete(c, "iat") },
		"a fractional expiry":                            func(_, c map[string]any) { c["exp"] = float64(now.Unix()) + 60.5 },
		"no type":                                        func(h, _ map[string]any) { delete(h, "typ") },
		"the type in lower case":                         func(h, _ map[string]any) { h["typ"] = "jwt" },
		"the type of an RFC 9068 access token":           func(h, _ map[string]any) { h["typ"] = "at+jwt" },
		"that type as a media type":                      func(h, _ map[string]any) { h["typ"] = "application/at+jwt" },
		// Claims about what the caller may do are carried by many issuers.
		// They are not read, so they change nothing.
		"claims this build does not read": func(_, c map[string]any) {
			c["scope"], c["roles"], c["tenant_id"] = "platform:admin", []string{"platform_admin"}, "tenant-1"
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			header, claims := f.header(), f.claims()
			change(header, claims)

			principal, err := f.verify(sign(t, f.issuer, header, claims))
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if want := (Principal{Issuer: f.issuer.URL(), Subject: "alice"}); principal != want {
				t.Errorf("principal %+v, want %+v", principal, want)
			}
		})
	}
}

func TestUnacceptableHeadersAreMalformed(t *testing.T) {
	f := newFixture(t)

	cases := map[string]func(header map[string]any){
		"another RSA algorithm":             func(h map[string]any) { h["alg"] = "RS384" },
		"the none algorithm":                func(h map[string]any) { h["alg"] = "none" },
		"a symmetric algorithm":             func(h map[string]any) { h["alg"] = "HS256" },
		"an algorithm that is not a string": func(h map[string]any) { h["alg"] = 7 },
		"no algorithm":                      func(h map[string]any) { delete(h, "alg") },
		// Member names are exact. A decoder that ignores case would read
		// this as the algorithm.
		"the algorithm under a name in another case": func(h map[string]any) { delete(h, "alg"); h["ALG"] = "RS256" },
		"no key identifier":                          func(h map[string]any) { delete(h, "kid") },
		"an empty key identifier":                    func(h map[string]any) { h["kid"] = "" },
		"a key identifier that is not a string":      func(h map[string]any) { h["kid"] = 7 },
		"the type of a DPoP proof":                   func(h map[string]any) { h["typ"] = "dpop+jwt" },
		"the type of a logout token":                 func(h map[string]any) { h["typ"] = "logout+jwt" },
		"a type that is not a string":                func(h map[string]any) { h["typ"] = 7 },
		"a critical extension":                       func(h map[string]any) { h["crit"] = []string{"exp"} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			header := f.header()
			change(header)
			token := sign(t, f.issuer, header, f.claims())

			if _, err := f.verify(token); !errors.Is(err, ErrTokenMalformed) {
				t.Fatalf("got %v, want a malformed token", err)
			} else {
				saysNothingOfTheToken(t, err, token)
			}
		})
	}
	if fetches := f.endpoint.count(); fetches != 0 {
		t.Errorf("%d key set fetches for tokens whose header is refused, want none", fetches)
	}
}

// The two attacks a verifier that obeys the header falls for: a token with no
// signature at all, and a token authenticated with the public key as if it
// were a shared secret.
func TestTheClassicAlgorithmAttacksFail(t *testing.T) {
	f := newFixture(t)
	payload := encodeJSON(t, f.claims())

	t.Run("an unsigned token", func(t *testing.T) {
		token := encodeJSON(t, map[string]any{"alg": "none", "typ": "JWT"}) + "." + payload + "."
		if _, err := f.verify(token); !errors.Is(err, ErrTokenMalformed) {
			t.Fatalf("got %v, want a malformed token", err)
		}
	})

	t.Run("a token keyed with the public key", func(t *testing.T) {
		public, err := x509.MarshalPKIXPublicKey(&publishedKey().PublicKey)
		if err != nil {
			t.Fatalf("encode the public key: %v", err)
		}
		signed := encodeJSON(t, map[string]any{"alg": "HS256", "kid": f.issuer.KeyID(), "typ": "JWT"}) + "." + payload
		mac := hmac.New(sha256.New, public)
		mac.Write([]byte(signed))
		token := signed + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

		if _, err := f.verify(token); !errors.Is(err, ErrTokenMalformed) {
			t.Fatalf("got %v, want a malformed token", err)
		}
	})
}

func TestWhatIsNotThreeDecodableSegmentsIsMalformed(t *testing.T) {
	f := newFixture(t)
	valid := sign(t, f.issuer, f.header(), f.claims())
	segments := strings.Split(valid, ".")
	header, payload, signature := segments[0], segments[1], segments[2]
	encode := base64.RawURLEncoding.EncodeToString

	cases := map[string]string{
		"nothing":                           "",
		"one segment":                       header,
		"two segments":                      header + "." + payload,
		"four segments":                     valid + "." + signature,
		"an empty signature":                header + "." + payload + ".",
		"a signature that is not base64url": header + "." + payload + ".!!!",
		"a padded signature":                valid + "=",
		"an empty header":                   "." + payload + "." + signature,
		"a header that is not base64url":    "!!!." + payload + "." + signature,
		"a header that is not JSON":         encode([]byte("not json")) + "." + payload + "." + signature,
		"a header that is a JSON array":     encode([]byte(`["RS256"]`)) + "." + payload + "." + signature,
		"a header that is null":             encode([]byte("null")) + "." + payload + "." + signature,
		"longer than the limit":             strings.Repeat("a", tokenMaxBytes+1),
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := f.verify(token); !errors.Is(err, ErrTokenMalformed) {
				t.Fatalf("got %v, want a malformed token", err)
			} else {
				saysNothingOfTheToken(t, err, token)
			}
		})
	}

	// A payload that is not an object is found only after the signature has
	// verified, so the issuer has to sign it.
	t.Run("a signed payload that is null", func(t *testing.T) {
		if _, err := f.verify(sign(t, f.issuer, f.header(), nil)); !errors.Is(err, ErrTokenMalformed) {
			t.Fatalf("got %v, want a malformed token", err)
		}
	})
}

func TestAnAlteredTokenFailsTheSignature(t *testing.T) {
	f := newFixture(t)
	valid := sign(t, f.issuer, f.header(), f.claims())
	segments := strings.Split(valid, ".")

	// Besides naming another subject, the forged payloads carry claims that
	// would be rejected. The signature is what fails: nothing of a payload is
	// judged before it.
	forged := f.claims()
	forged["sub"], forged["iss"] = "mallory", "https://canary.example"

	t.Run("another payload under the same signature", func(t *testing.T) {
		token := segments[0] + "." + encodeJSON(t, forged) + "." + segments[2]
		if _, err := f.verify(token); !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("got %v, want an invalid signature", err)
		}
	})

	t.Run("the signature of another token", func(t *testing.T) {
		other := strings.Split(sign(t, f.issuer, f.header(), forged), ".")
		token := segments[0] + "." + segments[1] + "." + other[2]
		if _, err := f.verify(token); !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("got %v, want an invalid signature", err)
		}
	})

	t.Run("another key under the published key identifier", func(t *testing.T) {
		token := sign(t, f.rotated(t), f.header(), f.claims())
		if _, err := f.verify(token); !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("got %v, want an invalid signature", err)
		}
	})
}

func TestATokenOfAKeyTheIssuerDoesNotPublishIsRefused(t *testing.T) {
	f := newFixture(t)

	// The same issuer URL, a valid signature, but a key the endpoint never
	// published.
	token := f.token(t, f.rotated(t), "alice")
	if _, err := f.verify(token); !errors.Is(err, ErrKeyUnknown) {
		t.Fatalf("got %v, want an unknown key", err)
	}
}

func TestAVerifierNeedsAnIssuerAnAudienceAndAKeySet(t *testing.T) {
	complete := Config{Issuer: "https://issuer.example", Audience: audience, JWKSURL: "https://issuer.example/jwks"}
	if _, err := NewVerifier(complete); err != nil {
		t.Fatalf("a complete configuration was refused: %v", err)
	}

	cases := map[string]func(config *Config){
		"no issuer":                      func(c *Config) { c.Issuer = "" },
		"an issuer with white space":     func(c *Config) { c.Issuer = "https://issuer.example " },
		"no audience":                    func(c *Config) { c.Audience = "" },
		"an audience with white space":   func(c *Config) { c.Audience = " " + audience },
		"no JWKS URL":                    func(c *Config) { c.JWKSURL = "" },
		"a JWKS URL over plain http":     func(c *Config) { c.JWKSURL = "http://issuer.example/jwks" },
		"a JWKS URL that is not a URL":   func(c *Config) { c.JWKSURL = "issuer.example/jwks" },
		"a JWKS URL with credentials":    func(c *Config) { c.JWKSURL = "https://user:canary@issuer.example/jwks" },
		"a JWKS URL that does not parse": func(c *Config) { c.JWKSURL = "https://user:canary@issuer.example:port/jwks" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			config := complete
			change(&config)

			_, err := NewVerifier(config)
			if err == nil {
				t.Fatal("the configuration was accepted, want a refusal")
			}
			if strings.Contains(err.Error(), "canary") {
				t.Errorf("the error repeats the credentials of the URL: %v", err)
			}
		})
	}
}
