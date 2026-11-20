package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestAChangeSaysWhatItDoesToWhichGenerationUnderWhichKey(t *testing.T) {
	valid := map[string]Change{
		"the first activation":         {Action: Activate, ReleaseID: "release", IdempotencyKey: "k"},
		"an activation with a reason":  {Action: Activate, ExpectedGeneration: 3, ReleaseID: "release", Reason: "ship it", IdempotencyKey: "deploy:2026-11-20_1.0"},
		"a withdrawal":                 {Action: Withdraw, ExpectedGeneration: 1, Reason: "wrong audience", IdempotencyKey: strings.Repeat("k", 128)},
		"a return to a release":        {Action: Restore, ExpectedGeneration: 2, ReleaseID: "release", Reason: "roll back", IdempotencyKey: "k"},
		"a reason of the longest size": {Action: Withdraw, ExpectedGeneration: 1, Reason: strings.Repeat("理", 500), IdempotencyKey: "k"},
	}
	for name, change := range valid {
		if err := change.Validate(); err != nil {
			t.Errorf("%s is refused: %v", name, err)
		}
	}
	change := func(edit func(*Change)) Change {
		c := Change{Action: Restore, ExpectedGeneration: 2, ReleaseID: "release", Reason: "roll back", IdempotencyKey: "k"}
		edit(&c)
		return c
	}
	invalid := map[string]Change{
		"a generation below zero":         change(func(c *Change) { c.ExpectedGeneration = -1 }),
		"a generation nothing follows":    change(func(c *Change) { c.ExpectedGeneration = math.MaxInt64 }),
		"no key":                          change(func(c *Change) { c.IdempotencyKey = "" }),
		"a key with a space":              change(func(c *Change) { c.IdempotencyKey = "a b" }),
		"a key longer than allowed":       change(func(c *Change) { c.IdempotencyKey = strings.Repeat("k", 129) }),
		"an action nobody defined":        change(func(c *Change) { c.Action = "PAUSE" }),
		"no action":                       change(func(c *Change) { c.Action = "" }),
		"an activation of nothing":        {Action: Activate, IdempotencyKey: "k"},
		"a withdrawal that names one":     {Action: Withdraw, ExpectedGeneration: 1, ReleaseID: "release", Reason: "why", IdempotencyKey: "k"},
		"a withdrawal without a reason":   {Action: Withdraw, ExpectedGeneration: 1, IdempotencyKey: "k"},
		"a return to nothing":             change(func(c *Change) { c.ReleaseID = "" }),
		"a return without a reason":       change(func(c *Change) { c.Reason = "" }),
		"a reason with space around it":   change(func(c *Change) { c.Reason = " roll back" }),
		"a reason of two lines":           change(func(c *Change) { c.Reason = "roll\nback" }),
		"a reason longer than allowed":    change(func(c *Change) { c.Reason = strings.Repeat("理", 501) }),
		"an activation with a bad reason": {Action: Activate, ReleaseID: "release", Reason: "ship\tit", IdempotencyKey: "k"},
	}
	for name, change := range invalid {
		if err := change.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want %v", name, err, ErrInvalid)
		}
	}
}

// A slot moves one generation at a time, from the generation the caller
// read. Every move is an operation that says what was active before.
func TestASlotMovesOneGenerationAtATimeFromTheOneThatWasRead(t *testing.T) {
	by, at := Principal{Issuer: "https://issuer.example", Subject: "publisher"}, time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	empty := Slot{ID: "slot", TargetID: "target", AssetID: "asset"}
	active := Slot{ID: "slot", TargetID: "target", AssetID: "asset", ActiveReleaseID: "first", Generation: 4}
	withdrawn := Slot{ID: "slot", TargetID: "target", AssetID: "asset", Generation: 5}
	for slot, want := range map[Slot]string{empty: "EMPTY", active: "ACTIVE", withdrawn: "WITHDRAWN"} {
		if got := slot.Visibility(); got != want {
			t.Errorf("a slot at generation %d with %q active is %s, want %s", slot.Generation, slot.ActiveReleaseID, got, want)
		}
	}

	moves := map[string]struct {
		slot   Slot
		change Change
		want   Operation
	}{
		"the first activation": {empty, Change{Action: Activate, ReleaseID: "first"},
			Operation{Generation: 1, Action: Activate, ReleaseID: "first"}},
		"another release": {active, Change{Action: Activate, ExpectedGeneration: 4, ReleaseID: "second", Reason: "ship"},
			Operation{Generation: 5, Action: Activate, PreviousReleaseID: "first", ReleaseID: "second", Reason: "ship"}},
		"a withdrawal": {active, Change{Action: Withdraw, ExpectedGeneration: 4, Reason: "wrong audience"},
			Operation{Generation: 5, Action: Withdraw, PreviousReleaseID: "first", Reason: "wrong audience"}},
		"a return after a withdrawal": {withdrawn, Change{Action: Restore, ExpectedGeneration: 5, ReleaseID: "first", Reason: "resume"},
			Operation{Generation: 6, Action: Restore, ReleaseID: "first", Reason: "resume"}},
	}
	for name, move := range moves {
		want := move.want
		want.ID, want.SlotID, want.By, want.At = "operation", "slot", by, at
		if got, err := move.slot.Operation(move.change, "operation", by, at); err != nil || got != want {
			t.Errorf("%s: got %+v, %v; want %+v", name, got, err, want)
		}
	}

	refused := map[string]struct {
		slot   Slot
		change Change
		want   error
	}{
		"a generation that was read earlier":    {active, Change{Action: Activate, ExpectedGeneration: 3, ReleaseID: "second"}, ErrGeneration},
		"a generation that is not there yet":    {active, Change{Action: Withdraw, ExpectedGeneration: 5, Reason: "why"}, ErrGeneration},
		"a first activation of a slot in use":   {active, Change{Action: Activate, ReleaseID: "second"}, ErrGeneration},
		"a withdrawal of nothing":               {withdrawn, Change{Action: Withdraw, ExpectedGeneration: 5, Reason: "why"}, ErrNoActiveRelease},
		"a withdrawal from an empty slot":       {empty, Change{Action: Withdraw, Reason: "why"}, ErrNoActiveRelease},
		"an activation of what is active":       {active, Change{Action: Activate, ExpectedGeneration: 4, ReleaseID: "first"}, ErrAlreadyActive},
		"a return to what is active":            {active, Change{Action: Restore, ExpectedGeneration: 4, ReleaseID: "first", Reason: "again"}, ErrAlreadyActive},
		"the generation before the active one":  {active, Change{Action: Activate, ExpectedGeneration: 3, ReleaseID: "first"}, ErrGeneration},
		"a withdrawal under an old generation":  {withdrawn, Change{Action: Withdraw, ExpectedGeneration: 4, Reason: "why"}, ErrGeneration},
		"an activation under a new generation":  {empty, Change{Action: Activate, ExpectedGeneration: 1, ReleaseID: "first"}, ErrGeneration},
		"a return under the wrong generation":   {withdrawn, Change{Action: Restore, ExpectedGeneration: 4, ReleaseID: "first", Reason: "resume"}, ErrGeneration},
		"a withdrawal of an empty, later slot":  {empty, Change{Action: Withdraw, ExpectedGeneration: 1, Reason: "why"}, ErrGeneration},
		"an activation that skips a generation": {active, Change{Action: Activate, ExpectedGeneration: 6, ReleaseID: "second"}, ErrGeneration},
	}
	for name, c := range refused {
		if got, err := c.slot.Operation(c.change, "operation", by, at); !errors.Is(err, c.want) || got != (Operation{}) {
			t.Errorf("%s: got %+v, %v; want %v and no operation", name, got, err, c.want)
		}
	}
}

