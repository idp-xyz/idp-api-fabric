package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

// downloaded is what a read of a release was answered: the status, the
// headers and the bytes of the answer as they came, which a decoded body
// would not keep.
type downloaded struct {
	status int
	header http.Header
	body   []byte
}

// code is the error code of an answer that is a problem, and empty for any
// other.
func (d downloaded) code() string {
	var problem struct {
		Code string `json:"error_code"`
	}
	if json.Unmarshal(d.body, &problem) != nil {
		return ""
	}
	return problem.Code
}

// download reads the path as the subject does, or without credentials if
// the subject is empty, and keeps the bytes of the answer.
func (s *stack) download(path, subject string) downloaded {
	s.t.Helper()

	request, err := http.NewRequestWithContext(s.t.Context(), http.MethodGet, s.server.URL+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	if subject != "" {
		request.Header.Set("Authorization", "Bearer "+s.token(subject))
	}
	response, err := s.server.Client().Do(request)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatalf("GET %s: read the answer: %v", path, err)
	}
	return downloaded{status: response.StatusCode, header: response.Header, body: data}
}

// sha256Of is the digest of the bytes, in the form the contract states.
func sha256Of(data []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(data)) }

// bundle is the files of an artifact as the artifact holds them.
type bundle struct {
	Format     string `json:"format"`
	Entrypoint string `json:"entrypoint"`
	Closure    string `json:"ref_closure_digest"`
	Files      []struct {
		Path    string `json:"path"`
		Content []byte `json:"content_base64"`
	} `json:"files"`
}

// holds fails the test unless the artifact is the format it says it is, of
// the one file given, under the entrypoint.
func (b bundle) holds(entrypoint, source string) bool {
	return b.Format == "fabric-openapi-source/v1" && b.Entrypoint == entrypoint &&
		len(b.Files) == 1 && b.Files[0].Path == entrypoint && string(b.Files[0].Content) == source
}

// releaseAt is where the release is read under its asset.
func (s *releaseFixture) releaseAt(id string) string { return s.asset + "/releases/" + id }

// A release is downloaded by its identifier, and what is downloaded is what
// the release recorded: the bytes that are kept under its digest, whose
// SHA-256 is that digest and is what the answer says it is. They are the
// bytes a slot serves of the same release and the files of the revision as
// they were imported, and whoever may read the release downloads the same.
func TestAReleaseIsDownloadedByItsIdentifierAsTheBytesItRecorded(t *testing.T) {
	s := publicationFor(t)
	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	_, _, id := s.released(revisionSource, "", "1.0.0")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, id, "", "first"), http.StatusOK, "")
	release := s.call(http.MethodGet, s.releaseAt(id), "author", "")
	s.require(release, http.StatusOK, "")
	digest := release.id("artifact_digest")

	var recorded []byte
	if err := s.owner.QueryRow(t.Context(), `SELECT payload FROM contractregistry.release_artifact WHERE artifact_digest = $1`, digest).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	served := s.download(slot+"/artifact", "author")
	if served.status != http.StatusOK || !bytes.Equal(served.body, recorded) {
		t.Fatalf("the slot serves %d %s, want the bytes the release recorded", served.status, served.body)
	}

	for _, reader := range []string{"author", "coauthor", "reviewer", "publisher"} {
		got := s.download(s.releaseAt(id)+"/artifact", reader)
		if got.status != http.StatusOK || !bytes.Equal(got.body, recorded) || sha256Of(got.body) != digest ||
			got.header.Get("X-Artifact-Digest") != digest || got.header.Get("Content-Type") != "application/octet-stream" ||
			got.header.Get("Cache-Control") != "no-store" || got.header.Get("Content-Length") != fmt.Sprint(len(recorded)) {
			t.Errorf("%s downloads %d %v %q, want the %d bytes of %s as an octet stream that is not cached",
				reader, got.status, got.header, got.body, len(recorded), digest)
		}
	}

	var files bundle
	got := s.download(s.releaseAt(id)+"/artifact", "author")
	if err := json.Unmarshal(got.body, &files); err != nil || !files.holds("openapi.yaml", revisionSource) ||
		files.Closure != release.id("ref_closure_digest") {
		t.Errorf("the artifact is %s (%v), want the file as it was imported under the closure of the release", got.body, err)
	}
}

