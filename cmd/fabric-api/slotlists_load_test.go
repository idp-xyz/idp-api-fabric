package main

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This is an opt-in measurement, not a timing assertion of the CI suite. It
// answers one question: how long does a page of slots take to read when every
// slot of it points at a release, since the approval of each of those is
// worked out again as the page is read. It runs the whole service as the
// functional tests do: the transport, the validator, the verifier of the
// tokens, the use cases and a real PostgreSQL reached as the application role.
// Only the identity provider is a stand-in. The slots are made through the API,
// each of an asset of its own with the revision, the check, the review, the
// release and the activation that a slot rests on, so that the page reads what
// a page reads.
//
// Run it without -race, which would change the measurement:
//
//	IDP_FABRIC_SLOT_LIST_LOAD=1 go test -count=1 -v -run TestAPageOfSlotsWithReleasesIsReadInTheTimeItTakes ./cmd/fabric-api
//
// with the two DSNs the other tests take. IDP_FABRIC_SLOT_LIST_LOAD_SLOTS (100
// by default) is the number of slots of the target, IDP_FABRIC_SLOT_LIST_LOAD_PADDING
// (0) the bytes of comment added to the source of each revision, which makes
// the artifact of its release larger up to what is allowed (the source of a
// revision is at most 40 KiB), and IDP_FABRIC_SLOT_LIST_LOAD_READS (7) how
// many times each page is read. The numbers are of one machine, a request at
// a time, with a warm database, and are good for their order of magnitude.
func TestAPageOfSlotsWithReleasesIsReadInTheTimeItTakes(t *testing.T) {
	if os.Getenv("IDP_FABRIC_SLOT_LIST_LOAD") != "1" {
		t.Skip("opt-in measurement; see the comment of the test")
	}
	slots, padding, reads := load(t, "SLOTS", 100), load(t, "PADDING", 0), load(t, "READS", 7)
	if slots < 1 || padding < 0 || padding > 39000 || reads < 1 {
		t.Fatalf("%d slots, %d bytes of padding and %d reads: the slots and the reads are at least one, and the padding at most 39000", slots, padding, reads)
	}

	w := slotsFor(t)
	target, _ := w.target(internalSandbox, "engineering")
	line := "# " + strings.Repeat("x", 76) + "\n"
	source := revisionSource + strings.Repeat(line, padding/len(line))

	started := time.Now()
	var first string
	for index := range slots {
		asset := w.assetIn(w.product, w.workspace, fmt.Sprintf("load-%03d", index))
		_, _, release := asset.released(source, "", "1.0.0")
		slot := assetSlot(target, asset.asset)
		w.require(asset.change(slot, "publisher", "ACTIVATE", 0, release, "", "first"), http.StatusOK, "")
		if index == 0 {
			first = asset.asset
		}
	}
	var artifacts, bytes int64
	if err := w.owner.QueryRow(t.Context(), `SELECT count(*), coalesce(avg(octet_length(payload)), 0)::bigint FROM contractregistry.release_artifact`).
		Scan(&artifacts, &bytes); err != nil {
		t.Fatal(err)
	}
	var version string
	if err := w.owner.QueryRow(t.Context(), `SHOW server_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d slots on one target, each of an asset of its own and a release of %d bytes on average, made through the API in %v; PostgreSQL %s, %d CPUs",
		slots, bytes, time.Since(started).Round(time.Second), version, runtime.NumCPU())

	token := w.token("publisher")
	timed := func(what, path string, want int) {
		t.Helper()

		var durations []time.Duration
		for range reads + 1 {
			begun := time.Now()
			got := w.callWith(http.MethodGet, path, token, "")
			durations = append(durations, time.Since(begun))
			if items, _ := got.body["items"].([]any); got.status != http.StatusOK || (want >= 0 && len(items) != want) {
				t.Fatalf("%s: %d with %d items, want 200 with %d", what, got.status, len(items), want)
			}
		}
		cold, warm := durations[0], slices.Clone(durations[1:])
		slices.Sort(warm)
		t.Logf("%-52s first %8v; then of %d reads: min %8v  median %8v  max %8v", what, cold.Round(time.Microsecond), reads,
			warm[0].Round(time.Microsecond), warm[len(warm)/2].Round(time.Microsecond), warm[len(warm)-1].Round(time.Microsecond))
	}
	page := min(slots, 100)
	timed(fmt.Sprintf("a page of %d slots of the target (page_size=100)", page), target+"/assets?page_size=100", page)
	timed("a page of 50 slots of the target (page_size=50)", target+"/assets?page_size=50", min(slots, 50))
	timed("a page of 1 slot of the target (page_size=1)", target+"/assets?page_size=1", 1)
	timed("the publications of one asset (one slot)", first+"/publications", 1)
	timed("one slot read alone", assetSlot(target, first), -1)
	timed("the target read alone, which reads no slot", target, -1)
}

// load is the number the variable of the measurement says, or what it is by
// default.
func load(t *testing.T, name string, fallback int) int {
	t.Helper()

	value := os.Getenv("IDP_FABRIC_SLOT_LIST_LOAD_" + name)
	if value == "" {
		return fallback
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("IDP_FABRIC_SLOT_LIST_LOAD_%s is %q, want a number", name, value)
	}
	return number
}
