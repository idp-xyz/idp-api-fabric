package domain

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Status is what a check, or one rule of it, concluded (F04 §8). The five
// are kept apart wherever a check is shown: a rule that was not run is not a
// rule that passed, and neither is one that could not be judged.
type Status string

const (
	// StatusPass: the rule was evaluated and nothing was found.
	StatusPass Status = "PASS"
	// StatusWarn: the rule was evaluated and has findings that warn.
	StatusWarn Status = "WARN"
	// StatusFail: the rule was evaluated and has findings that block.
	StatusFail Status = "FAIL"
	// StatusUnknown: the rule was evaluated and could not be judged.
	StatusUnknown Status = "UNKNOWN"
	// StatusNotRun: no tool of the check evaluates the rule.
	StatusNotRun Status = "NOT_RUN"
)

// Tool is what evaluated rules in a check: a name and the version of its
// behaviour. Two checks of the same input under the same policy by the same
// tools conclude the same, so a tool that judges anything differently is
// another version.
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

var (
	toolName    = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	toolVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]*$`)
)

const maxToolLabel = 60

func (t Tool) valid() bool {
	return len(t.Name) <= maxToolLabel && toolName.MatchString(t.Name) &&
		len(t.Version) <= maxToolLabel && toolVersion.MatchString(t.Version)
}

// ToolManifest writes the tools of a check out one way, in the order of
// their names. It is what is stored with the check, and its digest tells one
// set of tools from another.
func ToolManifest(tools []Tool) string {
	ordered := slices.Clone(tools)
	slices.SortFunc(ordered, func(a, b Tool) int { return strings.Compare(a.Name, b.Name) })
	encoded, err := json.Marshal(ordered)
	if err != nil {
		panic(fmt.Sprintf("encode a tool manifest: %v", err))
	}
	return string(encoded)
}

// ToolDigest is the SHA-256 digest of the manifest of the tools.
func ToolDigest(tools []Tool) string { return digestOf(ToolManifest(tools)) }

// RecordedTools reads the tools of a check back from the manifest it was
// stored with, and fails with ErrCheckRunDamaged for anything ToolManifest
// did not write.
func RecordedTools(manifest string) ([]Tool, error) {
	decoder := json.NewDecoder(strings.NewReader(manifest))
	decoder.DisallowUnknownFields()
	var tools []Tool
	if err := decoder.Decode(&tools); err != nil || len(tools) == 0 || ToolManifest(tools) != manifest {
		return nil, ErrCheckRunDamaged
	}
	for i, tool := range tools {
		if !tool.valid() || (i > 0 && tools[i-1].Name == tool.Name) {
			return nil, ErrCheckRunDamaged
		}
	}
	return tools, nil
}

// SourceFile is one file of the source a check reads.
type SourceFile struct {
	Path    string
	Content string
}

// Source is what a check reads: the files a revision was recorded from, as
// Contract Registry hands them over, and the digest of their closure. That
// digest is the input digest of the check: it covers the entrypoint and the
// path and bytes of every file.
type Source struct {
	Entrypoint string
	Files      []SourceFile
	Digest     string
}

// Finding is one place where a source breaks a rule.
//
// Message is written by the tool that found it and says what is wrong and
// what would mend it. It never repeats what was found: the finding of a
// credential must not be the second place the credential is stored in.
type Finding struct {
	RuleID string
	// Level is the severity of the rule. Complete sets it.
	Level Severity
	// Path names the file of the source.
	Path string
	// Pointer is the JSON Pointer of the place inside the file, or empty
	// for a finding that is known by its line alone.
	Pointer string
	// Line counts from 1. Zero is a place whose line is not known.
	Line    int
	Message string
}

const (
	// MaxFindingsPerRule is how many findings of one rule a check keeps.
	// The outcome of the rule counts all of them.
	MaxFindingsPerRule = 50
	// MaxFindingMessage and MaxFindingPointer bound a finding, in
	// characters.
	MaxFindingMessage = 300
	MaxFindingPointer = 1024

	maxFindingLine = 1 << 24
)

// ValidPointer reports whether pointer can be kept as the place of a
// finding: empty, or a JSON Pointer of at most MaxFindingPointer characters
// without control characters. A place whose pointer is not one is known by
// its line.
func ValidPointer(pointer string) bool {
	if pointer == "" {
		return true
	}
	return strings.HasPrefix(pointer, "/") && utf8.ValidString(pointer) &&
		utf8.RuneCountInString(pointer) <= MaxFindingPointer && !strings.ContainsFunc(pointer, unicode.IsControl)
}

// Observation is what a tool reports of one rule it evaluated.
type Observation struct {
	RuleID string
	// Undecided says that the tool could not judge the rule on this
	// source, for instance because a document it had to read is none.
	Undecided bool
	Findings  []Finding
}

// Outcome is what a check concluded of one rule of its policy.
type Outcome struct {
	Rule   Rule
	Status Status
	// Findings counts every finding of the rule, also those the check does
	// not keep.
	Findings int
	// Tool is what evaluated the rule. It is the zero Tool exactly when the
	// rule was not run.
	Tool Tool
}

// Summary counts the rules of a check by what was concluded of them, and
// the findings of all of them.
type Summary struct {
	Pass, Warn, Fail, Unknown, NotRun int
	Findings                          int
}

// Summarize counts outcomes.
func Summarize(outcomes []Outcome) Summary {
	var summary Summary
	for _, outcome := range outcomes {
		switch outcome.Status {
		case StatusPass:
			summary.Pass++
		case StatusWarn:
			summary.Warn++
		case StatusFail:
			summary.Fail++
		case StatusUnknown:
			summary.Unknown++
		case StatusNotRun:
			summary.NotRun++
		}
		summary.Findings += outcome.Findings
	}
	return summary
}

// Status is the status of the check the summary is of: the worst of what
// its rules say, in the order FAIL, UNKNOWN, WARN, PASS.
//
// A check is held to every rule of its policy. One that blocks is a FAIL.
// Short of that, a rule that was not run or could not be judged leaves the
// check UNKNOWN, however many others passed: a rule that passed says
// nothing of another one, and a check does not pass on the rules it happened
// to evaluate (F04 §8). PASS is every rule evaluated and nothing found.
func (s Summary) Status() Status {
	switch {
	case s.Fail > 0:
		return StatusFail
	case s.Unknown > 0 || s.NotRun > 0:
		return StatusUnknown
	case s.Warn > 0:
		return StatusWarn
	default:
		return StatusPass
	}
}

// Principal is who asked for a check.
type Principal struct {
	Issuer  string
	Subject string
}

// RevisionRef names the revision a check is of, under the product, the
// workspace and the asset it belongs to.
type RevisionRef struct {
	ProductID   string
	WorkspaceID string
	AssetID     string
	RevisionID  string
}

// Draft is a check that is about to run.
type Draft struct {
	ID               string
	PolicySnapshotID string
	Revision         RevisionRef
	RequestedBy      Principal
	StartedAt        time.Time
}

// CheckRun is the evidence of one check: what was read, the policy it was
// held to, the tools that evaluated it and what they found. It is written
// once and never changed. Checking again is another check run (F03 §4).
//
// A check run is evidence, not permission: what it concludes does not
// approve a revision and does not publish anything (F02 §8.1).
type CheckRun struct {
	ID               string
	PolicySnapshotID string
	Revision         RevisionRef
	// InputDigest is the digest of the closure that was read.
	InputDigest string
	// PolicySetID and PolicyDigest name the policy the check was held to.
	PolicySetID  string
	PolicyDigest string
	Tools        []Tool
	Status       Status
	// Outcomes holds one outcome for each rule of the policy, in the order
	// of the rule identifiers.
	Outcomes []Outcome
	// Findings are in the order of their rules, then of their files, lines
	// and pointers. A rule has at most MaxFindingsPerRule of them here.
	Findings    []Finding
	StartedAt   time.Time
	CompletedAt time.Time
	RequestedBy Principal
}

// Summary counts the outcomes of the check.
func (r CheckRun) Summary() Summary { return Summarize(r.Outcomes) }

var (
	// ErrInvalidEvaluation reports what a tool handed over that cannot be
	// recorded. It is a failure of the tool, never of the source.
	ErrInvalidEvaluation = errors.New("the evaluation cannot be recorded")
	// ErrCheckRunDamaged reports a stored check run that is not one this
	// model concluded.
	ErrCheckRunDamaged = errors.New("the stored check run is not the one that was recorded")
)

func invalidEvaluation(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidEvaluation, reason)
}

// Complete concludes a check from what tool observed of source. Every rule
// of policy gets an outcome: the rules the tool reports on by what it found,
// the others as not run.
//
// What the tool hands over is held to the policy and to the source: a rule
// the policy does not have, a file the source does not have or a message
// that is no single line is refused with ErrInvalidEvaluation rather than
// recorded as evidence.
func Complete(draft Draft, policy Policy, source Source, tool Tool, observations []Observation, completedAt time.Time) (CheckRun, error) {
	revision := draft.Revision
	switch {
	case draft.ID == "" || draft.PolicySnapshotID == "" || revision.ProductID == "" || revision.WorkspaceID == "" ||
		revision.AssetID == "" || revision.RevisionID == "" || draft.RequestedBy.Issuer == "" || draft.RequestedBy.Subject == "":
		return CheckRun{}, invalidEvaluation("the check does not say what it is of or who asked for it")
	case len(policy.rules) == 0:
		return CheckRun{}, invalidEvaluation("the check has no policy")
	case !digestForm.MatchString(source.Digest) || len(source.Files) == 0:
		return CheckRun{}, invalidEvaluation("the source has no digest or no file")
	case !tool.valid():
		return CheckRun{}, invalidEvaluation("the tool has no name or no version")
	}
	files := make(map[string]bool, len(source.Files))
	for _, file := range source.Files {
		files[file.Path] = true
	}

	observed := make(map[string]Observation, len(observations))
	for _, observation := range observations {
		rule, known := policy.Rule(observation.RuleID)
		if !known {
			return CheckRun{}, invalidEvaluation("a rule was evaluated that the policy does not have")
		}
		if _, twice := observed[rule.ID]; twice {
			return CheckRun{}, invalidEvaluation("rule " + rule.ID + " was evaluated twice")
		}
		for _, finding := range observation.Findings {
			switch {
			case finding.RuleID != rule.ID:
				return CheckRun{}, invalidEvaluation("a finding of rule " + rule.ID + " names another rule")
			case !files[finding.Path]:
				return CheckRun{}, invalidEvaluation("a finding of rule " + rule.ID + " is in a file the source does not have")
			case !ValidPointer(finding.Pointer) || finding.Line < 0 || finding.Line > maxFindingLine:
				return CheckRun{}, invalidEvaluation("a finding of rule " + rule.ID + " is at no place of a file")
			case !plainText(finding.Message, MaxFindingMessage):
				return CheckRun{}, invalidEvaluation("a finding of rule " + rule.ID + " has no message of one line")
			}
		}
		observed[rule.ID] = observation
	}

	run := CheckRun{
		ID: draft.ID, PolicySnapshotID: draft.PolicySnapshotID, Revision: revision, InputDigest: source.Digest,
		PolicySetID: policy.setID, PolicyDigest: policy.Digest(), Tools: []Tool{tool},
		Outcomes: make([]Outcome, 0, len(policy.rules)), RequestedBy: draft.RequestedBy,
		StartedAt: draft.StartedAt.UTC().Truncate(time.Microsecond),
	}
	// A clock that was set back between the two readings does not make a
	// check end before it began.
	run.CompletedAt = completedAt.UTC().Truncate(time.Microsecond)
	if run.CompletedAt.Before(run.StartedAt) {
		run.CompletedAt = run.StartedAt
	}
	for _, rule := range policy.rules {
		observation, evaluated := observed[rule.ID]
		if !evaluated {
			run.Outcomes = append(run.Outcomes, Outcome{Rule: rule, Status: StatusNotRun})
			continue
		}
		run.Outcomes = append(run.Outcomes, Outcome{
			Rule: rule, Status: statusOf(rule, observation), Findings: len(observation.Findings), Tool: tool,
		})
		found := slices.Clone(observation.Findings)
		slices.SortStableFunc(found, func(a, b Finding) int {
			return cmp.Or(strings.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line),
				strings.Compare(a.Pointer, b.Pointer), strings.Compare(a.Message, b.Message))
		})
		for i, finding := range found {
			if i == MaxFindingsPerRule {
				break
			}
			finding.Level = rule.Severity
			run.Findings = append(run.Findings, finding)
		}
	}
	run.Status = run.Summary().Status()
	return run, nil
}

// statusOf is what an observation says of its rule. Findings of a rule that
// blocks are a FAIL whatever else is undecided. Short of that an undecided
// rule is UNKNOWN, also when it has findings that warn: what is known of it
// is less than what the rule asks.
func statusOf(rule Rule, observation Observation) Status {
	switch {
	case len(observation.Findings) > 0 && rule.Severity == SeverityBlock:
		return StatusFail
	case observation.Undecided:
		return StatusUnknown
	case len(observation.Findings) > 0:
		return StatusWarn
	default:
		return StatusPass
	}
}

// Recorded reads a check run back from what storage holds of it and the
// policy it was stored with. It returns the run with the rules of the policy
// in its outcomes, and fails with ErrCheckRunDamaged unless the run is one
// Complete could have concluded under that policy: every rule of the policy
// has its outcome, each outcome agrees with its rule and its findings, and
// the status of the run is what its outcomes add up to.
//
// What is checked is that the rows agree with each other. Someone who
// rewrites all of them alike is not found out here.
func Recorded(run CheckRun, policy Policy) (CheckRun, error) {
	revision := run.Revision
	switch {
	case run.ID == "" || run.PolicySnapshotID == "" || revision.ProductID == "" || revision.WorkspaceID == "" ||
		revision.AssetID == "" || revision.RevisionID == "" || run.RequestedBy.Issuer == "" || run.RequestedBy.Subject == "":
		return CheckRun{}, ErrCheckRunDamaged
	case !digestForm.MatchString(run.InputDigest) || run.PolicyDigest != policy.Digest() || len(policy.rules) == 0:
		return CheckRun{}, ErrCheckRunDamaged
	case len(run.Tools) == 0 || len(run.Outcomes) != len(policy.rules) || run.CompletedAt.Before(run.StartedAt):
		return CheckRun{}, ErrCheckRunDamaged
	}
	for _, tool := range run.Tools {
		if !tool.valid() {
			return CheckRun{}, ErrCheckRunDamaged
		}
	}

	recorded := run
	recorded.PolicySetID = policy.setID
	recorded.Outcomes = make([]Outcome, len(run.Outcomes))
	kept := make(map[string]int, len(policy.rules))
	for _, finding := range run.Findings {
		kept[finding.RuleID]++
	}
	for i, outcome := range run.Outcomes {
		rule := policy.rules[i]
		if outcome.Rule.ID != rule.ID || !agrees(rule, outcome, run.Tools) || kept[rule.ID] != min(outcome.Findings, MaxFindingsPerRule) {
			return CheckRun{}, ErrCheckRunDamaged
		}
		outcome.Rule = rule
		recorded.Outcomes[i] = outcome
		delete(kept, rule.ID)
	}
	if len(kept) != 0 || run.Status != recorded.Summary().Status() {
		return CheckRun{}, ErrCheckRunDamaged
	}
	for _, finding := range run.Findings {
		rule, _ := policy.Rule(finding.RuleID)
		if finding.Level != rule.Severity || finding.Path == "" || !ValidPointer(finding.Pointer) ||
			finding.Line < 0 || !plainText(finding.Message, MaxFindingMessage) {
			return CheckRun{}, ErrCheckRunDamaged
		}
	}
	return recorded, nil
}

// agrees reports whether an outcome is one statusOf could have given its
// rule, by one of the tools of the check.
func agrees(rule Rule, outcome Outcome, tools []Tool) bool {
	if outcome.Status == StatusNotRun {
		return outcome.Findings == 0 && outcome.Tool == Tool{}
	}
	if !slices.Contains(tools, outcome.Tool) {
		return false
	}
	switch outcome.Status {
	case StatusPass:
		return outcome.Findings == 0
	case StatusFail:
		return outcome.Findings > 0 && rule.Severity == SeverityBlock
	case StatusWarn:
		return outcome.Findings > 0 && rule.Severity == SeverityWarn
	case StatusUnknown:
		return outcome.Findings == 0 || rule.Severity == SeverityWarn
	default:
		return false
	}
}
