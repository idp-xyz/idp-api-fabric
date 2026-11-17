package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A change is made from the version its If-Match names, and the answer
// carries the version the change made as its ETag.
func TestAChangeIsMadeFromTheVersionItNames(t *testing.T) {
	s := newService(t)
	target := audienceConfigs + "/" + audienceConfigID

	versions := map[string]int64{`"1"`: 1, `"3"`: 3, `"42"`: 42, `"999999999999999999"`: 999999999999999999}
	for tag, version := range versions {
		s.governance.configsChanged = nil
		response := s.do(t, call{Method: http.MethodPut, Target: target, Token: "alice", Body: docsChange,
			Header: map[string]string{"If-Match": tag}})
		if response.Code != http.StatusOK {
			t.Fatalf("If-Match %s: status %d, want 200: %s", tag, response.Code, response.Body)
		}
		if len(s.governance.configsChanged) != 1 || s.governance.configsChanged[0].Version != version {
			t.Errorf("If-Match %s: the use case was asked %+v, want one change from version %d", tag, s.governance.configsChanged, version)
		}
		if got := response.Header().Get("ETag"); got != `"3"` {
			t.Errorf("If-Match %s: ETag %q, want the version of the configuration the use case answered", tag, got)
		}
	}
}

// A change that does not say which version it was made from is refused, not
// made: it would overwrite whatever changed since the caller read. Who sends
// it is still asked first.
func TestAChangeThatNamesNoVersionIsRefused(t *testing.T) {
	s := newService(t)
	change := call{Method: http.MethodPut, Target: audienceConfigs + "/" + audienceConfigID, Body: docsChange}

	wantProblem(t, s.do(t, change), http.StatusUnauthorized, CodeCredentialsMissing, "")
	change.Token = "mallory"
	wantProblem(t, s.do(t, change), http.StatusUnauthorized, CodeTokenInvalid, "")
	change.Token = "alice"
	wantProblem(t, s.do(t, change), http.StatusPreconditionRequired, CodePreconditionRequired,
		"A change needs If-Match: the ETag of what it changes, as it was read.")
	if s.governance.calls() != 0 {
		t.Errorf("%d changes that name no version reached a use case", s.governance.calls())
	}
}

// An If-Match names one version, as it was handed out. Anything else would
// make the change from a version the caller did not read: "*" from any, a
// weak tag from one that is merely alike, a list from whichever is in force.
// An If-Match without a value is no version either, as a page size without a
// value is no size.
func TestAnIfMatchThatNamesNoSingleVersionIsRefused(t *testing.T) {
	values := []string{
		"", "*", `W/"3"`, `"3", "4"`, `"3",`, `3`, `"3`, `3"`, `""`, `"0"`, `"03"`, `"-3"`, `"+3"`, `"3.0"`, `"canary"`,
		`"1234567890123456789"`, `"9223372036854775807"`,
	}
	for _, value := range values {
		s := newService(t)
		response := s.do(t, call{Method: http.MethodPut, Target: audienceConfigs + "/" + audienceConfigID, Token: "alice", Body: docsChange,
			Header: map[string]string{"If-Match": value}})
		wantProblem(t, response, http.StatusBadRequest, CodeRequestInvalid, "The header parameter If-Match does not match the contract.")
		if strings.Contains(response.Body.String(), "canary") || strings.Contains(s.log.String(), "canary") {
			t.Errorf("If-Match %s: the answer or the log repeats what was sent", value)
		}
		if s.governance.calls() != 0 {
			t.Errorf("If-Match %s reached a use case", value)
		}
	}

	// Two of them, each good on its own. Which one counts is not for the
	// server to pick.
	s := newService(t)
	request := httptest.NewRequest(http.MethodPut, audienceConfigs+"/"+audienceConfigID, strings.NewReader(docsChange))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer alice")
	request.Header.Add("If-Match", `"3"`)
	request.Header.Add("If-Match", `"4"`)
	response := httptest.NewRecorder()
	s.handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || members(t, response)["error_code"] != CodeRequestInvalid {
		t.Errorf("two If-Match headers: status %d, want 400 %s: %s", response.Code, CodeRequestInvalid, response.Body)
	}
	if s.governance.calls() != 0 {
		t.Errorf("a change with two If-Match headers reached a use case")
	}
}

func TestTheETagOfAVersionIsItsNumberInDoubleQuotes(t *testing.T) {
	for version, want := range map[int64]string{1: `"1"`, 3: `"3"`, 999999999999999999: `"999999999999999999"`} {
		if got := entityTag(version); got != want {
			t.Errorf("entityTag(%d) = %s, want %s", version, got, want)
		}
		got, err := versionIn(&want)
		if err != nil || got != version {
			t.Errorf("versionIn(%s) = %d, %v; want %d", want, got, err, version)
		}
	}
}

// What the validator lets through, versionIn reads. What it should have
// refused is no request of the caller's: it is an internal error, not a
// problem the caller is told it caused.
func TestAnIfMatchTheValidatorShouldHaveRefusedIsAFailureOfTheService(t *testing.T) {
	for _, value := range []string{"", `"`, `""`, `3`, `"x"`, `"0"`, `"-3"`, `W/"3"`, `"99999999999999999999"`} {
		_, err := versionIn(&value)
		if err == nil {
			t.Errorf("versionIn(%s) read a version", value)
			continue
		}
		if errors.Is(err, errPreconditionRequired) {
			t.Errorf("versionIn(%s) says that no version was named", value)
		}
		if _, named := problemOf(err); named {
			t.Errorf("versionIn(%s) is answered as something the caller did: %v", value, err)
		}
	}
}
