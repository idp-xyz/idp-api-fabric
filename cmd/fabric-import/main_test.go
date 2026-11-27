package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	productID = "11111111-1111-4111-8111-111111111111"
	assetID   = "22222222-2222-4222-8222-222222222222"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func paths(upload sourceUpload) []string {
	names := make([]string, len(upload.Files))
	for i, file := range upload.Files {
		names[i] = file.Path
	}
	return names
}

func TestCollectTakesTheContractFilesOfTheFolder(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"openapi.yaml":      "openapi: 3.1.2\n",
		"schemas/item.json": "{}",
		"README.md":         "# not a contract\n",
		".git/config.json":  "{}",
		".hidden.yaml":      "a: b\n",
	})
	upload, err := collect(root, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(paths(upload), " "); got != "openapi.yaml schemas/item.json" || upload.Entrypoint != "openapi.yaml" {
		t.Errorf("collected %q with entrypoint %q", got, upload.Entrypoint)
	}
	content, _ := base64.StdEncoding.DecodeString(upload.Files[0].ContentBase64)
	if string(content) != "openapi: 3.1.2\n" {
		t.Errorf("the entrypoint was sent as %q", content)
	}

	// From the top of a repository the paths are the paths in it.
	upload, err = collect(filepath.Join(root, "schemas"), root, "item.json")
	if err != nil || upload.Entrypoint != "schemas/item.json" || strings.Join(paths(upload), " ") != "schemas/item.json" {
		t.Errorf("collected %v with entrypoint %q: %v", paths(upload), upload.Entrypoint, err)
	}
}

func TestCollectRefusesWhatTheImportWouldRefuse(t *testing.T) {
	many := map[string]string{}
	for i := range maxFiles + 1 {
		many["part"+string(rune('a'+i))+".yaml"] = "a: b\n"
	}
	for name, c := range map[string]struct {
		files map[string]string
		entry string
		want  string
	}{
		"no contract":       {map[string]string{"README.md": "x"}, "", "holds no YAML or JSON file"},
		"no entrypoint":     {map[string]string{"api.yaml": "a: b\n"}, "", "name the entrypoint with -entry"},
		"unknown entry":     {map[string]string{"openapi.yaml": "a: b\n"}, "other.yaml", "is not one of the contract files"},
		"too many files":    {many, "", "at most 16"},
		"too many bytes":    {map[string]string{"openapi.yaml": strings.Repeat("a", maxBytes+1)}, "", "at most 40960"},
		"not UTF-8":         {map[string]string{"openapi.yaml": "title: caf\xe9\n"}, "", "is not UTF-8 text"},
		"path out of range": {map[string]string{"open api.yaml": "a: b\n"}, "open api.yaml", "a path has letters"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, c.files)
			if _, err := collect(root, root, c.entry); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want an error saying %q", err, c.want)
			}
		})
	}
}

func TestARemoteIsWrittenAsTheImportTakesARepository(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:idp-xyz/contracts.git":                       "https://github.com/idp-xyz/contracts",
		"ssh://git@GitLab.Example.com:2222/team/sub/contracts.git":   "https://gitlab.example.com/team/sub/contracts",
		"https://x-access-token:secret@github.com/idp-xyz/contracts": "https://github.com/idp-xyz/contracts",
		"https://git.example.com:8443/idp/contracts.git/":            "https://git.example.com:8443/idp/contracts",
		"http://git.example.com/idp/contracts":                       "",
		"file:///srv/git/contracts":                                  "",
		"https://x-access-token:secret@github.com/idp-xyz/a?b":       "",
		"https://github.com/":                                        "",
	} {
		got, err := repositoryOf(remote)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("repositoryOf(%q) = %q, %v; want %q", remote, got, err, want)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("the error for %q shows the credentials: %v", remote, err)
		}
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=CI", "-c", "user.email=ci@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo makes a repository with a contract under contracts/rates, committed on
// main, whose remote is an SSH remote of GitHub.
func repo(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	write(t, root, map[string]string{"contracts/rates/openapi.yaml": "openapi: 3.1.2\n", "contracts/rates/item.yaml": "type: object\n"})
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "rates")
	gitIn(t, root, "remote", "add", "origin", "git@github.com:idp-xyz/contracts.git")
	return root, gitIn(t, root, "rev-parse", "HEAD")
}

