package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// The bounds of importRevision: a request over them is refused, so it is not
// sent.
const (
	maxFiles = 16
	maxBytes = 40960
)

var (
	sourcePath   = regexp.MustCompile(`^[a-zA-Z0-9._-]+(/[a-zA-Z0-9._-]+)*$`)
	contractFile = regexp.MustCompile(`(?i)\.(ya?ml|json)$`)
	entryNames   = []string{"openapi.yaml", "openapi.yml", "openapi.json"}
)

type sourceFile struct {
	Path          string `json:"path"`
	ContentBase64 string `json:"content_base64"`
}

type gitOrigin struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Ref        string `json:"ref,omitempty"`
}

type sourceUpload struct {
	Entrypoint string       `json:"entrypoint"`
	Files      []sourceFile `json:"files"`
	GitOrigin  *gitOrigin   `json:"git_origin,omitempty"`
}

// collect reads the contract files under dir, named by their paths from base:
// dir itself for an upload, the top of the repository for a Git source. A
// hidden folder, such as .git, is not entered.
func collect(dir, base, entry string) (sourceUpload, error) {
	var upload sourceUpload
	// The top of a repository comes from Git with its links resolved.
	root, err := realPath(dir)
	if err != nil {
		return upload, err
	}
	top, err := realPath(base)
	if err != nil {
		return upload, err
	}
	total := 0
	err = filepath.WalkDir(root, func(path string, item fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && strings.HasPrefix(item.Name(), ".") {
			if item.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if item.IsDir() || !contractFile.MatchString(item.Name()) {
			return nil
		}
		if !item.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		name, err := filepath.Rel(top, path)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		if !sourcePath.MatchString(name) || len(name) > 200 {
			return fmt.Errorf("%s: a path has letters, digits, '.', '_' and '-' only, and at most 200 characters", name)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !utf8.Valid(content) {
			return fmt.Errorf("%s is not UTF-8 text", name)
		}
		total += len(content)
		upload.Files = append(upload.Files, sourceFile{Path: name, ContentBase64: base64.StdEncoding.EncodeToString(content)})
		return nil
	})
	if err != nil {
		return upload, err
	}
	switch {
	case len(upload.Files) == 0:
		return upload, fmt.Errorf("%s holds no YAML or JSON file", dir)
	case len(upload.Files) > maxFiles:
		return upload, fmt.Errorf("%s holds %d contract files; a revision takes at most %d", dir, len(upload.Files), maxFiles)
	case total > maxBytes:
		return upload, fmt.Errorf("the contract files of %s hold %d bytes; a revision takes at most %d", dir, total, maxBytes)
	}

	prefix, err := filepath.Rel(top, root)
	if err != nil {
		return upload, err
	}
	paths := make([]string, len(upload.Files))
	for i, file := range upload.Files {
		paths[i] = file.Path
	}
	at := func(name string) string { return filepath.ToSlash(filepath.Join(prefix, name)) }
	if entry != "" {
		upload.Entrypoint = at(filepath.FromSlash(entry))
		if !slices.Contains(paths, upload.Entrypoint) {
			return upload, fmt.Errorf("the entrypoint %s is not one of the contract files of %s", entry, dir)
		}
		return upload, nil
	}
	for _, name := range entryNames {
		if slices.Contains(paths, at(name)) {
			upload.Entrypoint = at(name)
			return upload, nil
		}
	}
	return upload, errors.New("no openapi.yaml, .yml or .json at the top of the folder; name the entrypoint with -entry")
}

func realPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}