// Every release of an asset is downloaded as itself: the first of two
// versions is not the one a slot serves any more, or none is, and it is
// still what it was prepared as, each under the digest of its own.
func TestTwoReleasesOfAnAssetAreDownloadedEachAsItself(t *testing.T) {
	s := publicationFor(t)
	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	secondSource := strings.ReplaceAll(revisionSource, "v1", "v2")
	firstRevision, _, first := s.released(revisionSource, "", "1.0.0")
	_, _, second := s.released(secondSource, firstRevision, "2.0.0")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, first, "", "first"), http.StatusOK, "")
	s.require(s.change(slot, "publisher", "ACTIVATE", 1, second, "", "second"), http.StatusOK, "")

	digests := map[string]string{}
	for id, version := range map[string]string{first: "1.0.0", second: "2.0.0"} {
		release := s.call(http.MethodGet, s.releaseAt(id), "author", "")
		s.require(release, http.StatusOK, "")
		if release.body["version"] != version {
			t.Fatalf("the release %s is version %v, want %s", id, release.body["version"], version)
		}
		digests[id] = release.id("artifact_digest")
	}
	if digests[first] == digests[second] {
		t.Fatalf("the two versions are of the same bytes %s", digests[first])
	}

	// downloaded fails the test unless each release is downloaded as the
	// source it was prepared from, under the digest of its own.
	downloaded := func(when string) {
		t.Helper()
		for id, source := range map[string]string{first: revisionSource, second: secondSource} {
			got := s.download(s.releaseAt(id)+"/artifact", "author")
			var files bundle
			if err := json.Unmarshal(got.body, &files); err != nil || got.status != http.StatusOK ||
				sha256Of(got.body) != digests[id] || got.header.Get("X-Artifact-Digest") != digests[id] || !files.holds("openapi.yaml", source) {
				t.Errorf("%s: the release %s is downloaded as %d %v %q (%v), want the source it was prepared from under %s",
					when, id, got.status, got.header.Get("X-Artifact-Digest"), got.body, err, digests[id])
			}
		}
	}
	s.requireArtifact(slot, digests[second], secondSource)
	downloaded("while the slot serves the second")

	s.require(s.change(slot, "publisher", "RESTORE", 2, first, "back to the first", "back"), http.StatusOK, "")
	s.requireArtifact(slot, digests[first], revisionSource)
	downloaded("after the slot went back to the first")

	s.require(s.change(slot, "publisher", "WITHDRAW", 3, "", "taken down", "down"), http.StatusOK, "")
	s.require(s.call(http.MethodGet, slot+"/artifact", "author", ""), http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	downloaded("while the slot serves none")
}

