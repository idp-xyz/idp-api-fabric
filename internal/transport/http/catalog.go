package http

import (
	"context"
	"errors"
	"net/http"

	catalog "go.idp.xyz/idp-api-fabric/internal/apicatalog/application"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

type APICatalog interface {
	RegisterAsset(context.Context, catalog.RegisterAsset) (domain.Asset, error)
	ReadAsset(context.Context, catalog.GetAsset) (catalog.AssetView, error)
	ListAssets(context.Context, catalog.ListAssets) (catalog.Page, error)
}

func catalogRequest(ctx context.Context, product string) (catalog.Request, error) {
	principal, correlationID, err := caller(ctx)
	return catalog.Request{
		Caller: domain.Principal(principal), ProductID: domain.ProductID(product), CorrelationID: correlationID,
	}, err
}

func (o operations) RegisterAsset(ctx context.Context, request RegisterAssetRequestObject) (RegisterAssetResponseObject, error) {
	base, err := catalogRequest(ctx, request.ProductID)
	if err != nil {
		return nil, err
	}
	asset, err := o.catalog.RegisterAsset(ctx, catalog.RegisterAsset{
		Request: base, WorkspaceID: domain.WorkspaceID(request.Body.WorkspaceID),
		Key: request.Body.AssetKey, DisplayName: request.Body.DisplayName,
		DomainKey: request.Body.DomainKey, Kind: domain.Kind(request.Body.APIKind),
	})
	if err != nil {
		return nil, err
	}
	body, err := assetOf(asset)
	if err != nil {
		return nil, err
	}
	return RegisterAsset201JSONResponse{Body: body, Headers: RegisterAsset201ResponseHeaders{
		Location: "/v1/products/" + body.ProductID + "/assets/" + body.APIAssetID, CacheControl: noStore,
	}}, nil
}

func (o operations) GetAsset(ctx context.Context, request GetAssetRequestObject) (GetAssetResponseObject, error) {
	base, err := catalogRequest(ctx, request.ProductID)
	if err != nil {
		return nil, err
	}
	view, err := o.catalog.ReadAsset(ctx, catalog.GetAsset{Request: base, AssetID: domain.AssetID(request.APIAssetID)})
	if err != nil {
		return nil, err
	}
	body, err := assetViewOf(view)
	if err != nil {
		return nil, err
	}
	return GetAsset200JSONResponse{Body: body, Headers: GetAsset200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) ListAssets(ctx context.Context, request ListAssetsRequestObject) (ListAssetsResponseObject, error) {
	base, err := catalogRequest(ctx, request.ProductID)
	if err != nil {
		return nil, err
	}
	params := request.Params
	size, position, err := paging(listOfAssets, params.PageSize, params.PageToken)
	if err != nil {
		return nil, err
	}
	var after *ports.Position
	if position != nil {
		after = &ports.Position{At: position.At, ID: domain.AssetID(position.ID)}
	}
	page, err := o.catalog.ListAssets(ctx, catalog.ListAssets{
		Request: base, WorkspaceID: domain.WorkspaceID(valueOf(params.WorkspaceID)),
		Kind: domain.Kind(valueOf(params.APIKind)), DomainKey: valueOf(params.DomainKey), Query: valueOf(params.Q),
		CheckStatus: domain.CheckStatus(valueOf(params.CheckStatus)), ReleasePending: params.ReleasePending != nil && bool(*params.ReleasePending),
		PageSize: size, After: after,
	})
	if err != nil {
		return nil, err
	}
	items, err := listed(page.Items, assetViewOf)
	if err != nil {
		return nil, err
	}
	var next *NextPageToken
	if page.Next != nil {
		next = nextPageToken(listOfAssets, &governanceports.Position{At: page.Next.At, ID: string(page.Next.ID)})
	}
	return ListAssets200JSONResponse{
		Body: AssetList{Items: items, NextPageToken: next}, Headers: ListAssets200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func valueOf[T ~string](value *T) T {
	if value == nil {
		return ""
	}
	return *value
}

func assetOf(asset domain.Asset) (APIAsset, error) {
	kind, lifecycle := AssetKind(asset.Kind), APIAssetLifecycle(asset.Lifecycle)
	if !kind.Valid() || !lifecycle.Valid() {
		return APIAsset{}, errors.New("the API asset kind or lifecycle is absent from the contract")
	}
	return APIAsset{
		APIAssetID: string(asset.ID), ProductID: string(asset.ProductID), WorkspaceID: string(asset.WorkspaceID),
		AssetKey: asset.Key, DisplayName: asset.DisplayName, DomainKey: asset.DomainKey,
		APIKind: kind, Lifecycle: lifecycle, CreatedAt: asset.CreatedAt,
	}, nil
}

func catalogProblem(err error) (Problem, bool) {
	switch {
	case errors.Is(err, catalog.ErrProductNotFound):
		return problem(http.StatusNotFound, CodeProductNotFound, ""), true
	case errors.Is(err, catalog.ErrWorkspaceNotFound):
		return problem(http.StatusNotFound, CodeWorkspaceNotFound, ""), true
	case errors.Is(err, catalog.ErrAssetNotFound):
		return problem(http.StatusNotFound, "API_ASSET_NOT_FOUND", ""), true
	case errors.Is(err, catalog.ErrForbidden):
		return problem(http.StatusForbidden, CodeForbidden, "The caller may not perform this action."), true
	case errors.Is(err, ports.ErrAssetKeyTaken):
		return problem(http.StatusConflict, "API_ASSET_KEY_TAKEN", "The product has an API asset with this key already."), true
	case errors.Is(err, catalog.ErrProductSuspended):
		return problem(http.StatusConflict, CodeProductNotActive, "The product is suspended and takes no new work."), true
	case errors.Is(err, domain.ErrInvalid):
		return problem(http.StatusUnprocessableEntity, CodeRequestUnprocessable, ""), true
	default:
		return Problem{}, false
	}
}
