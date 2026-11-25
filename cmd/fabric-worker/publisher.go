package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"go.idp.xyz/idp-bento-go/eventing"
)

type httpPublisher struct {
	client   *http.Client
	endpoint string
	token    string
}

func newHTTPPublisher(address, token string) (*httpPublisher, error) {
	endpoint, err := url.Parse(address)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return nil, errors.New("IDP_FABRIC_OUTBOX_URL must be an absolute URL without credentials, query or fragment")
	}
	if endpoint.Scheme != "https" && (endpoint.Scheme != "http" || !net.ParseIP(endpoint.Hostname()).IsLoopback()) {
		return nil, errors.New("IDP_FABRIC_OUTBOX_URL requires HTTPS or a loopback HTTP address")
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return nil, errors.New("IDP_FABRIC_OUTBOX_TOKEN must be a single bearer token")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &httpPublisher{
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		endpoint: endpoint.String(),
		token:    token,
	}, nil
}

func (p *httpPublisher) Publish(ctx context.Context, event eventing.Envelope) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return eventing.ErrPublishUncertain
	}
	request.Header.Set("Content-Type", eventing.JSONContentType)
	if p.token != "" {
		request.Header.Set("Authorization", "Bearer "+p.token)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return eventing.ErrPublishUncertain
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%w: HTTP %d", eventing.ErrPublishRejected, response.StatusCode)
	}
	return nil
}