func TestTheOriginIsWhatWasCheckedOut(t *testing.T) {
	root, commit := repo(t)
	dir := filepath.Join(root, "contracts", "rates")
	none := func(string) string { return "" }

	got, err := readOrigin(context.Background(), dir, "origin", none)
	if err != nil {
		t.Fatal(err)
	}
	want := gitOrigin{Repository: "https://github.com/idp-xyz/contracts", Commit: commit, Ref: "refs/heads/main"}
	if got.origin != want {
		t.Errorf("origin %+v, want %+v", got.origin, want)
	}
	upload, err := collect(dir, got.root, "")
	if err != nil || upload.Entrypoint != "contracts/rates/openapi.yaml" {
		t.Fatalf("collected %v with entrypoint %q: %v", paths(upload), upload.Entrypoint, err)
	}
	if err := committed(context.Background(), got.root, commit, upload.Files, os.ReadFile); err != nil {
		t.Errorf("the committed files were refused: %v", err)
	}

	// GitHub Actions names the ref; a detached checkout of no branch or tag has none.
	actions := func(name string) string { return map[string]string{"GITHUB_REF": "refs/pull/7/merge"}[name] }
	if got, _ := readOrigin(context.Background(), dir, "origin", actions); got.origin.Ref != "refs/pull/7/merge" {
		t.Errorf("in Actions the ref is %q", got.origin.Ref)
	}
	gitIn(t, root, "checkout", "-q", "--detach")
	if got, _ := readOrigin(context.Background(), dir, "origin", none); got.origin.Ref != "" {
		t.Errorf("a detached checkout declared %q", got.origin.Ref)
	}
	gitIn(t, root, "tag", "v1.0.0")
	if got, _ := readOrigin(context.Background(), dir, "origin", none); got.origin.Ref != "refs/tags/v1.0.0" {
		t.Errorf("a tagged checkout declared %q", got.origin.Ref)
	}
	if _, err := readOrigin(context.Background(), dir, "upstream", none); err == nil {
		t.Error("a missing remote was taken")
	}
}

