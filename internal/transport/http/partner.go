package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	catalogdomain "go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/platform/identity"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	publication "go.idp.xyz/idp-api-fabric/internal/publication/application"
	publicationdomain "go.idp.xyz/idp-api-fabric/internal/publication/domain"
	publicationports "go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

type PartnerReferences interface {
	PartnerArtifact(ctx context.Context, caller publicationdomain.Principal, assetID, correlationID string) (publicationports.Artifact, error)
}

// NewPartnerHandler serves the partner origin on its own listener. Its
// verifier belongs to the external issuer, and its mux serves only references.
func NewPartnerHandler(verifier TokenVerifier, references PartnerReferences, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/reference/{asset}", func(w http.ResponseWriter, r *http.Request) {
		correlation := ids.NewUUID()
		w.Header().Set("X-Correlation-ID", correlation)
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		token, _, wellFormed := bearerToken(r)
		if !wellFormed {
			refusePartnerCredentials(w)
			return
		}
		caller, err := verifier.Verify(ctx, token)
		switch {
		case errors.Is(err, identity.ErrKeySetUnavailable):
			http.Error(w, "Reference temporarily unavailable", http.StatusServiceUnavailable)
			return
		case err != nil:
			refusePartnerCredentials(w)
			return
		}
		asset := r.PathValue("asset")
		if !catalogdomain.ValidIdentifier(asset) {
			http.NotFound(w, r)
			return
		}
		artifact, err := references.PartnerArtifact(ctx, publicationdomain.Principal(caller), asset, correlation)
		switch {
		case errors.Is(err, publication.ErrNoVisibleArtifact):
			http.NotFound(w, r)
		case err != nil:
			logger.WarnContext(ctx, "partner reference unavailable", "correlation_id", correlation, "error", err)
			http.Error(w, "Reference temporarily unavailable", http.StatusServiceUnavailable)
		default:
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="openapi-source.json"`)
			w.Header().Set("Content-Length", strconv.Itoa(len(artifact.Payload)))
			w.Header().Set("X-Artifact-Digest", artifact.Digest)
			_, _ = w.Write(artifact.Payload)
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func refusePartnerCredentials(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="CargoPai Developers"`)
	http.Error(w, "Authentication required", http.StatusUnauthorized)
}
