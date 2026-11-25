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
	Operations                                          []operation
	Notice                                              string
}

type operation struct {
	Anchor, Method, Path, Summary, Description string
	Responses                                  []response
}

type response struct {
	Code, Description string
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
			if op.Responses != nil {
				responses := op.Responses.Map()
				for _, code := range slices.Sorted(maps.Keys(responses)) {
					name := ""
					if ref := responses[code]; ref != nil && ref.Value != nil && ref.Value.Description != nil {
						name = *ref.Value.Description
					}
					entry.Responses = append(entry.Responses, response{Code: code, Description: name})
				}
			}
			view.Operations = append(view.Operations, entry)
		}
	}
	return view, nil
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
