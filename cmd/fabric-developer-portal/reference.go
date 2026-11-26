package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	bundle "go.idp.xyz/idp-api-fabric/internal/contractregistry/adapters/openapi"
	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

type reference struct {
	Title, Version, OpenAPI, Description, Query, Digest string
	Servers                                             []server
	Operations                                          []operation
	Notice                                              string
}

type server struct {
	URL, Description string
}

type operation struct {
	Anchor, Method, Path, Summary, Description string
	Parameters                                 []parameter
	RequestBody                                *requestBody
	Responses                                  []response
	Samples                                    []sample
}

type parameter struct {
	Name, In, Type, Description string
	Required                    bool
}

type requestBody struct {
	Description string
	Required    bool
	Contents    []content
}

type response struct {
	Code, Description string
	Contents          []content
}

type content struct {
	MediaType string
	Problem   bool
	Fields    []field
}

func readReference(artifact artifact, query string) (reference, error) {
	var archive struct {
		Format     string `json:"format"`
		Entrypoint string `json:"entrypoint"`
		Files      []struct {
			Path    string `json:"path"`
			Content []byte `json:"content_base64"`
		}
	}
	if err := json.Unmarshal(artifact.payload, &archive); err != nil || archive.Format != registry.ArtifactFormat {
		return reference{}, errors.New("invalid Fabric source bundle")
	}
	sources := make([]registry.SourceFile, 0, len(archive.Files))
	files := make(map[string][]byte, len(archive.Files))
	for _, file := range archive.Files {
		sources = append(sources, registry.SourceFile{Path: file.Path, Content: string(file.Content)})
		files["/"+file.Path] = file.Content
	}
	if _, err := bundle.Capture(archive.Entrypoint, sources); err != nil {
		return reference{}, err
	}
	entrypoint := &url.URL{Scheme: "https", Host: "fabric-bundle.invalid", Path: "/" + archive.Entrypoint}
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		if file, ok := files[location.Path]; ok && location.Scheme == entrypoint.Scheme &&
			location.Host == entrypoint.Host && location.RawQuery == "" && location.User == nil {
			return file, nil
		}
		return nil, errors.New("reference outside the captured source bundle")
	}
	doc, err := loader.LoadFromURI(entrypoint)
	if err != nil {
		return reference{}, err
	}
	view := reference{Title: doc.Info.Title, Version: doc.Info.Version, OpenAPI: doc.OpenAPI,
		Description: doc.Info.Description, Query: query, Digest: artifact.digest}
	for _, s := range doc.Servers {
		if s != nil {
			view.Servers = append(view.Servers, server{URL: s.URL, Description: s.Description})
		}
	}
	if doc.Paths == nil {
		return view, nil
	}
	match := strings.ToLower(strings.TrimSpace(query))
	paths := doc.Paths.Map()
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		item := paths[path]
		if item == nil {
			continue
		}
		operations := item.Operations()
		for _, method := range slices.Sorted(maps.Keys(operations)) {
			op := operations[method]
			if op == nil || !strings.Contains(strings.ToLower(method+" "+path+" "+op.Summary), match) {
				continue
			}
			entry := operation{Anchor: fmt.Sprintf("operation-%d", len(view.Operations)+1),
				Method: method, Path: path, Summary: op.Summary, Description: op.Description}
			parameters := operationParameters(item.Parameters, op.Parameters)
			for _, p := range parameters {
				entry.Parameters = append(entry.Parameters, parameter{Name: p.Name, In: p.In,
					Type: parameterType(p), Description: p.Description, Required: p.Required})
			}
			if op.RequestBody != nil && op.RequestBody.Value != nil {
				body := op.RequestBody.Value
				entry.RequestBody = &requestBody{Description: body.Description, Required: body.Required,
					Contents: contents(body.Content)}
			}
			if op.Responses != nil {
				responses := op.Responses.Map()
				for _, code := range slices.Sorted(maps.Keys(responses)) {
					next := response{Code: code}
					if ref := responses[code]; ref != nil && ref.Value != nil {
						if ref.Value.Description != nil {
							next.Description = *ref.Value.Description
						}
						next.Contents = contents(ref.Value.Content)
					}
					entry.Responses = append(entry.Responses, next)
				}
			}
			entry.Samples = codeSamples(operationRequest(doc, method, path, parameters, op))
			view.Operations = append(view.Operations, entry)
		}
	}
	return view, nil
}

