package main

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
	"regexp"
)

var identifier = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type client struct {
	base  *url.URL
	token string
	http  *http.Client
}

type revision struct {
	ID           string `json:"revision_id"`
	Sequence     int    `json:"revision_sequence"`
	Status       string `json:"status"`
	Entrypoint   string `json:"entrypoint"`
	SourceDigest string `json:"source_digest"`
}

type problem struct {
	Status    int    `json:"status"`
	ErrorCode string `json:"error_code"`
	Title     string `json:"title"`
	TraceID   string `json:"trace_id"`
}

func (p problem) Error() string {
	text := fmt.Sprintf("%d %s: %s", p.Status, p.ErrorCode, p.Title)
	if p.TraceID != "" {
		text += " (trace " + p.TraceID + ")"
	}
	return text
}

// newClient refuses to send the token where it could be read on the way: over
// http to anything but this machine.
func newClient(api, token string, httpClient *http.Client) (*client, error) {
	base, err := url.Parse(api)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		base.Path != "" && base.Path != "/" {
		return nil, errors.New("-api must be the origin of the Fabric management API, such as https://fabric.example")
	}
	switch base.Scheme {
	case "https":
	case "http":
		host := base.Hostname()
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("-api over http is taken for a loopback address only; use https")
		}
	default:
		return nil, errors.New("-api must be an https URL")
	}
	base.Path = ""
	return &client{base: base, token: token, http: httpClient}, nil
}

// call sends one request and decodes its answer into out, or its problem
// into the error.
func (c *client) call(ctx context.Context, method, path string, query url.Values, body, out any) (int, error) {
	target := *c.base
	target.Path = path
	target.RawQuery = query.Encode()
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), payload)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	if response.StatusCode >= 300 {
		failure := problem{Status: response.StatusCode}
		if json.Unmarshal(answer, &failure) != nil || failure.ErrorCode == "" {
			failure.ErrorCode, failure.Title = "UNEXPECTED_RESPONSE", http.StatusText(response.StatusCode)
		}
		return response.StatusCode, failure
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return response.StatusCode, fmt.Errorf("%s %s answered %d with what is not its JSON", method, path, response.StatusCode)
	}
	return response.StatusCode, nil
}

// find pages through a list for the one item whose key is key.
func (c *client) find(ctx context.Context, path string, query url.Values, keyField, idField, key string) (string, error) {
	seen := map[string]bool{}
	query.Set("page_size", "100")
	for {
		var page struct {
			Items         []map[string]any `json:"items"`
			NextPageToken string           `json:"next_page_token"`
		}
		if _, err := c.call(ctx, http.MethodGet, path, query, nil, &page); err != nil {
			return "", err
		}
		for _, item := range page.Items {
			if item[keyField] == key {
				if id, ok := item[idField].(string); ok {
					return id, nil
				}
			}
		}
		if page.NextPageToken == "" {
			return "", nil
		}
		if seen[page.NextPageToken] {
			return "", fmt.Errorf("the list of %s repeats a page", path)
		}
		seen[page.NextPageToken] = true
		query.Set("page_token", page.NextPageToken)
	}
}

func (c *client) productID(ctx context.Context, product string) (string, error) {
	if identifier.MatchString(product) {
		return product, nil
	}
	id, err := c.find(ctx, "/v1/products", url.Values{}, "product_key", "product_id", product)
	if err == nil && id == "" {
		err = fmt.Errorf("no product with the key %q is visible to this token", product)
	}
	return id, err
}

func (c *client) assetID(ctx context.Context, product, asset string) (string, error) {
	if identifier.MatchString(asset) {
		return asset, nil
	}
	id, err := c.find(ctx, "/v1/products/"+url.PathEscape(product)+"/assets", url.Values{"q": {asset}}, "asset_key", "api_asset_id", asset)
	if err == nil && id == "" {
		err = fmt.Errorf("no API asset with the key %q is visible to this token", asset)
	}
	return id, err
}

// importRevision says whether the revision was recorded now (201) or was the
// one recorded before from the same files (200).
func (c *client) importRevision(ctx context.Context, product, asset string, upload sourceUpload) (revision, bool, error) {
	var recorded revision
	path := "/v1/products/" + url.PathEscape(product) + "/assets/" + url.PathEscape(asset) + "/revisions"
	status, err := c.call(ctx, http.MethodPost, path, nil, upload, &recorded)
	if err != nil {
		return recorded, false, err
	}
	if status != http.StatusCreated && status != http.StatusOK || recorded.ID == "" {
		return recorded, false, fmt.Errorf("the import answered %d without a revision", status)
	}
	return recorded, status == http.StatusCreated, nil
}