// A request that is sent again under its key is the request it was: by the
// same caller, of the same generation, action, release and reason. Anything
// else under that key is another request.
func TestARetryIsTheRequestItWasByTheCallerItWasFrom(t *testing.T) {
	by := Principal{Issuer: "https://issuer.example", Subject: "publisher"}
	sent := Change{Action: Restore, ExpectedGeneration: 2, ReleaseID: "first", Reason: "roll back", IdempotencyKey: "rollback-1"}
	recorded := Operation{ID: "operation", SlotID: "slot", Generation: 3, Action: Restore, PreviousReleaseID: "second",
		ReleaseID: "first", Reason: "roll back", By: by}
	if !recorded.Matches(sent, by) {
		t.Fatal("the request as it was sent is not taken for the one that was recorded")
	}
	others := map[string]func(*Change, *Principal){
		"another caller":          func(_ *Change, p *Principal) { p.Subject = "colleague" },
		"the same name elsewhere": func(_ *Change, p *Principal) { p.Issuer = "https://other.example" },
		"another generation":      func(c *Change, _ *Principal) { c.ExpectedGeneration = 3 },
		"another action":          func(c *Change, _ *Principal) { c.Action = Activate },
		"another release":         func(c *Change, _ *Principal) { c.ReleaseID = "second" },
		"another reason":          func(c *Change, _ *Principal) { c.Reason = "roll back!" },
	}
	for name, edit := range others {
		change, caller := sent, by
		edit(&change, &caller)
		if recorded.Matches(change, caller) {
			t.Errorf("%s is taken for the request that was recorded", name)
		}
	}
	if got, want := sent.KeyDigest(), fmt.Sprintf("%x", sha256.Sum256([]byte("rollback-1"))); got != want {
		t.Errorf("the key is kept as %s, want its SHA-256 %s", got, want)
	}
}

func TestAPortalKeyAndAReasonAreHeldToTheirForm(t *testing.T) {
	for _, key := range []string{"engineering", "a", "portal-1", "9", strings.Repeat("p", 63)} {
		if err := ValidatePortalKey(key); err != nil {
			t.Errorf("the portal key %q is refused: %v", key, err)
		}
	}
	for _, key := range []string{"", "-portal", "Portal", "a_b", "a b", "门户", strings.Repeat("p", 64)} {
		if err := ValidatePortalKey(key); !errors.Is(err, ErrInvalid) {
			t.Errorf("the portal key %q: got %v, want %v", key, err, ErrInvalid)
		}
	}
	for _, reason := range []string{"investigate publication", "调查", strings.Repeat("r", 500)} {
		if err := ValidateReason(reason); err != nil {
			t.Errorf("the reason %q is refused: %v", reason[:min(len(reason), 20)], err)
		}
	}
	for _, reason := range []string{"", " why", "why ", "why\nnot", "why\x00", strings.Repeat("r", 501)} {
		if err := ValidateReason(reason); !errors.Is(err, ErrInvalid) {
			t.Errorf("the reason %q: got %v, want %v", reason[:min(len(reason), 20)], err, ErrInvalid)
		}
	}
	if block := (AccessBlock{}); !block.Active() {
		t.Error("a block that was not cleared is not active")
	}
	if block := (AccessBlock{ClearedAt: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}); block.Active() {
		t.Error("a block that was cleared is active")
	}
}
