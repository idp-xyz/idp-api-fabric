package main

import (
	"net/http"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

const partnerSandboxConfig = `{"audience":"partner","environment":"sandbox","docs_url":"https://docs.example/partners"}`

func TestAudienceConfigLifecycleOverHTTP(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "alice")
	other := s.register("platform-admin", "parcel", "bob")
	workspace := s.createWorkspace(product, "alice", "engineering")
	s.join(product, workspace, "alice", "carol", "internal_author")
	configs := "/v1/products/" + product + "/audience-configs"

	if items, token := s.page(configs, "alice"); len(items) != 0 || token != "" {
		t.Fatalf("a new product lists %v and a token %q", items, token)
	}
	created := s.call(http.MethodPost, configs, "alice", partnerSandboxConfig)
	id, location := created.id("audience_config_id"), created.header.Get("Location")
	if created.status != http.StatusCreated || id == "" || location != configs+"/"+id || created.header.Get("ETag") != `"1"` {
		t.Fatalf("create: status %d, headers %v, body %v", created.status, created.header, created.body)
	}
	s.expect("cannot configure the same pair twice", "alice", []exchange{
		{http.MethodPost, configs, partnerSandboxConfig, http.StatusConflict, "AUDIENCE_CONFIG_EXISTS"},
	})
	s.expect("a product member can see the configuration", "carol", []exchange{
		{http.MethodGet, location, "", http.StatusOK, ""},
		{http.MethodDelete, location, "", http.StatusForbidden, "FORBIDDEN"},
	})
	s.expect("a platform administrator can inspect configurations", "platform-admin", []exchange{
		{http.MethodGet, location, "", http.StatusOK, ""},
		{http.MethodPost, configs, partnerSandboxConfig, http.StatusForbidden, "FORBIDDEN"},
	})
	s.expect("another product owner cannot read it", "bob", []exchange{
		{http.MethodGet, location, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
		{http.MethodGet, strings.Replace(location, product, other, 1), "", http.StatusNotFound, "AUDIENCE_CONFIG_NOT_FOUND"},
	})
	second := s.call(http.MethodPost, configs, "alice", `{"audience":"internal","environment":"production"}`)
	if second.status != http.StatusCreated {
		t.Fatalf("configure a second audience: status %d, body %v", second.status, second.body)
	}
	pages := s.whole(configs, "carol", 1)
	if len(pages) != 2 || len(pages[0]) != 1 || len(pages[1]) != 1 ||
		pages[0][0]["audience_config_id"] != id || pages[1][0]["audience_config_id"] != second.id("audience_config_id") {
		t.Fatalf("the member read pages %v, want one configuration per page in creation order", pages)
	}

	replace := func(subject, tag, body string) answer {
		headers := make(http.Header)
		if tag != "" {
			headers.Set("If-Match", tag)
		}
		return s.callWithHeaders(http.MethodPut, location, s.token(subject), body, headers)
	}
	entry := `{"docs_url":"https://docs.example/partners-v2"}`
	if got := replace("alice", "", entry); got.status != http.StatusPreconditionRequired || got.code() != "PRECONDITION_REQUIRED" {
		t.Fatalf("change without If-Match: status %d, body %v", got.status, got.body)
	}
	if got := replace("alice", `"1"`, "{}"+entry); got.status != http.StatusBadRequest || got.code() != "REQUEST_INVALID" {
		t.Fatalf("two JSON values: status %d, body %v", got.status, got.body)
	}
	if got := replace("carol", `"1"`, entry); got.status != http.StatusForbidden {
		t.Fatalf("change as a member: status %d, body %v", got.status, got.body)
	}
	if got := replace("alice", `"1"`, entry); got.status != http.StatusOK || got.header.Get("ETag") != `"2"` ||
		got.body["docs_url"] != "https://docs.example/partners-v2" {
		t.Fatalf("change: status %d, headers %v, body %v", got.status, got.header, got.body)
	}
	if got := replace("alice", `"1"`, "{}"); got.status != http.StatusPreconditionFailed || got.code() != "PRECONDITION_FAILED" {
		t.Fatalf("change with stale If-Match: status %d, body %v", got.status, got.body)
	}
	if got := replace("alice", `"2"`, entry); got.status != http.StatusOK || got.header.Get("ETag") != `"2"` {
		t.Fatalf("repeat the current entry: status %d, headers %v, body %v", got.status, got.header, got.body)
	}
	cleared := replace("alice", `"2"`, "{}")
	if _, hasEntry := cleared.body["docs_url"]; cleared.status != http.StatusOK || hasEntry || cleared.header.Get("ETag") != `"3"` {
		t.Fatalf("clear the entry: status %d, headers %v, body %v", cleared.status, cleared.header, cleared.body)
	}
	s.expect("the configuration ends and stays as history", "alice", []exchange{
		{http.MethodDelete, location, "", http.StatusNoContent, ""},
		{http.MethodGet, location, "", http.StatusNotFound, "AUDIENCE_CONFIG_NOT_FOUND"},
		{http.MethodDelete, location, "", http.StatusNotFound, "AUDIENCE_CONFIG_NOT_FOUND"},
	})
	if items, token := s.page(configs, "alice"); len(items) != 1 || items[0]["audience_config_id"] != second.id("audience_config_id") || token != "" {
		t.Fatalf("after removal the product lists %v and a token %q", items, token)
	}

	var version, events, allowed int
	var removed bool
	ctx := t.Context()
	if err := s.owner.QueryRow(ctx, `SELECT audience_config_version, removed_at IS NOT NULL
		FROM productgovernance.product_audience_config WHERE audience_config_id = $1`, id).Scan(&version, &removed); err != nil {
		t.Fatal(err)
	}
	if err := s.owner.QueryRow(ctx, `SELECT count(*) FROM bento.outbox
		WHERE event_type = 'fabric.productgovernance.audience_config.changed' AND subject = $1`, "audience_config/"+id).Scan(&events); err != nil {
		t.Fatal(err)
	}
	for _, row := range s.audit() {
		if row.ResourceID == id && row.Outcome == "SUCCEEDED" {
			allowed++
		}
	}
	if version != 4 || !removed || events != 4 || allowed != 4 {
		t.Fatalf("history version=%d removed=%t events=%d success audits=%d, want 4/true/4/4", version, removed, events, allowed)
	}
}

// A revocation that already holds the owner assignment wins against an
// audience write: the write waits for it and is refused once it commits. A
// plain read of the role would see the uncommitted assignment as active and
// let the request through. Adding a configuration, changing its entry and
// removing it each lock the assignments of their caller, so each is tried.
func TestAudienceWritesWaitForOwnerRevocation(t *testing.T) {
	writes := map[string]audienceWrite{
		"a new configuration":  {method: http.MethodPost, onList: true, body: `{"audience":"public","environment":"production"}`},
		"a change of an entry": {method: http.MethodPut, body: `{"docs_url":"https://docs.example/wrong"}`, conditional: true},
		"a removal":            {method: http.MethodDelete},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) { write.waitsForOwnerRevocation(t) })
	}
}