func TestFilesThatDifferFromTheCommitAreRefused(t *testing.T) {
	root, commit := repo(t)
	dir := filepath.Join(root, "contracts", "rates")
	for name, change := range map[string]map[string]string{
		"changed":   {"contracts/rates/item.yaml": "type: string\n"},
		"untracked": {"contracts/rates/extra.yaml": "type: string\n"},
	} {
		t.Run(name, func(t *testing.T) {
			write(t, root, change)
			defer gitIn(t, root, "checkout", "-q", "--", ".")
			defer os.Remove(filepath.Join(dir, "extra.yaml"))
			upload, err := collect(dir, root, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := committed(context.Background(), root, commit, upload.Files, os.ReadFile); err == nil || !strings.Contains(err.Error(), "commit it first") {
				t.Errorf("got %v, want the file refused", err)
			}
		})
	}
}

func TestRunImportsByKeysAndSaysWhetherTheRevisionIsNew(t *testing.T) {
	root, commit := repo(t)
	var imported []map[string]any
	status := http.StatusCreated
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer example-access-token" {
			t.Errorf("%s %s without the token", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/products":
			if r.URL.Query().Get("page_token") == "" {
				_, _ = io.WriteString(w, `{"items":[{"product_id":"00000000-0000-4000-8000-000000000000","product_key":"parcel"}],"next_page_token":"p2"}`)
				return
			}
			_, _ = io.WriteString(w, `{"items":[{"product_id":"`+productID+`","product_key":"cargopai"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/products/"+productID+"/assets":
			if r.URL.Query().Get("q") != "rates-api" {
				t.Errorf("assets were searched for %q", r.URL.Query().Get("q"))
			}
			_, _ = io.WriteString(w, `{"items":[{"api_asset_id":"33333333-3333-4333-8333-333333333333","asset_key":"rates-api-v2"},{"api_asset_id":"`+assetID+`","asset_key":"rates-api"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/products/"+productID+"/assets/"+assetID+"/revisions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			imported = append(imported, body)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"revision_id":"44444444-4444-4444-8444-444444444444","revision_sequence":3,"status":"DRAFT","entrypoint":"contracts/rates/openapi.yaml","source_digest":"sha256:ab"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	env := map[string]string{"IDP_FABRIC_TOKEN": "example-access-token", "IDP_FABRIC_API_URL": server.URL}
	getenv := func(name string) string { return env[name] }
	args := []string{"-product", "cargopai", "-asset", "rates-api", "-dir", filepath.Join(root, "contracts", "rates"), "-git"}

	var out bytes.Buffer
	if err := run(context.Background(), args, getenv, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if want := "revision #3 44444444-4444-4444-8444-444444444444 DRAFT, recorded now: entrypoint contracts/rates/openapi.yaml, 2 files, sha256:ab\n"; out.String() != want {
		t.Errorf("printed %q, want %q", out.String(), want)
	}
	origin, _ := imported[0]["git_origin"].(map[string]any)
	if imported[0]["entrypoint"] != "contracts/rates/openapi.yaml" || origin["repository"] != "https://github.com/idp-xyz/contracts" ||
		origin["commit"] != commit || origin["ref"] != "refs/heads/main" {
		t.Errorf("imported %v", imported[0])
	}

	status = http.StatusOK
	out.Reset()
	if err := run(context.Background(), append(args[:len(args)-1], "-product", productID, "-asset", assetID), getenv, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "recorded before") || imported[1]["git_origin"] != nil {
		t.Errorf("printed %q and imported %v, want an upload recorded before", out.String(), imported[1])
	}
}

func TestRunSaysWhatTheServerRefused(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"openapi.yaml": "openapi: 3.0.3\n"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"type":"about:blank","title":"The entrypoint must be OpenAPI 3.1.2.","status":422,"error_code":"UNSUPPORTED_SPEC_VERSION","trace_id":"t-1"}`)
	}))
	defer server.Close()
	getenv := func(name string) string {
		return map[string]string{"IDP_FABRIC_TOKEN": "example-access-token"}[name]
	}
	err := run(context.Background(), []string{"-api", server.URL, "-product", productID, "-asset", assetID, "-dir", root}, getenv, io.Discard, io.Discard)
	if err == nil || err.Error() != "422 UNSUPPORTED_SPEC_VERSION: The entrypoint must be OpenAPI 3.1.2. (trace t-1)" {
		t.Errorf("got %v", err)
	}
}

func TestTheTokenGoesOnlyWhereItCannotBeRead(t *testing.T) {
	for api, ok := range map[string]bool{
		"https://fabric.example":      true,
		"http://127.0.0.1:8080":       true,
		"http://localhost:8080/":      true,
		"http://[::1]:8080":           true,
		"http://fabric.example":       false,
		"https://user@fabric.example": false,
		"https://fabric.example/v1":   false,
		"fabric.example":              false,
	} {
		if _, err := newClient(api, "token", http.DefaultClient); (err == nil) != ok {
			t.Errorf("newClient(%q): %v", api, err)
		}
	}
	err := run(context.Background(), []string{"-api", "https://fabric.example", "-product", "p", "-asset", "a"},
		func(string) string { return "" }, io.Discard, io.Discard)
	if err == nil || err.Error() != "IDP_FABRIC_TOKEN is not set" {
		t.Errorf("got %v without a token", err)
	}
}