// operationParameters lists the path's shared parameters, each replaced by an
// operation parameter with the same name and location.
func operationParameters(shared, own openapi3.Parameters) []*openapi3.Parameter {
	var merged []*openapi3.Parameter
	for _, list := range []openapi3.Parameters{shared, own} {
		for _, ref := range list {
			if ref == nil || ref.Value == nil {
				continue
			}
			p := ref.Value
			if i := slices.IndexFunc(merged, func(m *openapi3.Parameter) bool { return m.Name == p.Name && m.In == p.In }); i >= 0 {
				merged[i] = p
			} else {
				merged = append(merged, p)
			}
		}
	}
	return merged
}

func parameterType(p *openapi3.Parameter) string {
	if p.Schema != nil && p.Schema.Value != nil {
		return schemaType(p.Schema.Value)
	}
	for _, name := range slices.Sorted(maps.Keys(p.Content)) {
		if media := p.Content[name]; media != nil && media.Schema != nil && media.Schema.Value != nil {
			return schemaType(media.Schema.Value)
		}
	}
	return "any"
}

func contents(mediaTypes openapi3.Content) []content {
	var listed []content
	for _, name := range slices.Sorted(maps.Keys(mediaTypes)) {
		media := mediaTypes[name]
		if media == nil {
			continue
		}
		base, _, _ := strings.Cut(name, ";")
		base = strings.ToLower(strings.TrimSpace(base))
		listed = append(listed, content{MediaType: name,
			Problem: base == "application/problem+json" || base == "application/problem+xml",
			Fields:  schemaFieldsOf(media.Schema)})
	}
	return listed
}

func newPortal(address, assetID string) (http.Handler, error) {
	upstream, err := newOrigin(address, assetID)
	if err != nil {
		return nil, err
	}
	page, err := template.ParseFS(web, "web/reference.html")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		published, status := upstream.fetch(r)
		if status != http.StatusOK {
			renderReference(w, page, status, unavailableReference(status))
			return
		}
		view, err := readReference(published, r.URL.Query().Get("q"))
		if err != nil {
			renderReference(w, page, http.StatusBadGateway, unavailableReference(http.StatusBadGateway))
			return
		}
		renderReference(w, page, http.StatusOK, view)
	})
	mux.HandleFunc("GET /openapi-source.json", func(w http.ResponseWriter, r *http.Request) {
		published, status := upstream.fetch(r)
		if status != http.StatusOK {
			renderReference(w, page, status, unavailableReference(status))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Artifact-Digest", published.digest)
		w.Header().Set("Content-Disposition", `attachment; filename="openapi-source.json"`)
		_, _ = w.Write(published.payload)
	})
	mux.HandleFunc("GET /reference.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, web, "web/reference.css")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	}), nil
}

func renderReference(w http.ResponseWriter, page *template.Template, status int, view reference) {
	var output bytes.Buffer
	if err := page.Execute(&output, view); err != nil {
		http.Error(w, "Reference temporarily unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = output.WriteTo(w)
}

func unavailableReference(status int) reference {
	switch status {
	case http.StatusUnauthorized:
		return reference{Title: "Sign in to CargoPai", Notice: "Sign in through your CargoPai partner portal and reopen this reference."}
	case http.StatusNotFound:
		return reference{Title: "Reference unavailable", Notice: "Contact your CargoPai representative to check your access."}
	default:
		return reference{Title: "Reference temporarily unavailable", Notice: "Try opening this reference again in a moment."}
	}
}