// audienceWrite is a write of an audience configuration, as the request that
// asks for it.
type audienceWrite struct {
	method string
	// onList says that the request goes to the list, where a configuration
	// is added, and not to the configuration there is.
	onList bool
	body   string
	// conditional says that the request carries the ETag of the
	// configuration there is.
	conditional bool
}

// waitsForOwnerRevocation sends the write as an owner whose assignment a
// transaction that has not committed yet revokes.
func (w audienceWrite) waitsForOwnerRevocation(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "alice", "bob")
	workspace := s.createWorkspace(product, "alice", "engineering")
	s.join(product, workspace, "alice", "bob", "internal_author")
	configs := "/v1/products/" + product + "/audience-configs"
	created := s.call(http.MethodPost, configs, "alice", partnerSandboxConfig)
	if created.status != http.StatusCreated {
		t.Fatalf("create: status %d, body %v", created.status, created.body)
	}

	conn := pgtest.Connect(t, s.database.Migrator)
	ctx := t.Context()
	revoking, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = revoking.Rollback(ctx) }()
	revoked, err := revoking.Exec(ctx, `UPDATE productgovernance.product_role_assignment
		SET revoked_at = greatest(granted_at, clock_timestamp()), revoked_by_issuer = $2,
		    revoked_by_subject = 'alice', role_assignment_version = role_assignment_version + 1
		WHERE product_id = $1 AND principal_issuer = $2 AND principal_subject = 'bob' AND revoked_at IS NULL`,
		product, s.issuer.URL())
	if err != nil || revoked.RowsAffected() != 1 {
		t.Fatalf("revoke Bob's owner assignment within the transaction: %v (%v)", revoked, err)
	}

	location, token := created.header.Get("Location"), s.token("bob")
	target := location
	if w.onList {
		target = configs
	}
	headers := make(http.Header)
	if w.conditional {
		headers.Set("If-Match", created.header.Get("ETag"))
	}
	done := make(chan answer, 1)
	go func() {
		defer close(done)
		done <- s.callWithHeaders(w.method, target, token, w.body, headers)
	}()
	s.waiting(1)
	if err := revoking.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.status != http.StatusForbidden || got.code() != "FORBIDDEN" {
		t.Fatalf("revoked owner is still a member: status %d, body %v, want 403", got.status, got.body)
	}
	// The write left nothing: the configuration there was is as it was, and
	// it is the only one.
	read := s.call(http.MethodGet, location, "alice", "")
	if read.status != http.StatusOK || read.header.Get("ETag") != `"1"` || read.body["docs_url"] != created.body["docs_url"] {
		t.Fatalf("revoked owner's write changed the configuration: status %d, headers %v, body %v", read.status, read.header, read.body)
	}
	if items, next := s.page(configs, "alice"); len(items) != 1 || next != "" {
		t.Fatalf("revoked owner's write left the configurations %v and a token %q, want the one there was", items, next)
	}
}
