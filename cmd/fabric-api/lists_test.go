package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// page reads one page of a list as the subject. It returns the items and
// the token of the page that follows, which is empty on the last page. A
// page is a list of items and, at most, that token: it says nothing else,
// and not how many items there are.
func (s *stack) page(path, subject string) (items []map[string]any, next string) {
	s.t.Helper()

	got := s.call(http.MethodGet, path, subject, "")
	if got.status != http.StatusOK {
		s.t.Fatalf("%s lists %s: status %d %v", subject, path, got.status, got.body)
	}
	if cache := got.header.Get("Cache-Control"); cache != "no-store" {
		s.t.Errorf("%s lists %s: Cache-Control %q, want no-store", subject, path, cache)
	}
	listed, isList := got.body["items"].([]any)
	if !isList {
		s.t.Fatalf("%s lists %s: the items are %v, want a list, empty if there is nothing", subject, path, got.body["items"])
	}
	for _, item := range listed {
		object, isObject := item.(map[string]any)
		if !isObject {
			s.t.Fatalf("%s lists %s: an item is %v, want an object", subject, path, item)
		}
		items = append(items, object)
	}
	members := 1
	if token, carried := got.body["next_page_token"]; carried {
		members++
		if next, _ = token.(string); next == "" {
			s.t.Fatalf("%s lists %s: the token of the next page is %v, want a token or no member", subject, path, token)
		}
	}
	if len(got.body) != members {
		s.t.Errorf("%s lists %s: the page says more than its items and its token: %v", subject, path, got.body)
	}
	return items, next
}

// whole reads a list to its end as the subject, a page of size items at a
// time, each page with the token the one before handed out. It returns the
// pages.
func (s *stack) whole(path, subject string, size int) [][]map[string]any {
	s.t.Helper()

	var pages [][]map[string]any
	target := fmt.Sprintf("%s?page_size=%d", path, size)
	for len(pages) < 200 {
		items, next := s.page(target, subject)
		pages = append(pages, items)
		if next == "" {
			return pages
		}
		target = fmt.Sprintf("%s?page_size=%d&page_token=%s", path, size, next)
	}
	s.t.Fatalf("%s lists %s: the list does not end", subject, path)
	return nil
}

// told are the values of one member of the items, in the order of the list.
// Of an object, such as the principal of a role assignment, it is the
// subject.
func told(items []map[string]any, member string) []string {
	values := []string{}
	for _, item := range items {
		switch value := item[member].(type) {
		case string:
			values = append(values, value)
		case map[string]any:
			values = append(values, fmt.Sprint(value["subject"]))
		default:
			values = append(values, fmt.Sprint(value))
		}
	}
	return values
}

// join makes the subject a member of the workspace, as the owner, and
// returns where the membership is read and revoked.
func (s *stack) join(productID, workspaceID, owner, subject, role string) string {
	s.t.Helper()

	got := s.call(http.MethodPost, "/v1/products/"+productID+"/workspaces/"+workspaceID+"/memberships", owner, s.membership(subject, role))
	if got.status != http.StatusCreated {
		s.t.Fatalf("make %s a member: status %d %v", subject, got.status, got.body)
	}
	return got.header.Get("Location")
}

// revoke ends what is at the location, as the owner.
func (s *stack) revoke(location, owner string) {
	s.t.Helper()

	if got := s.call(http.MethodDelete, location, owner, ""); got.status != http.StatusNoContent {
		s.t.Fatalf("revoke %s: status %d %v", location, got.status, got.body)
	}
}

