package openapi

import (
	"net/netip"
	"regexp"
	"strings"
)

// The two scans read the bytes of every file, line by line, and not the
// documents: a credential in a comment is published with the file as much
// as one in a description. A finding names the line and the kind of what
// was found, and never what was found.

// scan reports the lines of the source that detect has something to say of.
func (b *bundle) scan(detect func(line string) string) judgment {
	var judged judgment
	for _, file := range b.source.Files {
		for index, line := range strings.Split(file.Content, "\n") {
			if message := detect(line); message != "" {
				judged.findings = append(judged.findings, found{place{file: file.Path, line: index + 1}, message})
			}
		}
	}
	return judged
}

// credential is one kind of credential the scan knows the form of.
type credential struct {
	kind    string
	pattern *regexp.Regexp
	// generated says that the pattern has one group and that a match
	// counts only when the group looks like a generated value: see
	// looksGenerated.
	generated bool
}

// credentials are the forms the scan knows. The first two are the ones the
// rule registry illustrates (a bearer token, a secret assigned to a name);
// the others are tokens whose form is published by whoever issues them.
//
// This is a list of known forms, not a proof of absence: a secret of no
// known form in a place of no telling name passes.
var credentials = []credential{
	{"bearer token", regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9._~+/=-]{16,})`), true},
	{"secret assigned to a name", regexp.MustCompile(
		`(?i)(?:api[_-]?key|client[_-]?secret|private[_-]?key|secret[_-]?key|access[_-]?token|refresh[_-]?token|password|passwd)` +
			`["']?\s*[:=]\s*["']?([A-Za-z0-9_/+.=~-]{12,})`), true},
	{"private key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`), false},
	{"JSON Web Token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), false},
	{"AWS access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), false},
	{"GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})`), false},
	{"GitLab token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`), false},
	{"Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), false},
}

// placeholder is how a value says that it stands for a credential and is
// none, as the examples of F04 do.
const placeholder = "DESIGN_EXAMPLE"

// looksGenerated tells a value that may be a credential from a word that
// follows "Bearer" or a name in a sentence: a generated value has a digit in
// it, and a placeholder says that it is one. A token of letters alone is
// missed for that; so is the sentence "Bearer authentication-required" not
// taken for one.
func looksGenerated(value string) bool {
	return strings.ContainsAny(value, "0123456789") && !strings.HasPrefix(strings.ToUpper(value), placeholder)
}

// F04-GOV-004: no line holds a credential.
func credentialOn(line string) string {
	var kinds []string
	for _, known := range credentials {
		matches := known.pattern.FindAllStringSubmatch(line, -1)
		hit := len(matches) > 0 && !known.generated
		for _, match := range matches {
			hit = hit || (known.generated && looksGenerated(match[1]))
		}
		if hit {
			kinds = append(kinds, known.kind)
		}
	}
	for _, address := range addressesOn(line) {
		if address.password {
			kinds = append(kinds, "password in a URL")
			break
		}
	}
	if len(kinds) == 0 {
		return ""
	}
	return "the line holds what has the form of a credential (" + strings.Join(kinds, ", ") +
		"); remove it, and replace an example by a placeholder that begins with " + placeholder
}

// address is a URL found on a line, as far as the scans look at it.
type address struct {
	scheme string
	host   string
	// password says that the URL carries a password in front of its host.
	password bool
}

var (
	// schemed matches the scheme of a URL and the "://" behind it.
	schemed = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://`)
	// notPassword is what a password in a URL does not hold unescaped: the
	// marks of a variable to fill in, as in {password}, and of the pattern
	// a schema states for a URL, as in [^@]+.
	notPassword = "{}$*[]^|"
)

// addressesOn finds the URLs on a line and takes them apart by hand: what
// is on the line need not be a URL a parser accepts, and the scans care for
// the host and the password alone. A URL inside another one, as in a query
// that names where to go next, is found as well.
func addressesOn(line string) []address {
	var addresses []address
	for _, match := range schemed.FindAllStringIndex(line, -1) {
		found := address{scheme: strings.ToLower(line[match[0] : match[1]-len("://")])}
		// The authority ends where the path, the query or the fragment
		// begins, and at the latest where the URL does: at white space, a
		// quote, an angle bracket, a backtick or a backslash.
		authority := line[match[1]:]
		if end := strings.IndexAny(authority, "/?# \t\r\"'<>`\\"); end >= 0 {
			authority = authority[:end]
		}
		host := authority
		if split := strings.LastIndex(authority, "@"); split >= 0 {
			host = authority[split+1:]
			_, password, has := strings.Cut(authority[:split], ":")
			found.password = has && password != "" && !strings.ContainsAny(password, notPassword)
		}
		if strings.HasPrefix(host, "[") {
			// An IPv6 address in brackets, with or without a port. What
			// else stands in brackets is no host: the pattern of a URL,
			// ^https://[a-z0-9]+, begins that way.
			inside, _, _ := strings.Cut(host[1:], "]")
			if _, err := netip.ParseAddr(inside); err != nil {
				inside = ""
			}
			host = inside
		} else {
			// What follows a URL in a sentence or in a flow mapping is not
			// part of its host.
			host = strings.TrimRight(host, ".,;:!?)]}")
			if name, port, has := cutLast(host, ":"); has && isDigits(port) {
				host = name
			}
		}
		found.host = strings.ToLower(strings.TrimSuffix(host, "."))
		addresses = append(addresses, found)
	}
	return addresses
}

func cutLast(text, separator string) (before, after string, found bool) {
	if index := strings.LastIndex(text, separator); index >= 0 {
		return text[:index], text[index+len(separator):], true
	}
	return text, "", false
}

func isDigits(text string) bool {
	return text != "" && strings.Trim(text, "0123456789") == ""
}

// internalSuffixes are the names that resolve inside a private network
// only: the special-use names of RFC 6761, RFC 6762 and RFC 8375, the name
// reserved for private use (.internal), and the ones private networks have
// taken for themselves.
var internalSuffixes = []string{
	".localhost", ".local", ".localdomain", ".internal", ".intranet", ".corp", ".lan", ".home.arpa",
}

// hostName is the form of a host that is a name, or an address written in
// numbers that are not four decimal ones.
var hostName = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]*[a-z0-9_])?(\.[a-z0-9_]([a-z0-9_-]*[a-z0-9_])?)*$`)

// webSchemes are the schemes whose host is always a host of a network. Under
// another scheme the part behind "://" may be anything: the redirect address
// of a mobile application, myapp://callback, names no host.
var webSchemes = map[string]bool{"http": true, "https": true, "ws": true, "wss": true}

// internal reports whether a URL names a host of a private network:
//
//   - under any scheme, an address that is loopback, private, link-local
//     or unspecified, localhost, and a name that ends in one of
//     internalSuffixes;
//   - under a web scheme, also a name without a dot, which nothing outside
//     a private network resolves, and a host that ends in a number without
//     being an address (127.1, 2130706433, 0x7f.1): no name ends in one, so
//     it is an address written another way, and which one cannot be told
//     without resolving it.
//
// What stands where a host would is judged only when it is one: letters,
// digits, hyphens and underscores in labels between dots. A variable to fill
// in is none, and neither is the pattern a schema states for a URL.
func (a address) internal() bool {
	host := a.host
	if parsed, err := netip.ParseAddr(host); err == nil {
		parsed = parsed.Unmap()
		return parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsLinkLocalUnicast() || parsed.IsUnspecified()
	}
	if !hostName.MatchString(host) {
		return false
	}
	if host == "localhost" {
		return true
	}
	for _, suffix := range internalSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	if !webSchemes[a.scheme] {
		return false
	}
	labels := strings.Split(host, ".")
	last := labels[len(labels)-1]
	return len(labels) == 1 || isDigits(last) || strings.HasPrefix(last, "0x")
}

// F04-GOV-010: no line names a URL of a host inside a private network.
func internalAddressOn(line string) string {
	for _, address := range addressesOn(line) {
		if address.internal() {
			return "the line holds a URL of a host inside a private network (a loopback, private or link-local address, " +
				"or a name that resolves only there); a contract that may be published must not disclose it"
		}
	}
	return ""
}
