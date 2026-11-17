package main

import (
	"maps"
	"net/http"
	"reflect"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

// developersBrand is the brand CargoPai gives itself: a name of its own, a
// logo and a colour.
const developersBrand = `{"name":"CargoPai Developers","logo_url":"https://static.cargopai.example/brand/logo.svg","primary_color":"#0A7B3C"}`

var developers = map[string]any{
	"name": "CargoPai Developers", "logo_url": "https://static.cargopai.example/brand/logo.svg", "primary_color": "#0A7B3C",
}

// brandOf is the brand an answer shows of a product.
func brandOf(got answer) map[string]any {
	brand, _ := got.body["brand"].(map[string]any)
	return brand
}

// changeBrand changes the brand of the product as the subject, from the
// version the tag names, or from none if the tag is empty.
func (s *stack) changeBrand(productID, subject, tag, body string) answer {
	s.t.Helper()

	headers := make(http.Header)
	if tag != "" {
		headers.Set("If-Match", tag)
	}
	return s.callWithHeaders(http.MethodPut, "/v1/products/"+productID+"/brand", s.token(subject), body, headers)
}

// FR-005, DC-003: each product has a brand of its own, from its
// registration. Its owners change it, from the version of the product they
// read, and nobody else does. Whoever may read the product reads its brand
// with it, and the brand of one product says nothing about another's.
func TestTheBrandOfAProductOverHTTP(t *testing.T) {
	s := start(t, "platform-admin")

	registered := s.call(http.MethodPost, "/v1/products", "platform-admin", registration("cargopai", "alice")(s))
	if registered.status != http.StatusCreated || registered.header.Get("ETag") != `"1"` ||
		!reflect.DeepEqual(brandOf(registered), map[string]any{"name": "Product cargopai"}) {
		t.Fatalf("register: status %d, headers %v, body %v, want the product under its display name at version 1",
			registered.status, registered.header, registered.body)
	}
	product := registered.id("product_id")
	other := s.register("platform-admin", "parcel", "bob")
	workspace := s.createWorkspace(product, "alice", "engineering")
	s.join(product, workspace, "alice", "carol", "internal_author")
	location := "/v1/products/" + product

	// A change says which version it was made from, is one JSON value, and
	// is a brand. None of these is a refusal of the caller.
	if got := s.changeBrand(product, "alice", "", developersBrand); got.status != http.StatusPreconditionRequired || got.code() != "PRECONDITION_REQUIRED" {
		t.Fatalf("change without If-Match: status %d, body %v", got.status, got.body)
	}
	if got := s.changeBrand(product, "alice", `"1"`, developersBrand+`{"name":"Second"}`); got.status != http.StatusBadRequest || got.code() != "REQUEST_INVALID" {
		t.Fatalf("two JSON values: status %d, body %v", got.status, got.body)
	}
	if got := s.changeBrand(product, "alice", `"1"`, `{"name":"CargoPai Developers","primary_color":"green"}`); got.status != http.StatusBadRequest || got.code() != "REQUEST_INVALID" {
		t.Fatalf("a colour by its name: status %d, body %v", got.status, got.body)
	}
	// Only an owner changes the brand. Whoever else knows the product is
	// told it may not; to the owner of another product there is none.
	for subject, want := range map[string]struct {
		status int
		code   string
	}{
		"carol":          {http.StatusForbidden, "FORBIDDEN"},
		"platform-admin": {http.StatusForbidden, "FORBIDDEN"},
		"bob":            {http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	} {
		if got := s.changeBrand(product, subject, `"1"`, developersBrand); got.status != want.status || got.code() != want.code {
			t.Fatalf("%s changes the brand: status %d, body %v, want %d %s", subject, got.status, got.body, want.status, want.code)
		}
	}

	changed := s.changeBrand(product, "alice", `"1"`, developersBrand)
	if changed.status != http.StatusOK || changed.header.Get("ETag") != `"2"` || !reflect.DeepEqual(brandOf(changed), developers) ||
		changed.body["display_name"] != "Product cargopai" || changed.body["product_id"] != product {
		t.Fatalf("change: status %d, headers %v, body %v, want the product with its brand at version 2", changed.status, changed.header, changed.body)
	}
	if got := s.changeBrand(product, "alice", `"1"`, `{"name":"Stale"}`); got.status != http.StatusPreconditionFailed || got.code() != "PRECONDITION_FAILED" {
		t.Fatalf("change from the version before: status %d, body %v", got.status, got.body)
	}
	if got := s.changeBrand(product, "alice", `"2"`, developersBrand); got.status != http.StatusOK || got.header.Get("ETag") != `"2"` {
		t.Fatalf("the same brand again: status %d, headers %v, body %v, want the product at the version it was", got.status, got.header, got.body)
	}

	// Whoever may read the product reads its brand with it, in a list too.
	for _, reader := range []string{"alice", "carol", "platform-admin"} {
		read := s.call(http.MethodGet, location, reader, "")
		if read.status != http.StatusOK || read.header.Get("ETag") != `"2"` || !reflect.DeepEqual(brandOf(read), developers) {
			t.Errorf("%s reads the product: status %d, headers %v, body %v, want its brand at version 2", reader, read.status, read.header, read.body)
		}
	}
	if items, _ := s.page("/v1/products", "carol"); len(items) != 1 || !reflect.DeepEqual(items[0]["brand"], any(developers)) {
		t.Errorf("a member lists %v, want the product with its brand", items)
	}
	s.expect("the owner of another product reads nothing of it", "bob", []exchange{
		{http.MethodGet, location, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	})

	// The other product has a brand of its own, which is its owner's to
	// change and says nothing about this one.
	if got := s.changeBrand(other, "bob", `"1"`, `{"name":"Parcel Open Platform"}`); got.status != http.StatusOK || got.header.Get("ETag") != `"2"` ||
		!reflect.DeepEqual(brandOf(got), map[string]any{"name": "Parcel Open Platform"}) {
		t.Fatalf("the owner of the other product changes its brand: status %d, headers %v, body %v", got.status, got.header, got.body)
	}
	if got := s.changeBrand(other, "alice", `"2"`, developersBrand); got.status != http.StatusNotFound || got.code() != "PRODUCT_NOT_FOUND" {
		t.Fatalf("alice changes the brand of the other product: status %d, body %v", got.status, got.body)
	}
	if read := s.call(http.MethodGet, location, "alice", ""); read.header.Get("ETag") != `"2"` || !reflect.DeepEqual(brandOf(read), developers) {
		t.Errorf("after the other product changed its brand, this one reads %v at %s", read.body, read.header.Get("ETag"))
	}

	// A brand is replaced as a whole: what the new one leaves out, the
	// product has none of from then on.
	plain := s.changeBrand(product, "alice", `"2"`, `{"name":"CargoPai 开放平台"}`)
	if plain.status != http.StatusOK || plain.header.Get("ETag") != `"3"` || !reflect.DeepEqual(brandOf(plain), map[string]any{"name": "CargoPai 开放平台"}) {
		t.Fatalf("the name alone: status %d, headers %v, body %v", plain.status, plain.header, plain.body)
	}

	// What the two changes left: the row at version 3 without a logo or a
	// colour, an event for each, and their audit records beside those of
	// the refusals.
	var version, events int
	var name, changedBy string
	var withLogo, withColour bool
	ctx := t.Context()
	if err := s.owner.QueryRow(ctx, `SELECT aggregate_version, brand_name, brand_logo_url IS NOT NULL, brand_primary_color IS NOT NULL, updated_by_subject
		FROM productgovernance.product WHERE product_id = $1`, product).Scan(&version, &name, &withLogo, &withColour, &changedBy); err != nil {
		t.Fatal(err)
	}
	if err := s.owner.QueryRow(ctx, `SELECT count(*) FROM bento.outbox
		WHERE event_type = 'fabric.productgovernance.product.brand_changed' AND subject = $1`, "product/"+product).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if version != 3 || name != "CargoPai 开放平台" || withLogo || withColour || changedBy != "alice" || events != 2 {
		t.Errorf("the row is at version %d under %q (logo %t, colour %t), changed by %s, with %d events; want version 3, the name alone, by alice, 2 events",
			version, name, withLogo, withColour, changedBy, events)
	}
	want := map[audited]int{
		{"alice", "product.change_brand", "SUCCEEDED", "AC-09", product, "Product", product}:         2,
		{"carol", "product.change_brand", "DENIED", "DENY-03", product, "Product", product}:          1,
		{"platform-admin", "product.change_brand", "DENIED", "DENY-03", product, "Product", product}: 1,
		{"bob", "product.change_brand", "DENIED", "DENY-03", product, "Product", product}:            1,
		{"bob", "product.change_brand", "SUCCEEDED", "AC-09", other, "Product", other}:               1,
		{"alice", "product.change_brand", "DENIED", "DENY-03", other, "Product", other}:              1,
	}
	if got := s.recorded(func(record audited) bool { return record.Action == "product.change_brand" }); !maps.Equal(got, want) {
		t.Errorf("the audit trail of the brands holds\n %v\nwant\n %v", got, want)
	}
}

// A revocation that already holds the owner assignment wins against a change
// of the brand, as it does against a write of an audience: the change waits
// for it and is refused once it commits.
func TestAChangeOfTheBrandWaitsForOwnerRevocation(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "alice", "bob")
	workspace := s.createWorkspace(product, "alice", "engineering")
	s.join(product, workspace, "alice", "bob", "internal_author")

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

	token := s.token("bob")
	headers := make(http.Header)
	headers.Set("If-Match", `"1"`)
	done := make(chan answer, 1)
	go func() {
		defer close(done)
		done <- s.callWithHeaders(http.MethodPut, "/v1/products/"+product+"/brand", token, developersBrand, headers)
	}()
	s.waiting(1)
	if err := revoking.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.status != http.StatusForbidden || got.code() != "FORBIDDEN" {
		t.Fatalf("revoked owner is still a member: status %d, body %v, want 403", got.status, got.body)
	}
	// The change left nothing: the product is as it was registered.
	read := s.call(http.MethodGet, "/v1/products/"+product, "alice", "")
	if read.status != http.StatusOK || read.header.Get("ETag") != `"1"` || !reflect.DeepEqual(brandOf(read), map[string]any{"name": "Product cargopai"}) {
		t.Fatalf("revoked owner's change reached the product: status %d, headers %v, body %v", read.status, read.header, read.body)
	}
}

// INV-01: a suspended product takes no change of its brand, and keeps the
// brand it has. No operation suspends a product yet: its status is set where
// an operator would set it, which the table lets the role that owns it do.
func TestASuspendedProductKeepsItsBrand(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "alice")
	if got := s.changeBrand(product, "alice", `"1"`, developersBrand); got.status != http.StatusOK {
		t.Fatalf("change: status %d, body %v", got.status, got.body)
	}

	setStatus := func(status string) {
		t.Helper()
		if _, err := s.owner.Exec(t.Context(), `UPDATE productgovernance.product SET status = $2 WHERE product_id = $1`, product, status); err != nil {
			t.Fatalf("set the status %s: %v", status, err)
		}
	}
	setStatus("SUSPENDED")
	for what, body := range map[string]string{"another brand": `{"name":"CargoPai"}`, "the brand it has": developersBrand} {
		if got := s.changeBrand(product, "alice", `"2"`, body); got.status != http.StatusConflict || got.code() != "PRODUCT_NOT_ACTIVE" {
			t.Errorf("%s, of a suspended product: status %d, body %v, want 409 PRODUCT_NOT_ACTIVE", what, got.status, got.body)
		}
	}
	// It is read as it is: suspended, under its brand, at the version it
	// was. Setting the status by hand counts in no version.
	read := s.call(http.MethodGet, "/v1/products/"+product, "alice", "")
	if read.status != http.StatusOK || read.body["status"] != "SUSPENDED" || read.header.Get("ETag") != `"2"` || !reflect.DeepEqual(brandOf(read), developers) {
		t.Errorf("the suspended product: status %d, headers %v, body %v", read.status, read.header, read.body)
	}

	setStatus("ACTIVE")
	if got := s.changeBrand(product, "alice", `"2"`, `{"name":"CargoPai"}`); got.status != http.StatusOK || got.header.Get("ETag") != `"3"` {
		t.Errorf("once it is resumed: status %d, headers %v, body %v, want the change at version 3", got.status, got.header, got.body)
	}
}