// The repair of a product whose owner was mistyped, through the API alone.
// The platform administrator names the owner that was meant. She lists the
// owners of the product, finds the one that was mistyped and removes it by
// the identifier the list gave her: no identifier is read anywhere else.
// The administrator lists no owner, before or after.
func TestAMistypedOwnerIsFoundInTheListOfTheOwnersAndRemoved(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alcie")
	owners := "/v1/products/" + cargopai + "/role-assignments"

	s.expect("before it names the owner", "platform-admin", []exchange{{http.MethodGet, owners, "", http.StatusForbidden, "FORBIDDEN"}})
	alices := s.nameOwner(cargopai, "platform-admin", "alice")
	s.expect("after it named the owner", "platform-admin", []exchange{{http.MethodGet, owners, "", http.StatusForbidden, "FORBIDDEN"}})

	// The owner of the registration comes first, then the one named since.
	listed, next := s.page(owners, "alice")
	if got := told(listed, "principal"); !slices.Equal(got, []string{"alcie", "alice"}) || next != "" {
		t.Fatalf("alice lists the owners %v and a token %q, want the mistyped one, herself, and no token", got, next)
	}
	// Each is listed as it is read, and hers is the assignment the
	// administrator was answered with.
	for _, item := range listed {
		read := s.call(http.MethodGet, owners+"/"+fmt.Sprint(item["role_assignment_id"]), "alice", "")
		if read.status != http.StatusOK || fmt.Sprint(read.body) != fmt.Sprint(item) {
			t.Errorf("the owner listed as %v is read as %d %v", item, read.status, read.body)
		}
	}
	if listed[1]["role_assignment_id"] != last(alices) {
		t.Errorf("alice is listed under %v, want the assignment at %s", listed[1]["role_assignment_id"], alices)
	}

	s.revoke(owners+"/"+fmt.Sprint(listed[0]["role_assignment_id"]), "alice")
	if listed, _ := s.page(owners, "alice"); !slices.Equal(told(listed, "principal"), []string{"alice"}) {
		t.Errorf("once the mistyped owner is removed the list holds %v, want alice alone", told(listed, "principal"))
	}
	if got, want := s.owners(cargopai), []string{"alice"}; !slices.Equal(got, want) {
		t.Errorf("the owners are %v, want %v", got, want)
	}
	s.createWorkspace(cargopai, "alice", "api")

	// The two refusals of the administrator are on record. A list that was
	// answered left none.
	want := map[audited]int{{"platform-admin", "product_role.read", "DENIED", "DENY-03", cargopai, "RoleAssignment", ""}: 2}
	if got := s.recorded(func(record audited) bool { return record.Action == "product_role.read" }); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the audit trail holds\n %v\nwant\n %v", got, want)
	}
}

