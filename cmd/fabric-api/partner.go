package main

import (
	"context"
	"errors"

	catalog "go.idp.xyz/idp-api-fabric/internal/apicatalog/application"
	catalogdomain "go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/platform/identity"
	governance "go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/adapters/partnergrant"
	publication "go.idp.xyz/idp-api-fabric/internal/publication/application"
	publicationdomain "go.idp.xyz/idp-api-fabric/internal/publication/domain"
	publicationports "go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

type partnerSettings struct {
	address, productID, targetID string
	identity                     identity.Config
	grantURL, grantToken         string
}

func readPartnerSettings(getenv func(string) string) *partnerSettings {
	address := getenv("IDP_FABRIC_PARTNER_HTTP_ADDR")
	if address == "" {
		return nil
	}
	return &partnerSettings{
		address: address, productID: getenv("IDP_FABRIC_PARTNER_PRODUCT_ID"),
		targetID: getenv("IDP_FABRIC_PARTNER_TARGET_ID"),
		identity: identity.Config{
			Issuer:   getenv("IDP_FABRIC_PARTNER_OIDC_ISSUER"),
			Audience: getenv("IDP_FABRIC_PARTNER_OIDC_AUDIENCE"),
			JWKSURL:  getenv("IDP_FABRIC_PARTNER_OIDC_JWKS_URL"),
		},
		grantURL:   getenv("IDP_FABRIC_PARTNER_GRANT_URL"),
		grantToken: getenv("IDP_FABRIC_PARTNER_GRANT_TOKEN"),
	}
}

func (p *partnerSettings) bind(internalIssuer string, authority publicationAuthority) (*publication.PartnerAccess, *identity.Verifier, error) {
	if p == nil {
		return nil, nil, nil
	}
	if !catalogdomain.ValidIdentifier(p.productID) || !catalogdomain.ValidIdentifier(p.targetID) {
		return nil, nil, errors.New("IDP_FABRIC_PARTNER_PRODUCT_ID and IDP_FABRIC_PARTNER_TARGET_ID must be UUIDs")
	}
	if p.identity.Issuer == internalIssuer {
		return nil, nil, errors.New("IDP_FABRIC_PARTNER_OIDC_ISSUER must differ from the management issuer")
	}
	verifier, err := identity.NewVerifier(p.identity)
	if err != nil {
		return nil, nil, err
	}
	grants, err := partnergrant.NewClient(p.grantURL, p.grantToken)
	if err != nil {
		return nil, nil, err
	}
	return &publication.PartnerAccess{
		ProductID: p.productID, TargetID: p.targetID, ProductActive: authority.cargopaiActive,
		Allowed: grants.Allowed, ReadApproved: authority.readApprovedPartnerArtifact,
	}, verifier, nil
}

func (s publicationAuthority) cargopaiActive(ctx context.Context, productID string) (bool, error) {
	key, err := s.governance.ActiveProductKey(ctx, governancedomain.ProductID(productID))
	return key == "cargopai", err
}

func (s publicationAuthority) readApprovedPartnerArtifact(ctx context.Context, request publicationdomain.Request,
	target publicationdomain.Target, releaseID string) (publicationports.Artifact, error) {
	active, err := s.cargopaiActive(ctx, request.ProductID)
	if err != nil {
		return publicationports.Artifact{}, err
	}
	if !active {
		return publicationports.Artifact{}, publication.ErrNoVisibleArtifact
	}
	audience, environment, err := s.Config(ctx, request.ProductID, target.AudienceConfigID)
	switch {
	case errors.Is(err, governance.ErrAudienceConfigNotFound):
		return publicationports.Artifact{}, publication.ErrNoVisibleArtifact
	case err != nil:
		return publicationports.Artifact{}, err
	case audience != target.Audience || environment != target.Environment:
		return publicationports.Artifact{}, publication.ErrNoVisibleArtifact
	}
	asset, err := s.catalog.AssetInProduct(ctx, catalogdomain.ProductID(request.ProductID), catalogdomain.AssetID(request.AssetID))
	if errors.Is(err, catalog.ErrAssetNotFound) {
		return publicationports.Artifact{}, publication.ErrNoVisibleArtifact
	}
	if err != nil {
		return publicationports.Artifact{}, err
	}
	artifact, err := s.verify(ctx, assetOf(request, string(asset.WorkspaceID)), target, releaseID, "", registrydomain.Principal{}, false)
	if unapproved(err) {
		return publicationports.Artifact{}, publication.ErrNoVisibleArtifact
	}
	return artifact, err
}
