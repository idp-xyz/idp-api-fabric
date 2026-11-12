package ids

import (
	"regexp"
	"testing"
)

// A version 4 UUID of the RFC 9562 variant, in lower case.
var canonical = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewUUIDIsACanonicalVersion4UUID(t *testing.T) {
	seen := make(map[string]bool)
	for range 1000 {
		id := NewUUID()
		if !canonical.MatchString(id) {
			t.Fatalf("%q is not a canonical version 4 UUID", id)
		}
		if seen[id] {
			t.Fatalf("%q was generated twice", id)
		}
		seen[id] = true
	}
	if id := (UUIDs{}).NewID(); !canonical.MatchString(id) {
		t.Fatalf("the generator returned %q, want a canonical version 4 UUID", id)
	}
}