// Each list shows a caller what the single read shows it, and no more.
// Whoever may not read a list is answered as far as it may know, a stranger
// as for a product that is missing, and what belongs to one product is in no
// list of another. Every refusal is on record; a list that is answered
// leaves no record.
func TestEachListShowsACallerWhatItMayRead(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai, parcel := s.register("platform-admin", "cargopai", "alice"), s.register("platform-admin", "parcel", "bob")
	engineering, operations := s.createWorkspace(cargopai, "alice", "api-engineering"), s.createWorkspace(cargopai, "alice", "operations")
	sorting := s.createWorkspace(parcel, "bob", "sorting")
	// carol maintains the first workspace of CargoPai and dave writes in the
	// second. erin was a member of the first and frank an owner of the
	// product: neither is any more. mallory never held anything.
	carols := s.join(cargopai, engineering, "alice", "carol", "workspace_maintainer")
	s.join(cargopai, operations, "alice", "dave", "internal_author")
	s.revoke(s.join(cargopai, engineering, "alice", "erin", "internal_author"), "alice")
	s.revoke(s.nameOwner(cargopai, "alice", "frank"), "alice")

	const missing = "00000000-0000-4000-8000-000000000000"
	workspaces := func(product string) string { return "/v1/products/" + product + "/workspaces" }
	members := func(product, workspace string) string { return workspaces(product) + "/" + workspace + "/memberships" }
	owners := func(product string) string { return "/v1/products/" + product + "/role-assignments" }
	strangers := []string{"bob", "erin", "frank", "mallory"}

	// What each caller is shown.
	for _, list := range []struct {
		what, path, subject, member string
		want                        []string
	}{
		{"the products", "/v1/products", "platform-admin", "product_key", []string{"cargopai", "parcel"}},
		{"the products", "/v1/products", "alice", "product_key", []string{"cargopai"}},
		{"the products", "/v1/products", "bob", "product_key", []string{"parcel"}},
		{"the products", "/v1/products", "carol", "product_key", []string{"cargopai"}},
		{"the products", "/v1/products", "dave", "product_key", []string{"cargopai"}},
		{"the products", "/v1/products", "erin", "product_key", []string{}},
		{"the products", "/v1/products", "frank", "product_key", []string{}},
		{"the products", "/v1/products", "mallory", "product_key", []string{}},
		{"the workspaces of CargoPai", workspaces(cargopai), "alice", "workspace_key", []string{"api-engineering", "operations"}},
		{"the workspaces of CargoPai", workspaces(cargopai), "carol", "workspace_key", []string{"api-engineering"}},
		{"the workspaces of CargoPai", workspaces(cargopai), "dave", "workspace_key", []string{"operations"}},
		{"the members of the first workspace", members(cargopai, engineering), "alice", "member", []string{"carol"}},
		{"the members of the second workspace", members(cargopai, operations), "alice", "member", []string{"dave"}},
		{"the owners of CargoPai", owners(cargopai), "alice", "principal", []string{"alice"}},
		// What belongs to Parcel is listed under Parcel, to its owner.
		{"the workspaces of Parcel", workspaces(parcel), "bob", "workspace_key", []string{"sorting"}},
		{"the members of the workspace of Parcel", members(parcel, sorting), "bob", "member", []string{}},
		{"the owners of Parcel", owners(parcel), "bob", "principal", []string{"bob"}},
	} {
		items, next := s.page(list.path, list.subject)
		if got := told(items, list.member); !slices.Equal(got, list.want) || next != "" {
			t.Errorf("%s lists %s as %v with the token %q, want %v and no token", list.subject, list.what, got, next, list.want)
		}
	}
	// A membership is listed as it is read.
	if items, _ := s.page(members(cargopai, engineering), "alice"); len(items) == 1 {
		if read := s.call(http.MethodGet, carols, "alice", ""); fmt.Sprint(read.body) != fmt.Sprint(items[0]) {
			t.Errorf("the membership listed as %v is read as %v", items[0], read.body)
		}
	}

	// What everybody else is answered.
	refused := map[audited]int{}
	refuse := func(list, action, resource, subject string, status int, code string) {
		t.Helper()

		got := s.call(http.MethodGet, list, subject, "")
		if got.status != status || got.code() != code || got.body["items"] != nil {
			t.Errorf("%s lists %s: %d %v, want %d %s and no item", subject, list, got.status, got.body, status, code)
		}
		refused[audited{subject, action, "DENIED", "DENY-03", cargopai, resource, ""}]++
		// To a stranger the product is not there: the answer is the one a
		// product that is missing gets, member for member.
		if code == "PRODUCT_NOT_FOUND" {
			if absent := s.call(http.MethodGet, strings.Replace(list, cargopai, missing, 1), subject, ""); !sameAnswer(got, absent) {
				t.Errorf("%s lists %s: %d %v; of a product that is missing: %d %v", subject, list, got.status, got.body, absent.status, absent.body)
			}
		}
	}
	for _, stranger := range strangers {
		refuse(workspaces(cargopai), "workspace.read", "Workspace", stranger, http.StatusNotFound, "PRODUCT_NOT_FOUND")
		refuse(members(cargopai, engineering), "member.read", "Member", stranger, http.StatusNotFound, "PRODUCT_NOT_FOUND")
		refuse(owners(cargopai), "product_role.read", "RoleAssignment", stranger, http.StatusNotFound, "PRODUCT_NOT_FOUND")
	}
	// The administrator knows the product and nothing of what is inside it.
	refuse(workspaces(cargopai), "workspace.read", "Workspace", "platform-admin", http.StatusForbidden, "FORBIDDEN")
	refuse(members(cargopai, engineering), "member.read", "Member", "platform-admin", http.StatusNotFound, "WORKSPACE_NOT_FOUND")
	refuse(owners(cargopai), "product_role.read", "RoleAssignment", "platform-admin", http.StatusForbidden, "FORBIDDEN")
	// A member reads its workspace, not the members of it, and not the
	// owners of the product. Of another workspace it does not know.
	refuse(members(cargopai, engineering), "member.read", "Member", "carol", http.StatusForbidden, "FORBIDDEN")
	refuse(members(cargopai, operations), "member.read", "Member", "carol", http.StatusNotFound, "WORKSPACE_NOT_FOUND")
	refuse(members(cargopai, engineering), "member.read", "Member", "dave", http.StatusNotFound, "WORKSPACE_NOT_FOUND")
	refuse(owners(cargopai), "product_role.read", "RoleAssignment", "carol", http.StatusForbidden, "FORBIDDEN")
	refuse(owners(cargopai), "product_role.read", "RoleAssignment", "dave", http.StatusForbidden, "FORBIDDEN")

	// A workspace is listed under its own product only, also for an owner:
	// that is no refusal, there is no such workspace there.
	s.expect("under another product", "bob", []exchange{
		{http.MethodGet, members(parcel, engineering), "", http.StatusNotFound, "WORKSPACE_NOT_FOUND"},
		{http.MethodGet, members(parcel, missing), "", http.StatusNotFound, "WORKSPACE_NOT_FOUND"},
	})

	if got := s.recorded(func(record audited) bool { return record.Outcome == "DENIED" }); fmt.Sprint(got) != fmt.Sprint(refused) {
		t.Errorf("the audit trail holds the refusals\n %v\nwant\n %v", got, refused)
	}
	// Reading changed nothing.
	if products, spaces, held := s.count("productgovernance.product"), s.count("productgovernance.workspace"),
		s.count("productgovernance.workspace_membership"); products != 2 || spaces != 3 || held != 3 {
		t.Errorf("%d products, %d workspaces and %d memberships, want the 2, the 3 and the 3 there were", products, spaces, held)
	}
}

