package main

import (
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"time"

	catalog "go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

type origin struct {
	endpoint string
	client   *http.Client
}

type artifact struct {
	payload []byte
	digest  string
}

func newOrigin(address, assetID string) (origin, error) {
	endpoint, err := url.Parse(address)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" ||
		endpoint.Path != "" && endpoint.Path != "/" {
		return origin{}, errors.New("IDP_FABRIC_DEVELOPER_PORTAL_ORIGIN must be an origin without credentials, query or fragment")
	}
	if endpoint.Scheme != "https" && (endpoint.Scheme != "http" || !net.ParseIP(endpoint.Hostname()).IsLoopback()) {
		return origin{}, errors.New("IDP_FABRIC_DEVELOPER_PORTAL_ORIGIN requires HTTPS or a loopback HTTP address")
	}
	if !catalog.ValidIdentifier(assetID) {
		return origin{}, errors.New("IDP_FABRIC_DEVELOPER_PORTAL_ASSET_ID must be a Fabric asset UUID")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return origin{
		endpoint: endpoint.JoinPath("v1", "reference", assetID).String(),
		client: &http.Client{
			Transport: transport,
			Timeout:   12 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (o origin) fetch(browser *http.Request) (artifact, int) {
	authorization := browser.Header.Values("Authorization")
	if len(authorization) != 1 || authorization[0] == "" {
		return artifact{}, http.StatusUnauthorized
	}
	request, err := http.NewRequestWithContext(browser.Context(), http.MethodGet, o.endpoint, nil)
	if err != nil {
		return artifact{}, http.StatusBadGateway
	}
	request.Header.Set("Authorization", authorization[0])
	request.Header.Set("Accept", "application/octet-stream")
	response, err := o.client.Do(request)
	if err != nil {
		return artifact{}, http.StatusBadGateway
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusNotFound, http.StatusServiceUnavailable:
		return artifact{}, response.StatusCode
	case http.StatusOK:
	default:
		return artifact{}, http.StatusBadGateway
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/octet-stream" {
		return artifact{}, http.StatusBadGateway
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, registry.MaxArtifactBytes+1))
	digest := response.Header.Get("X-Artifact-Digest")
	if err != nil || len(body) > registry.MaxArtifactBytes || registry.VerifyArtifact(digest, body) != nil {
		return artifact{}, http.StatusBadGateway
	}
	return artifact{payload: body, digest: digest}, http.StatusOK
}
