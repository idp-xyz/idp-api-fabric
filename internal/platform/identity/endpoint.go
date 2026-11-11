package identity

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// checkKeySetURL refuses a JWKS URL that no fetch could use, or that a fetch
// should not use, so that a misconfigured deployment fails when it starts
// instead of refusing every protected request later.
//
// The keys fetched from this URL decide which tokens are trusted. Over plain
// http anyone on the path can substitute them, so http is accepted for a
// loopback address only, which is where a development issuer listens.
func checkKeySetURL(raw string) error {
	if raw == "" {
		return errors.New("identity: the JWKS URL is required")
	}
	if strings.ContainsFunc(raw, unicode.IsSpace) {
		return errors.New("identity: the JWKS URL contains white space")
	}
	endpoint, err := url.Parse(raw)
	if err != nil {
		// A url.Error repeats the whole value, which may carry credentials.
		// Only the cause is kept.
		var parseErr *url.Error
		if errors.As(err, &parseErr) {
			err = parseErr.Err
		}
		return fmt.Errorf("identity: the JWKS URL is not a URL: %w", err)
	}

	switch {
	case endpoint.User != nil:
		return errors.New("identity: the JWKS URL must not carry credentials")
	case endpoint.Fragment != "":
		return errors.New("identity: the JWKS URL must not carry a fragment")
	case endpoint.Hostname() == "":
		return errors.New("identity: the JWKS URL has no host")
	}
	switch endpoint.Scheme {
	case "https":
	case "http":
		if address, err := netip.ParseAddr(endpoint.Hostname()); err != nil || !address.IsLoopback() {
			return errors.New("identity: the JWKS URL must use https; http is accepted for a loopback IP address only")
		}
	default:
		return errors.New("identity: the JWKS URL must use https")
	}
	if port := endpoint.Port(); port != "" {
		if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
			return errors.New("identity: the JWKS URL names a port that does not exist")
		}
	}
	return nil
}
