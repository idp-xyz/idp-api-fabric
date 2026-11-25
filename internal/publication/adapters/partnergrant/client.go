// Package partnergrant reads current document access from a grant authority.
package partnergrant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

const maxDecisionBytes = 4096

type Client struct {
	endpoint string
	token    string
	http     *http.Client
}

func NewClient(address, token string) (*Client, error) {
	endpoint, err := url.Parse(address)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return nil, errors.New("partner grant authority needs a URL without credentials, query or fragment")
	}
	if endpoint.Scheme != "https" && (endpoint.Scheme != "http" || !net.ParseIP(endpoint.Hostname()).IsLoopback()) {
		return nil, errors.New("partner grant authority needs HTTPS or a loopback HTTP address")
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return nil, errors.New("partner grant authority credential must be a single bearer token")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Client{
		endpoint: endpoint.String(),
		token:    token,
		http: &http.Client{
			Transport: transport,
			Timeout:   3 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

type grantCheck struct {
	Issuer      string `json:"issuer"`
	Subject     string `json:"subject"`
	ProductID   string `json:"product_id"`
	TargetID    string `json:"target_id"`
	AssetID     string `json:"api_asset_id"`
	Audience    string `json:"audience"`
	Environment string `json:"environment"`
	Action      string `json:"action"`
}

func (c *Client) Allowed(ctx context.Context, request ports.PartnerGrantRequest) (bool, error) {
	input, err := json.Marshal(grantCheck{
		Issuer: request.Caller.Issuer, Subject: request.Caller.Subject, ProductID: request.ProductID,
		TargetID: request.TargetID, AssetID: request.AssetID, Audience: "partner", Environment: request.Environment,
		Action: "contract.download",
	})
	if err != nil {
		return false, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(input))
	if err != nil {
		return false, errors.New("cannot build the partner grant request")
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if c.token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return false, errors.New("partner grant authority could not be reached")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("partner grant authority responded with HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxDecisionBytes+1))
	if err != nil || len(raw) > maxDecisionBytes {
		return false, errors.New("partner grant authority returned an unreadable decision")
	}
	var decision struct {
		Allowed *bool `json:"allowed"`
	}
	if err := json.Unmarshal(raw, &decision); err != nil || decision.Allowed == nil {
		return false, errors.New("partner grant authority returned an invalid decision")
	}
	return *decision.Allowed, nil
}
