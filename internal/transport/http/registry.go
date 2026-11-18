package http

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"

	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

type ContractRegistry interface {
	ImportRevision(context.Context, registry.ImportRevision) (ports.Registration, error)
	GetRevision(context.Context, ports.ScopeQuery, string) (domain.Revision, error)
	ListRevisions(context.Context, registry.ListRevisions) (registry.Page, error)
}

func revisionScope(ctx context.Context, product, asset string) (ports.ScopeQuery, error) {
	principal, correlationID, err := caller(ctx)
	return ports.ScopeQuery{
		Caller: domain.Principal(principal), ProductID: product, AssetID: asset, CorrelationID: correlationID,
	}, err
}

var errInvalidSourceEncoding = errors.New("a source file does not use standard Base64")

func (o operations) ImportRevision(ctx context.Context, request ImportRevisionRequestObject) (ImportRevisionResponseObject, error) {
	scope, err := revisionScope(ctx, request.ProductID, request.APIAssetID)
	if err != nil {
		return nil, err
	}
	files := make([]domain.SourceFile, 0, len(request.Body.Files))
	for _, file := range request.Body.Files {
		content, err := base64.StdEncoding.Strict().DecodeString(file.ContentBase64)
		if err != nil {
			return nil, errInvalidSourceEncoding
		}
		files = append(files, domain.SourceFile{Path: file.Path, Content: string(content)})
	}
	registered, err := o.registry.ImportRevision(ctx, registry.ImportRevision{
		ScopeQuery: scope, Entrypoint: request.Body.Entrypoint, Files: files,
	})
	if err != nil {
		return nil, err
	}
	body := revisionOf(registered.Revision)
	if registered.Created {
		location := "/v1/products/" + body.ProductID + "/assets/" + body.APIAssetID + "/revisions/" + body.RevisionID
		return ImportRevision201JSONResponse{Body: body, Headers: ImportRevision201ResponseHeaders{
			CacheControl: noStore, Location: location,
		}}, nil
	}
	return ImportRevision200JSONResponse{Body: body, Headers: ImportRevision200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) GetRevision(ctx context.Context, request GetRevisionRequestObject) (GetRevisionResponseObject, error) {
	scope, err := revisionScope(ctx, request.ProductID, request.APIAssetID)
	if err != nil {
		return nil, err
	}
	revision, err := o.registry.GetRevision(ctx, scope, request.RevisionID)
	if err != nil {
		return nil, err
	}
	return GetRevision200JSONResponse{
		Body: revisionOf(revision), Headers: GetRevision200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func (o operations) ListRevisions(ctx context.Context, request ListRevisionsRequestObject) (ListRevisionsResponseObject, error) {
	scope, err := revisionScope(ctx, request.ProductID, request.APIAssetID)
	if err != nil {
		return nil, err
	}
	list := "revisions/" + request.ProductID + "/" + request.APIAssetID
	size, position, err := paging(list, request.Params.PageSize, request.Params.PageToken)
	if err != nil {
		return nil, err
	}
	var after *ports.Position
	if position != nil {
		after = &ports.Position{At: position.At, ID: position.ID}
	}
	page, err := o.registry.ListRevisions(ctx, registry.ListRevisions{ScopeQuery: scope, PageSize: size, After: after})
	if err != nil {
		return nil, err
	}
	items := make([]ContractRevision, len(page.Items))
	for i, revision := range page.Items {
		items[i] = revisionOf(revision)
	}
	var next *NextPageToken
	if page.Next != nil {
		next = nextPageToken(list, &governanceports.Position{At: page.Next.At, ID: page.Next.ID})
	}
	return ListRevisions200JSONResponse{
		Body: ContractRevisionList{Items: items, NextPageToken: next}, Headers: ListRevisions200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func revisionOf(revision domain.Revision) ContractRevision {
	return ContractRevision{
		RevisionID: revision.ID, SourceSnapshotID: revision.SourceSnapshotID, ProductID: revision.Asset.ProductID,
		APIAssetID: revision.Asset.AssetID, RevisionSequence: revision.Sequence, Status: ContractRevisionStatus(revision.Status),
		SourceKind: ContractRevisionSourceKind(revision.SourceKind), Entrypoint: revision.Entrypoint, SpecVersion: ContractRevisionSpecVersion(revision.SpecVersion),
		SourceDigest: revision.SourceDigest, RefClosureDigest: revision.RefClosureDigest, CreatedAt: revision.CreatedAt,
	}
}

func registryProblem(err error) (Problem, bool) {
	switch {
	case errors.Is(err, errInvalidSourceEncoding):
		return problem(http.StatusBadRequest, CodeRequestInvalid, "A contract file must contain standard Base64."), true
	case errors.Is(err, registry.ErrForbidden):
		return problem(http.StatusForbidden, CodeForbidden, "The caller is not a current internal author of the workspace."), true
	case errors.Is(err, registry.ErrRevisionNotFound):
		return problem(http.StatusNotFound, "REVISION_NOT_FOUND", ""), true
	case errors.Is(err, registry.ErrInvalidPage):
		return problem(http.StatusBadRequest, CodeRequestInvalid, ""), true
	case errors.Is(err, registry.ErrProductSuspended):
		return problem(http.StatusConflict, CodeProductNotActive, "The product is suspended and takes no new work."), true
	case errors.Is(err, registry.ErrAssetKind):
		return problem(http.StatusConflict, "API_ASSET_KIND_UNSUPPORTED", "The API asset does not accept OpenAPI revisions."), true
	case errors.Is(err, domain.ErrUnsupportedSpecVersion):
		return problem(http.StatusUnprocessableEntity, "UNSUPPORTED_SPEC_VERSION", "The entrypoint must be OpenAPI 3.1.2."), true
	case errors.Is(err, domain.ErrInvalidSource):
		return problem(http.StatusUnprocessableEntity, "CONTRACT_SOURCE_INVALID", "The source bundle violates the import requirements."), true
	default:
		return Problem{}, false
	}
}