// A list is read a page at a time, oldest first. Every page but the last is
// full and hands out the token of the next; the last one hands out none,
// also when it is full. What is added while the list is read comes at its
// end, and what is revoked is listed no more.
func TestAListIsReadAPageAtATimeOldestFirst(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	workspaces := "/v1/products/" + cargopai + "/workspaces"

	var keys, ids []string
	for index := range 7 {
		keys = append(keys, fmt.Sprintf("space-%d", index))
		ids = append(ids, s.createWorkspace(cargopai, "alice", keys[index]))
	}
	for _, size := range []int{1, 2, 3, 7, 8, 100} {
		pages := s.whole(workspaces, "alice", size)
		if got := told(slices.Concat(pages...), "workspace_key"); !slices.Equal(got, keys) {
			t.Errorf("in pages of %d the workspaces are %v, want %v", size, got, keys)
		}
		if want := (len(keys) + size - 1) / size; len(pages) != want {
			t.Errorf("in pages of %d the list has %d pages, want %d", size, len(pages), want)
		}
		for index, page := range pages[:len(pages)-1] {
			if len(page) != size {
				t.Errorf("in pages of %d, page %d holds %d workspaces and is not the last", size, index+1, len(page))
			}
		}
	}

	// A workspace created after the first page was read comes at the end.
	first, next := s.page(workspaces+"?page_size=3", "alice")
	if !slices.Equal(told(first, "workspace_key"), keys[:3]) || next == "" {
		t.Fatalf("the first page holds %v and the token %q, want the first three and a token", told(first, "workspace_key"), next)
	}
	keys = append(keys, "space-7")
	s.createWorkspace(cargopai, "alice", "space-7")
	var rest []map[string]any
	for next != "" {
		var items []map[string]any
		items, next = s.page(workspaces+"?page_size=3&page_token="+next, "alice")
		rest = append(rest, items...)
	}
	if got := told(rest, "workspace_key"); !slices.Equal(got, keys[3:]) {
		t.Errorf("behind the first page the list holds %v, want %v", got, keys[3:])
	}

	// The members of a workspace and the owners of a product, in the order
	// they were granted. What is revoked while the list is read is listed
	// no more, and the rest stays where it was.
	memberships := workspaces + "/" + ids[0] + "/memberships"
	owners := "/v1/products/" + cargopai + "/role-assignments"
	var memberLocations, ownerLocations []string
	for _, subject := range []string{"bob", "carol", "dave", "erin", "frank"} {
		memberLocations = append(memberLocations, s.join(cargopai, ids[0], "alice", subject, "internal_author"))
		ownerLocations = append(ownerLocations, s.nameOwner(cargopai, "alice", subject))
	}
	for list, c := range map[string]struct {
		path, member string
		locations    []string
		want, after  []string
	}{
		"the members": {memberships, "member", memberLocations,
			[]string{"bob", "carol", "dave", "erin", "frank"}, []string{"bob", "erin", "frank"}},
		"the owners": {owners, "principal", ownerLocations,
			[]string{"alice", "bob", "carol", "dave", "erin", "frank"}, []string{"alice", "bob", "erin", "frank"}},
	} {
		if got := told(slices.Concat(s.whole(c.path, "alice", 2)...), c.member); !slices.Equal(got, c.want) {
			t.Errorf("%s, in pages of two: %v, want %v", list, got, c.want)
		}
		// carol and dave end between the first page and the second.
		first, next := s.page(c.path+"?page_size=2", "alice")
		s.revoke(c.locations[1], "alice")
		s.revoke(c.locations[2], "alice")
		listed := told(first, c.member)
		for next != "" {
			var items []map[string]any
			items, next = s.page(c.path+"?page_size=2&page_token="+next, "alice")
			listed = append(listed, told(items, c.member)...)
		}
		// The page that was read holds what was there when it was read.
		want := slices.Concat(c.want[:2], c.after[len(c.after)-2:])
		if !slices.Equal(listed, want) {
			t.Errorf("%s, with two revoked behind the first page: %v, want %v", list, listed, want)
		}
		if got := told(slices.Concat(s.whole(c.path, "alice", 2)...), c.member); !slices.Equal(got, c.after) {
			t.Errorf("%s, once two are revoked: %v, want %v", list, got, c.after)
		}
	}

	// The products, to the administrator and to somebody who holds
	// something in several of them.
	registered := []string{"cargopai"}
	for index := range 4 {
		registered = append(registered, fmt.Sprintf("product-%d", index))
		s.register("platform-admin", registered[index+1], []string{"zoe", "alice"}[index%2])
	}
	if got := told(slices.Concat(s.whole("/v1/products", "platform-admin", 2)...), "product_key"); !slices.Equal(got, registered) {
		t.Errorf("the administrator lists the products %v, want %v", got, registered)
	}
	if got, want := told(slices.Concat(s.whole("/v1/products", "alice", 1)...), "product_key"), []string{"cargopai", "product-1", "product-3"}; !slices.Equal(got, want) {
		t.Errorf("alice lists the products %v, want %v", got, want)
	}

	// Without a size a page holds fifty items.
	bulk := s.register("platform-admin", "bulk", "alice")
	for index := range 51 {
		s.createWorkspace(bulk, "alice", fmt.Sprintf("room-%d", index))
	}
	page, next := s.page("/v1/products/"+bulk+"/workspaces", "alice")
	if len(page) != 50 || next == "" {
		t.Fatalf("without a size the first page holds %d workspaces and the token %q, want 50 and a token", len(page), next)
	}
	if page, next := s.page("/v1/products/"+bulk+"/workspaces?page_token="+next, "alice"); len(page) != 1 || next != "" ||
		page[0]["workspace_key"] != "room-50" {
		t.Errorf("the second page holds %v and the token %q, want the last workspace and no token", told(page, "workspace_key"), next)
	}

	// Reading left no record.
	if got := s.recorded(func(record audited) bool { return record.Outcome == "DENIED" }); len(got) != 0 {
		t.Errorf("the lists left the refusals %v, want none", got)
	}
}