// A release is downloaded by its identifier whatever stands between a slot
// and the artifact but a block of the release itself: an access block on its
// target or on its asset, a product that is suspended, an audience that is
// not configured any more. A slot serves an artifact to a target, and these
// are what it holds to. A check the tools have moved on from is not among
// them: the slot serves on, as the download does. A release read by its
// identifier is served to no target, and is read by whoever may read the
// release: whoever reads the revision it was prepared from. A block that
// names the release refuses the download too (CR-0002), which
// TestABlockOfAReleaseStopsItsDownloadOnWhicheverTargetItStands follows.
func TestAReleaseOnABlockedTargetIsStillDownloadedByItsIdentifier(t *testing.T) {
	s := publicationFor(t)
	target, configPath := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	revision, check, id := s.released(revisionSource, "", "1.0.0")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, id, "", "first"), http.StatusOK, "")
	path := s.releaseAt(id)
	digest := s.call(http.MethodGet, path, "author", "").id("artifact_digest")
	want := s.download(path+"/artifact", "author")
	if want.status != http.StatusOK || sha256Of(want.body) != digest {
		t.Fatalf("the release is downloaded as %d %q, want the bytes of %s", want.status, want.body, digest)
	}
	s.requireArtifact(slot, digest, revisionSource)

	// served fails the test unless the slot answers as the case says and the
	// release is downloaded as it was, with its provenance read.
	served := func(when string, status int, code string) {
		t.Helper()
		if got := s.call(http.MethodGet, slot+"/artifact", "author", ""); got.status != status || got.code() != code {
			t.Errorf("%s: the slot serves %d %s, want %d %q", when, got.status, got.code(), status, code)
		}
		if got := s.download(path+"/artifact", "author"); got.status != http.StatusOK || !bytes.Equal(got.body, want.body) ||
			got.header.Get("X-Artifact-Digest") != digest {
			t.Errorf("%s: the release is downloaded as %d %v %q, want the bytes as they were", when, got.status, got.header, got.body)
		}
		if got := s.call(http.MethodGet, path+"/provenance", "author", ""); got.status != http.StatusOK {
			t.Errorf("%s: the provenance of the release is read as %d %v, want it read", when, got.status, got.body)
		}
	}

	s.suspend("SUSPENDED")
	served("a product that is suspended", http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	s.suspend("ACTIVE")
	served("the product active again", http.StatusOK, "")

	// A check of the revision by a later version of the tools takes the
	// release neither off the slot nor out of reach by its identifier.
	recordAnotherCheck(t, checkFixture{stack: s.stack}, checkRef(s, revision), check)
	served("a check by a later version of the tools", http.StatusOK, "")
	if shown := s.call(http.MethodGet, slot, "publisher", ""); shown.body["blocked"] != false {
		t.Fatalf("the slot reads %v, want no block", shown.body)
	}

	s.require(s.securityRole("security", "security_admin", "owner"), http.StatusCreated, "")
	blocking := func(body map[string]string) {
		t.Helper()
		body["reason"] = "the asset names an internal host"
		s.require(s.call(http.MethodPost, target+"/access-blocks", "security", jsonBody(body)), http.StatusCreated, "")
	}
	blocking(map[string]string{"api_asset_id": last(s.asset)})
	served("a block of its asset", http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	blocking(map[string]string{})
	served("blocks of its asset and of the whole target", http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	if blocked := s.call(http.MethodGet, slot, "publisher", ""); blocked.body["blocked"] != true {
		t.Fatalf("the slot reads %v, want it blocked", blocked.body)
	}
	s.suspend("SUSPENDED")
	served("those blocks in a product that is suspended", http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	s.suspend("ACTIVE")

	s.require(s.call(http.MethodDelete, configPath, "owner", ""), http.StatusNoContent, "")
	served("an audience that is not configured any more", http.StatusNotFound, "AUDIENCE_CONFIG_NOT_FOUND")
}

// A block of one release refuses its download by identifier, whichever
// target of the product the block stands on: the target the release is
// served on, or one it never was on (CR-0002). The refusal says so (409
// RELEASE_ACCESS_BLOCKED) to whoever may download the release, and is on
// record as the refusal of the download under DENY-03, as a refusal for want
// of the grant is. Who may not download is refused for that, and is told
// nothing of the block. The release and its provenance are read all along,
// another release of the asset is downloaded, and a block of the release on
// one target leaves what another target serves alone. Once the block is
// cleared, the release is downloaded again as it was; a suspended product
// changes none of this.
func TestABlockOfAReleaseStopsItsDownloadOnWhicheverTargetItStands(t *testing.T) {
	s := publicationFor(t)
	s.join(s.product, s.workspace, "owner", "reader", "internal_reader")
	serving, configPath := s.target(internalSandbox, "engineering")
	elsewhere := s.call(http.MethodPost, "/v1/products/"+s.product+"/publication-targets", "owner", jsonBody(map[string]string{
		"audience_config_id": last(configPath), "portal_key": "preview",
	}))
	s.require(elsewhere, http.StatusCreated, "")
	other := elsewhere.header.Get("Location")
	slot := assetSlot(serving, s.asset)
	firstRevision, _, id := s.released(revisionSource, "", "1.0.0")
	_, _, next := s.released(strings.ReplaceAll(revisionSource, "v1", "v2"), firstRevision, "2.0.0")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, id, "", "first"), http.StatusOK, "")
	path := s.releaseAt(id)
	want := s.download(path+"/artifact", "author")
	if want.status != http.StatusOK {
		t.Fatalf("the release is downloaded as %d %s before any block", want.status, want.body)
	}
	for subject, role := range map[string]string{"security": "security_admin", "clearer": "security_reviewer"} {
		s.require(s.securityRole(subject, role, "owner"), http.StatusCreated, "")
	}
	block := func(target string) string {
		t.Helper()
		placed := s.call(http.MethodPost, target+"/access-blocks", "security", jsonBody(map[string]string{
			"api_asset_id": last(s.asset), "release_id": id, "reason": "the release names an internal host",
		}))
		s.require(placed, http.StatusCreated, "")
		return target + "/access-blocks/" + placed.id("access_block_id")
	}
	clear := func(block string) {
		t.Helper()
		s.require(s.call(http.MethodPost, block+"/clearance", "clearer", jsonBody(map[string]string{"reason": "the host was renamed"})), http.StatusOK, "")
	}
	// downloads fails the test unless whoever may download the release is
	// refused it while the block stands, or downloads it as it was, and the
	// rest is read as it is.
	downloads := func(when string, blocked bool) {
		t.Helper()
		for _, reader := range []string{"author", "publisher", "maintainer", "owner"} {
			got := s.download(path+"/artifact", reader)
			switch {
			case blocked && (got.status != http.StatusConflict || got.code() != "RELEASE_ACCESS_BLOCKED" || bytes.Contains(got.body, want.body)):
				t.Errorf("%s: %s downloads %d %s, want 409 RELEASE_ACCESS_BLOCKED", when, reader, got.status, got.code())
			case !blocked && (got.status != http.StatusOK || !bytes.Equal(got.body, want.body)):
				t.Errorf("%s: %s downloads %d %s, want the bytes as they were", when, reader, got.status, got.code())
			}
		}
		for _, read := range []string{path, path + "/provenance"} {
			if got := s.call(http.MethodGet, read, "author", ""); got.status != http.StatusOK {
				t.Errorf("%s: %s is read as %d %v, want it read", when, read, got.status, got.body)
			}
		}
		if got := s.download(s.releaseAt(next)+"/artifact", "author"); got.status != http.StatusOK {
			t.Errorf("%s: another release of the asset is downloaded as %d %s, want it downloaded", when, got.status, got.code())
		}
		if got := s.download(path+"/artifact", "reader"); got.status != http.StatusForbidden || got.code() != "FORBIDDEN" {
			t.Errorf("%s: who may not download is answered %d %s, want 403 FORBIDDEN", when, got.status, got.code())
		}
		if got := s.download(path+"/artifact", "stranger"); got.status != http.StatusNotFound || got.code() != "PRODUCT_NOT_FOUND" {
			t.Errorf("%s: a stranger is answered %d %s, want 404 PRODUCT_NOT_FOUND", when, got.status, got.code())
		}
	}

	// A block on the target the release was never on.
	refusals := s.on("author", "release.download", "DENY-03", false)
	placed := block(other)
	downloads("a block on another target", true)
	s.requireArtifact(slot, want.header.Get("X-Artifact-Digest"), revisionSource)
	if got := s.on("author", "release.download", "DENY-03", false); got != refusals+1 {
		t.Errorf("%d refusals of the download by the author are on record, want %d", got, refusals+1)
	}
	s.suspend("SUSPENDED")
	downloads("that block in a product that is suspended", true)
	s.suspend("ACTIVE")
	clear(placed)
	downloads("that block cleared", false)
	s.suspend("SUSPENDED")
	downloads("a product that is suspended, with the block cleared", false)
	s.suspend("ACTIVE")

	// A block on the target that serves it: it is taken off that target to
	// clear the block, and is downloaded again.
	placed = block(serving)
	downloads("a block on the target that serves it", true)
	s.require(s.call(http.MethodGet, slot+"/artifact", "author", ""), http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	s.require(s.change(slot, "publisher", "WITHDRAW", 1, "", "it names an internal host", "down"), http.StatusOK, "")
	clear(placed)
	downloads("that block cleared", false)
}

// The blocks of a release are read after every target of the product is
// held, as a read through a slot holds its target: a block that is being
// placed on any of them, even one the release was never on, makes the
// download wait, and once it is placed the download is refused. Here the
// placement is what it is in the store: the target locked for update, the
// block recorded and the block generation of the target advanced, and then
// committed.
func TestADownloadByIdentifierWaitsForABlockOfTheReleaseThatIsBeingPlaced(t *testing.T) {
	s := publicationFor(t)
	_, configPath := s.target(internalSandbox, "engineering")
	elsewhere := s.call(http.MethodPost, "/v1/products/"+s.product+"/publication-targets", "owner", jsonBody(map[string]string{
		"audience_config_id": last(configPath), "portal_key": "preview",
	}))
	s.require(elsewhere, http.StatusCreated, "")
	other := last(elsewhere.header.Get("Location"))
	_, _, id := s.released(revisionSource, "", "1.0.0")
	path := s.releaseAt(id) + "/artifact"
	s.require(s.call(http.MethodGet, path, "author", ""), http.StatusOK, "")

	placing, err := s.owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = placing.Rollback(context.WithoutCancel(t.Context())) })
	for _, statement := range []string{
		`SELECT 1 FROM publication.target WHERE target_id = $1 FOR UPDATE`,
		`INSERT INTO publication.access_block
    (block_id, product_id, target_id, api_asset_id, release_id, generation, reason, created_by_issuer, created_by_subject, created_at)
SELECT gen_random_uuid(), product_id, target_id, $2, $3, block_generation + 1, 'the release names an internal host',
       'https://issuer.example', 'security', now()
FROM publication.target WHERE target_id = $1`,
		`UPDATE publication.target SET block_generation = block_generation + 1 WHERE target_id = $1`,
	} {
		arguments := []any{other}
		if strings.Contains(statement, "$3") {
			arguments = append(arguments, last(s.asset), id)
		}
		if _, err := placing.Exec(t.Context(), statement, arguments...); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	answered := make(chan downloaded, 1)
	go func() {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.server.URL+path, nil)
		if err != nil {
			answered <- downloaded{}
			return
		}
		request.Header.Set("Authorization", "Bearer "+s.token("author"))
		response, err := s.server.Client().Do(request)
		if err != nil {
			answered <- downloaded{}
			return
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		answered <- downloaded{status: response.StatusCode, header: response.Header, body: data}
	}()
	select {
	case got := <-answered:
		t.Fatalf("the download answered %d %s while a block of the release was being placed, want it to wait", got.status, got.code())
	case <-time.After(500 * time.Millisecond):
	}
	if err := placing.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-answered:
		if got.status != http.StatusConflict || got.code() != "RELEASE_ACCESS_BLOCKED" {
			t.Errorf("the download that waited answered %d %s, want 409 RELEASE_ACCESS_BLOCKED", got.status, got.code())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the download did not answer once the block was placed")
	}
}

// Release downloads and provenance require a governance read grant for the
// asset. The grant is checked on every request.
func TestReleaseDownloadsAndProvenanceRequireGovernanceAccess(t *testing.T) {
	s := publicationFor(t)
	other := s.createWorkspace(s.product, "owner", "operations")
	s.join(s.product, other, "owner", "elsewhere", "internal_publisher")
	// Someone who reads release metadata in engineering and publishes in operations.
	s.join(s.product, s.workspace, "owner", "between", "internal_reader")
	s.join(s.product, other, "owner", "between", "internal_publisher")
	_, _, id := s.released(revisionSource, "", "1.0.0")
	paths := map[string]string{
		"the release": s.releaseAt(id), "its artifact": s.releaseAt(id) + "/artifact", "its provenance": s.releaseAt(id) + "/provenance",
	}

	for _, c := range []struct {
		subject string
		status  int
		code    string
	}{
		{"author", http.StatusOK, ""}, {"coauthor", http.StatusOK, ""}, {"reviewer", http.StatusOK, ""}, {"publisher", http.StatusOK, ""},
		{"maintainer", http.StatusOK, ""}, {"owner", http.StatusOK, ""},
		// The grant counts where it was made, and nobody sees an asset of
		// a workspace they have no grant in, whoever they are.
		{"elsewhere", http.StatusNotFound, "API_ASSET_NOT_FOUND"}, {"platform-admin", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		{"stranger", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	} {
		for what, path := range paths {
			if got := s.download(path, c.subject); got.status != c.status || got.code() != c.code {
				t.Errorf("%s reads %s: %d %s, want %d %q", c.subject, what, got.status, got.code(), c.status, c.code)
			}
		}
	}

	s.require(s.call(http.MethodGet, paths["the release"], "between", ""), http.StatusOK, "")
	for _, suffix := range []string{"/artifact", "/provenance"} {
		s.require(s.call(http.MethodGet, s.releaseAt(id)+suffix, "between", ""), http.StatusForbidden, "FORBIDDEN")
	}

	for what, path := range paths {
		if got := s.download(path, ""); got.status != http.StatusUnauthorized || got.code() != "CREDENTIALS_MISSING" {
			t.Errorf("%s without credentials: %d %s, want 401 CREDENTIALS_MISSING", what, got.status, got.code())
		}
		if got := s.callWith(http.MethodGet, path, "mallory", ""); got.status != http.StatusUnauthorized || got.code() != "TOKEN_INVALID" {
			t.Errorf("%s with a token nobody accepts: %d %s, want 401 TOKEN_INVALID", what, got.status, got.code())
		}
	}

	// The grant is read with every request: it was all the subject held in
	// the product, so without it the product is one it does not know. A
	// product that is suspended still shows what it released.
	membership := s.join(s.product, s.workspace, "owner", "temporary", "internal_author")
	s.suspend("SUSPENDED")
	for what, path := range paths {
		if got := s.download(path, "temporary"); got.status != http.StatusOK {
			t.Errorf("%s in a suspended product: %d %s, want it read", what, got.status, got.code())
		}
	}
	s.suspend("ACTIVE")
	s.revoke(membership, "owner")
	for what, path := range paths {
		if got := s.download(path, "temporary"); got.status != http.StatusNotFound || got.code() != "PRODUCT_NOT_FOUND" {
			t.Errorf("%s once the grant is revoked: %d %s, want 404 PRODUCT_NOT_FOUND", what, got.status, got.code())
		}
	}
}

// The identifier of a release is of one asset of one product. One of another
// asset, of an asset of another workspace or of another product names no
// release here, and is answered as one that never was, so that guessing
// identifiers says nothing of what exists: what is asked of an asset the
// caller does not see is answered as the asset that is not there. The path
// takes no digest, and there is no other path to the bytes.
func TestAReleaseOfAnotherAssetOrProductIsNoReleaseOfThisAsset(t *testing.T) {
	s := publicationFor(t)
	other := *s
	otherReviews := *s.reviewFixture
	other.reviewFixture = &otherReviews
	other.revisions = s.revisionAsset(s.product, s.workspace, "maintainer", "tracking-api", "HTTP")
	other.asset = strings.TrimSuffix(other.revisions, "/revisions")
	_, _, shipping := s.released(revisionSource, "", "1.0.0")
	_, _, tracking := other.released(revisionSource, "", "1.0.0")

	operations := s.createWorkspace(s.product, "owner", "operations")
	s.join(s.product, operations, "owner", "maintainer", "workspace_maintainer")
	s.join(s.product, operations, "owner", "elsewhere", "internal_publisher")
	billing := strings.TrimSuffix(s.revisionAsset(s.product, operations, "maintainer", "billing-api", "HTTP"), "/revisions")
	parcel := s.register("platform-admin", "parcel", "parcel-owner")
	parcelWorkspace := s.createWorkspace(parcel, "parcel-owner", "engineering")
	s.join(parcel, parcelWorkspace, "parcel-owner", "publisher", "workspace_maintainer")
	s.join(parcel, parcelWorkspace, "parcel-owner", "publisher", "internal_publisher")
	parcelAsset := strings.TrimSuffix(s.revisionAsset(parcel, parcelWorkspace, "publisher", "parcel-api", "HTTP"), "/revisions")

	// asked is the release under the asset, read each way there is.
	for _, c := range []struct {
		when, subject, asset, release string
		absent                        string
		status                        int
		code                          string
	}{
		{"a release of another asset of the workspace", "publisher", s.asset, tracking, ids.NewUUID(), http.StatusNotFound, "RELEASE_NOT_FOUND"},
		{"and the other way round", "publisher", other.asset, shipping, ids.NewUUID(), http.StatusNotFound, "RELEASE_NOT_FOUND"},
		{"a release of an asset of another workspace, read by who reads its asset", "elsewhere", billing, shipping, ids.NewUUID(), http.StatusNotFound, "RELEASE_NOT_FOUND"},
		{"a release of an asset of another workspace, asked under that asset", "elsewhere", s.asset, shipping, ids.NewUUID(), http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		{"a release of another product, read by who reads in both", "publisher", parcelAsset, shipping, ids.NewUUID(), http.StatusNotFound, "RELEASE_NOT_FOUND"},
		{"a release of a product the caller does not know", "author", parcelAsset, shipping, ids.NewUUID(), http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	} {
		for _, suffix := range []string{"", "/artifact", "/provenance"} {
			asked := s.download(c.asset+"/releases/"+c.release+suffix, c.subject)
			missing := s.download(c.asset+"/releases/"+c.absent+suffix, c.subject)
			if asked.status != c.status || asked.code() != c.code || missing.status != c.status || missing.code() != c.code ||
				asked.status != missing.status || !sameProblem(asked, missing) {
				t.Errorf("%s, %s%s: %d %s, a release that never was: %d %s; want 404 %s alike", c.when, c.subject, suffix,
					asked.status, asked.code(), missing.status, missing.code(), c.code)
			}
		}
	}

	// What is not an identifier of a release names nothing: the digest of the
	// bytes does not either.
	digest := s.call(http.MethodGet, s.releaseAt(shipping), "author", "").id("artifact_digest")
	for _, name := range []string{digest, "latest", "1.0.0"} {
		for _, suffix := range []string{"", "/artifact", "/provenance"} {
			if got := s.download(s.asset+"/releases/"+name+suffix, "author"); got.status != http.StatusBadRequest || got.code() != "REQUEST_INVALID" {
				t.Errorf("a release named %q%s: %d %s, want 400 REQUEST_INVALID", name, suffix, got.status, got.code())
			}
		}
	}
}

// sameProblem reports whether two answers are the same problem, apart from
// the identifier of the request they were answered under.
func sameProblem(a, b downloaded) bool {
	problem := func(d downloaded) string {
		var members map[string]any
		if json.Unmarshal(d.body, &members) != nil {
			return string(d.body)
		}
		delete(members, "trace_id")
		return fmt.Sprint(members)
	}
	return problem(a) == problem(b)
}

// A refusal to read a release is on record as the refusal of what was asked:
// the download as release.download and the provenance as release.read, each
// under DENY-03, as the read of a release is, and of the release though it
// names none. A read that was served, and one of a release that is not there,
// leave nothing: no audit record and no event.
func TestARefusedReadIsOnRecordAsARefusalAndASuccessfulOneIsNot(t *testing.T) {
	s := publicationFor(t)
	s.join(s.product, s.workspace, "owner", "reader", "internal_reader")
	_, _, id := s.released(revisionSource, "", "1.0.0")
	path := s.releaseAt(id)
	records, events := s.count("platformoperations.audit_record"), s.count("bento.outbox")

	for _, reader := range []string{"author", "reviewer", "publisher", "owner"} {
		for _, suffix := range []string{"/artifact", "/provenance"} {
			s.require(s.call(http.MethodGet, path+suffix, reader, ""), http.StatusOK, "")
		}
	}
	for _, suffix := range []string{"/artifact", "/provenance"} {
		s.require(s.call(http.MethodGet, s.releaseAt(ids.NewUUID())+suffix, "publisher", ""), http.StatusNotFound, "RELEASE_NOT_FOUND")
	}
	if after, sent := s.count("platformoperations.audit_record"), s.count("bento.outbox"); after != records || sent != events {
		t.Errorf("the reads that were served or found nothing left %d audit records and %d events, want none", after-records, sent-events)
	}

	for _, caller := range []string{"reader"} {
		s.require(s.call(http.MethodGet, path+"/artifact", caller, ""), http.StatusForbidden, "FORBIDDEN")
		if download, read := s.on(caller, "release.download", "DENY-03", false), s.on(caller, "release.read", "DENY-03", false); download != 1 || read != 0 {
			t.Errorf("%s was refused the download: %d refusals of the download and %d of a read are on record, want 1 and 0", caller, download, read)
		}
		s.require(s.call(http.MethodGet, path+"/provenance", caller, ""), http.StatusForbidden, "FORBIDDEN")
		if download, read := s.on(caller, "release.download", "DENY-03", false), s.on(caller, "release.read", "DENY-03", false); download != 1 || read != 1 {
			t.Errorf("%s was refused the provenance: %d refusals of the download and %d of a read are on record, want 1 and 1", caller, download, read)
		}
	}
	// Who is not known to the product is refused too, and what the catalog
	// says of that is its own record.
	for _, suffix := range []string{"/artifact", "/provenance"} {
		s.require(s.call(http.MethodGet, path+suffix, "stranger", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	}
	for action, want := range map[string]int{"release.download": 1, "release.read": 1} {
		if got := s.on("stranger", action, "DENY-03", false); got != want {
			t.Errorf("%d refusals of %s are on record for the stranger, want %d", got, action, want)
		}
	}

	// Each is a refusal of the release and of nothing else, and none wrote
	// anything but its record.
	refusals := s.recorded(func(row audited) bool {
		return strings.HasPrefix(row.Action, "release.") && row.Outcome != "SUCCEEDED"
	})
	if occurrences(refusals) != 4 {
		t.Errorf("%d refusals of a release are on record, want 4: %v", occurrences(refusals), refusals)
	}
	for row := range refusals {
		if row.Rule != "DENY-03" || row.ResourceType != "ContractRelease" || row.ResourceID != "" || row.ProductID != s.product {
			t.Errorf("a refusal is on record as %+v, want DENY-03 of a ContractRelease that it does not name, in the product", row)
		}
	}
	if sent := s.count("bento.outbox"); sent != events {
		t.Errorf("the refusals left %d events, want none", sent-events)
	}
}
