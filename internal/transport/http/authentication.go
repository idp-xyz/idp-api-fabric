package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"

	"go.idp.xyz/idp-api-fabric/internal/platform/identity"
)

// TokenVerifier turns a bearer token into the principal it is about. It is
// the identity provider of the deployment as the transport sees it.
type TokenVerifier interface {
	Verify(ctx context.Context, token string) (identity.Principal, error)
}

// bearerScheme is the security scheme of the contract. An operation that
// requires any other scheme is refused: the transport knows this one only.
const bearerScheme = "bearerToken"

// authenticationRefusal is why a request was not authenticated.
type authenticationRefusal int

const (
	// notRefused is a request that was authenticated, or never had to be.
	notRefused authenticationRefusal = iota
	// credentialsMissing is a request without an Authorization header.
	credentialsMissing
	// tokenInvalid is a request whose credentials are not accepted.
	tokenInvalid
	// providerUnavailable is a request whose token could not be judged: the
	// keys of the identity provider are out of reach.
	providerUnavailable
	// schemeUnknown is an operation whose security scheme the transport does
	// not implement. It is a mistake in the contract, not in the request.
	schemeUnknown
)

var errNotAuthenticated = errors.New("the request is not authenticated")

// authenticator decides the security requirement of an operation. The
// validator calls it for every operation that declares one and for no
// operation declared anonymous, before it looks at a parameter or the body.
type authenticator struct {
	verifier TokenVerifier
	logger   *slog.Logger
}

// authenticate verifies the bearer token of the request and records the
// principal in the state of the request, where the operation finds it.
//
// Why a token is refused goes to the log, never to the caller, and the log
// never gets the token.
func (a authenticator) authenticate(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
	request := input.RequestValidationInput.Request
	state := stateOf(request.Context())
	if state == nil {
		return errors.New("the request has no state")
	}
	refuse := func(refusal authenticationRefusal, reason error) error {
		state.refusal = refusal
		a.logger.InfoContext(ctx, "authentication refused",
			"correlation_id", state.correlationID, "method", request.Method, "path", request.URL.Path, "reason", reason)
		return errNotAuthenticated
	}

	if input.SecuritySchemeName != bearerScheme {
		return refuse(schemeUnknown, errors.New("the operation requires a security scheme the transport does not implement"))
	}
	token, present, wellFormed := bearerToken(request)
	switch {
	case !present:
		return refuse(credentialsMissing, errors.New("no Authorization header"))
	case !wellFormed:
		return refuse(tokenInvalid, errors.New("the Authorization header is not one Bearer token"))
	}

	principal, err := a.verifier.Verify(ctx, token)
	switch {
	case errors.Is(err, identity.ErrKeySetUnavailable):
		return refuse(providerUnavailable, err)
	case err != nil:
		return refuse(tokenInvalid, err)
	}
	state.principal, state.authenticated = principal, true
	return nil
}

// bearerToken returns the token of an Authorization header that carries
// exactly one Bearer token (RFC 6750 §2.1).
//
// Credentials of another scheme are credentials this API does not accept,
// which is not the same as none. Two Authorization headers are ambiguous and
// are not accepted either.
func bearerToken(request *http.Request) (token string, present, wellFormed bool) {
	headers := request.Header.Values("Authorization")
	if len(headers) == 0 {
		return "", false, false
	}
	if len(headers) > 1 {
		return "", true, false
	}
	scheme, token, found := strings.Cut(headers[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t") {
		return "", true, false
	}
	return token, true, true
}
