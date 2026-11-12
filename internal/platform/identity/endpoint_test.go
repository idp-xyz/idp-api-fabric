package identity

import "testing"

func TestTheKeySetURLIsHTTPSOrLoopback(t *testing.T) {
	accepted := []string{
		"https://issuer.example/.well-known/jwks.json",
		"https://issuer.example:8443/keys?realm=fabric",
		"https://10.0.0.5/jwks",
		// A development issuer listens on a loopback address.
		"http://127.0.0.1:9450/.well-known/jwks.json",
		"http://127.0.0.2/jwks",
		"http://[::1]:9450/jwks",
	}
	for _, value := range accepted {
		if err := checkKeySetURL(value); err != nil {
			t.Errorf("%q was refused: %v", value, err)
		}
	}

	refused := map[string]string{
		"nothing":                      "",
		"no scheme":                    "issuer.example/jwks",
		"another scheme":               "ftp://issuer.example/jwks",
		"http to another host":         "http://issuer.example/jwks",
		"http to a private address":    "http://10.0.0.5/jwks",
		"http to a link-local address": "http://169.254.169.254/latest/meta-data",
		// A name is resolved by whoever answers for it; only an address is
		// known to be loopback.
		"http to a name":      "http://localhost:9450/jwks",
		"no host":             "https:///jwks",
		"credentials":         "https://user:secret@issuer.example/jwks",
		"a fragment":          "https://issuer.example/jwks#keys",
		"a port out of range": "https://issuer.example:99999/jwks",
		"port zero":           "https://issuer.example:0/jwks",
		"white space":         "https://issuer.example/ jwks",
		"a trailing newline":  "https://issuer.example/jwks\n",
	}
	for name, value := range refused {
		t.Run(name, func(t *testing.T) {
			if err := checkKeySetURL(value); err == nil {
				t.Fatalf("%q was accepted, want a refusal", value)
			}
		})
	}
}