// A token names a position and grants nothing. What a caller may read is
// decided with every page, from what it holds then: a token another caller
// was given opens nothing, and a caller whose role was revoked since the
// page before is a stranger to the next one. What is no token, and a size
// that is none, changes nothing and leaves no record.
func TestAPageTokenGrantsNothingAndWhatIsNoneIsRefused(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	workspaces, owners := "/v1/products/"+cargopai+"/workspaces", "/v1/products/"+cargopai+"/role-assignments"
	var ids []string
	for _, key := range []string{"first", "second", "third"} {
		ids = append(ids, s.createWorkspace(cargopai, "alice", key))
	}
	s.join(cargopai, ids[0], "alice", "carol", "internal_author")
	s.join(cargopai, ids[2], "alice", "dave", "internal_author")
	franks := s.nameOwner(cargopai, "alice", "frank")
	s.nameOwner(cargopai, "alice", "grace")

	// The token alice is given behind the first workspace, in the hands of
	// others: a member lists the workspaces of its own that are behind it,
	// and a stranger and the administrator are answered as without it.
	_, token := s.page(workspaces+"?page_size=1", "alice")
	if token == "" {
		t.Fatal("the first page of three workspaces hands out no token")
	}
	behind := workspaces + "?page_token=" + token
	if items, _ := s.page(behind, "alice"); !slices.Equal(told(items, "workspace_key"), []string{"second", "third"}) {
		t.Errorf("alice lists %v behind the first workspace, want the other two", told(items, "workspace_key"))
	}
	if items, _ := s.page(behind, "carol"); len(items) != 0 {
		t.Errorf("carol, a member of the first workspace, lists %v behind it, want nothing", told(items, "workspace_key"))
	}
	if items, _ := s.page(behind, "dave"); !slices.Equal(told(items, "workspace_key"), []string{"third"}) {
		t.Errorf("dave, a member of the third workspace, lists %v behind the first, want his own", told(items, "workspace_key"))
	}
	s.expect("with the token of an owner", "mallory", []exchange{{http.MethodGet, behind, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"}})
	s.expect("with the token of an owner", "platform-admin", []exchange{{http.MethodGet, behind, "", http.StatusForbidden, "FORBIDDEN"}})

	// frank reads the first page as an owner. His role is revoked before he
	// asks for the second: the token he holds opens nothing.
	_, franksToken := s.page(workspaces+"?page_size=2", "frank")
	s.revoke(franks, "alice")
	s.expect("once his role is revoked", "frank", []exchange{
		{http.MethodGet, workspaces + "?page_size=2&page_token=" + franksToken, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
		{http.MethodGet, workspaces, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	})

	before := len(s.audit())
	// What is no token of the list, and a page that cannot be: the same
	// answer to an owner and to a stranger, who learns nothing from it.
	_, ownersToken := s.page(owners+"?page_size=1", "alice")
	if ownersToken == "" {
		t.Fatal("the first page of two owners hands out no token")
	}
	for _, subject := range []string{"alice", "mallory"} {
		s.expect("with what is no page", subject, []exchange{
			{http.MethodGet, workspaces + "?page_token=bm90LWEtdG9rZW4", "", http.StatusBadRequest, "PAGE_TOKEN_INVALID"},
			// The token of the owners is no token of the workspaces.
			{http.MethodGet, workspaces + "?page_token=" + ownersToken, "", http.StatusBadRequest, "PAGE_TOKEN_INVALID"},
			{http.MethodGet, workspaces + "?page_token=" + token[:len(token)-2], "", http.StatusBadRequest, "PAGE_TOKEN_INVALID"},
			{http.MethodGet, workspaces + "?page_token=not.a.token", "", http.StatusBadRequest, "REQUEST_INVALID"},
			{http.MethodGet, workspaces + "?page_size=0", "", http.StatusBadRequest, "REQUEST_INVALID"},
			{http.MethodGet, workspaces + "?page_size=101", "", http.StatusBadRequest, "REQUEST_INVALID"},
			{http.MethodGet, workspaces + "?page_size=ten", "", http.StatusBadRequest, "REQUEST_INVALID"},
			{http.MethodGet, workspaces + "?page_size=", "", http.StatusBadRequest, "REQUEST_INVALID"},
			// A page is asked for in the words of the contract.
			{http.MethodGet, workspaces + "?pageSize=1", "", http.StatusBadRequest, "REQUEST_INVALID"},
			{http.MethodGet, workspaces + "?offset=1", "", http.StatusBadRequest, "REQUEST_INVALID"},
			{http.MethodGet, "/v1/products?page_size=1&page=2", "", http.StatusBadRequest, "REQUEST_INVALID"},
			{http.MethodGet, owners + "?page_token=" + token, "", http.StatusBadRequest, "PAGE_TOKEN_INVALID"},
		})
	}
	// Whoever sends the request is asked first.
	if got := s.call(http.MethodGet, workspaces+"?page_size=0", "", ""); got.status != http.StatusUnauthorized || got.code() != "CREDENTIALS_MISSING" {
		t.Errorf("a page that cannot be, without credentials: %d %v, want 401 CREDENTIALS_MISSING", got.status, got.body)
	}
	if after := len(s.audit()); after != before {
		t.Errorf("what is no page left %d audit records, want none", after-before)
	}

	// The refusals above are on record, each once, and no list that was
	// answered is.
	want := map[audited]int{
		{"mallory", "workspace.read", "DENIED", "DENY-03", cargopai, "Workspace", ""}:        1,
		{"platform-admin", "workspace.read", "DENIED", "DENY-03", cargopai, "Workspace", ""}: 1,
		{"frank", "workspace.read", "DENIED", "DENY-03", cargopai, "Workspace", ""}:          2,
	}
	if got := s.recorded(func(record audited) bool { return record.Outcome == "DENIED" }); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the audit trail holds the refusals\n %v\nwant\n %v", got, want)
	}
}
