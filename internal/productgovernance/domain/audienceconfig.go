package domain

import (
	"errors"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"
)

// Audience is who a product documents its APIs for (F05 §5.1). Who may read
// the documentation of an audience is decided where it is read; the audience
// says which rule applies.
type Audience string

// The audiences of a product. What each of them may read is a rule of the
// platform, the same for every product, so a product cannot name one of its
// own: a rule would be missing for it.
const (
	// AudiencePublic is anybody, signed in or not.
	AudiencePublic Audience = "public"
	// AudiencePartner is the external developers of the organizations the
	// product gives access to.
	AudiencePartner Audience = "partner"
	// AudienceInternal is the engineers of IDP.
	AudienceInternal Audience = "internal"
)

// Audiences returns the audiences a product can document its APIs for. The
// contract of the API states the same audiences, and a test holds the two
// together.
func Audiences() []Audience {
	return []Audience{AudiencePublic, AudiencePartner, AudienceInternal}
}

// Environment is where the APIs a product documents run (F05 §5.1). As with
// an audience, what an environment allows is a rule of the platform: a
// sandbox is where a developer may try an API, production is where its
// business runs.
type Environment string

// The environments of a product.
const (
	EnvironmentSandbox    Environment = "sandbox"
	EnvironmentProduction Environment = "production"
)

// Environments returns the environments a product can document its APIs in.
// The contract of the API states the same environments, and a test holds the
// two together.
func Environments() []Environment {
	return []Environment{EnvironmentSandbox, EnvironmentProduction}
}

// DocsURLPattern is the pattern of the entry of the documentation: an https
// URL of a host, with an optional port up to 65535. A percent sign must
// start a hex escape. The URL cannot contain whitespace or control
// characters. The host is a name of letters, digits and hyphens, so
// a name that is not ASCII is given in its punycode form, and nothing can
// stand in front of it: a URL that carries credentials does not match.
//
// The contract of the API states the same pattern, and a test holds the two
// together.
const DocsURLPattern = `^https://` + docsURLHostPattern + `(:` + docsURLPortPattern + `)?` +
	`([/?#]([^%\p{Z}\p{Cc}]|%[0-9A-Fa-f]{2})*)?$`

const docsURLHostPattern = `[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*`

// Preserve the caller's spelling, including leading zeros, for a port
// written with at most five digits and in the range 0–65535.
const docsURLPortPattern = `([0-5]?[0-9]{1,4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5])`

var docsURL = regexp.MustCompile(DocsURLPattern)

// DocsURLMaxLength bounds the entry of the documentation, in characters. The
// contract of the API states the same bound.
const DocsURLMaxLength = 2048

// checkDocsURL accepts where an audience enters the documentation, or
// nothing yet. Fabric keeps the URL as it is given and never opens it, so it
// cannot tell whether the host belongs to the product.
func checkDocsURL(value string) error {
	switch {
	case value == "":
		return nil
	case !utf8.ValidString(value):
		return invalid("the entry of the documentation is not valid UTF-8")
	case utf8.RuneCountInString(value) > DocsURLMaxLength:
		return invalid("the entry of the documentation is longer than %d characters", DocsURLMaxLength)
	case !docsURL.MatchString(value):
		return invalid("the entry of the documentation must be a valid https URL without credentials or whitespace")
	}
	return nil
}

// ErrAudienceConfigRemoved reports a change to an audience configuration
// that was removed already. A removed configuration is a record: nothing
// about it changes again.
var ErrAudienceConfigRemoved = errors.New("the audience configuration was removed already")

// AudienceConfig says that a product documents its APIs for one audience in
// one environment, and where that audience enters the documentation (FR-005;
// ProductAudienceConfig, F03 §5.4). A product has at most one in force for an
// audience and an environment, and none to begin with: what a product
// documents for nobody is published to nobody.
//
// A configuration is in force from the moment it is added until it is
// removed. A removed configuration is kept, as the record of what the product
// offered and when (F03 §13). Its audience and its environment never change:
// another pair is another configuration.
type AudienceConfig struct {
	ID          AudienceConfigID
	ProductID   ProductID
	Audience    Audience
	Environment Environment
	// DocsURL is where the audience enters the documentation of the
	// environment. It is empty for as long as nobody said where.
	DocsURL string
	// Version counts the changes of the configuration, for optimistic
	// concurrency and for the order of its events: 1 when it is added, one
	// more with every change, and one more when it is removed.
	Version   int64
	CreatedAt time.Time
	CreatedBy Principal
	// UpdatedAt and UpdatedBy say when the configuration changed last, and
	// who changed it. Until it changes they are those of its creation.
	UpdatedAt time.Time
	UpdatedBy Principal
	// RemovedAt is the zero time for as long as the configuration is in
	// force.
	RemovedAt time.Time
	RemovedBy Principal
}

// ConfigureAudience makes product document its APIs for audience in
// environment, entered at docsURL: in force from at, at version 1.
//
// A suspended product takes no new configuration (INV-01). What it has stays
// as it is, and can still be removed.
func ConfigureAudience(id AudienceConfigID, product Product, audience Audience, environment Environment,
	docsURL string, by Principal, at time.Time) (AudienceConfig, error) {
	if err := product.AdmitNewWork(); err != nil {
		return AudienceConfig{}, err
	}
	if _, err := ParseAudienceConfigID(string(id)); err != nil {
		return AudienceConfig{}, err
	}
	if !slices.Contains(Audiences(), audience) {
		return AudienceConfig{}, invalid("an audience configuration is for one of the audiences of a product")
	}
	if !slices.Contains(Environments(), environment) {
		return AudienceConfig{}, invalid("an audience configuration is for one of the environments of a product")
	}
	if err := checkDocsURL(docsURL); err != nil {
		return AudienceConfig{}, err
	}
	if err := by.Validate(); err != nil {
		return AudienceConfig{}, err
	}
	if at.IsZero() {
		return AudienceConfig{}, invalid("an audience is configured at a known time")
	}
	at = instant(at)
	return AudienceConfig{
		ID: id, ProductID: product.ID, Audience: audience, Environment: environment, DocsURL: docsURL,
		Version: 1, CreatedAt: at, CreatedBy: by, UpdatedAt: at, UpdatedBy: by,
	}, nil
}

// InForce reports whether the product documents its APIs for the audience
// in the environment.
func (c AudienceConfig) InForce() bool {
	return c.RemovedAt.IsZero()
}

// ChangeDocsURL makes docsURL the entry of the documentation from the
// instant at, at the next version. Empty says that there is none. A
// configuration whose entry is docsURL already is returned as it is: nothing
// changes, and its version stays.
//
// product is the product of the configuration. A suspended one takes no
// change (INV-01).
//
// A clock that runs behind the one of the last change does not keep the
// configuration from changing: the change is then at the instant of the last
// one.
func (c AudienceConfig) ChangeDocsURL(product Product, docsURL string, by Principal, at time.Time) (AudienceConfig, error) {
	if !c.InForce() {
		return AudienceConfig{}, ErrAudienceConfigRemoved
	}
	if product.ID != c.ProductID {
		return AudienceConfig{}, invalid("an audience configuration is changed with the product it belongs to")
	}
	if err := product.AdmitNewWork(); err != nil {
		return AudienceConfig{}, err
	}
	if err := checkDocsURL(docsURL); err != nil {
		return AudienceConfig{}, err
	}
	if err := by.Validate(); err != nil {
		return AudienceConfig{}, err
	}
	if at.IsZero() {
		return AudienceConfig{}, invalid("an audience configuration is changed at a known time")
	}
	if docsURL == c.DocsURL {
		return c, nil
	}
	at = instant(at)
	if at.Before(c.UpdatedAt) {
		at = c.UpdatedAt
	}
	c.DocsURL, c.UpdatedAt, c.UpdatedBy, c.Version = docsURL, at, by, c.Version+1
	return c, nil
}

// Remove ends the configuration at the instant at: from then on the product
// documents nothing for the audience in the environment, until it is
// configured again. Who removed it is kept with it.
//
// A clock that runs behind the one of the last change does not keep the
// configuration from being removed: it then ends at the instant of that
// change.
func (c AudienceConfig) Remove(by Principal, at time.Time) (AudienceConfig, error) {
	if !c.InForce() {
		return AudienceConfig{}, ErrAudienceConfigRemoved
	}
	if err := by.Validate(); err != nil {
		return AudienceConfig{}, err
	}
	if at.IsZero() {
		return AudienceConfig{}, invalid("an audience configuration is removed at a known time")
	}
	at = instant(at)
	if at.Before(c.UpdatedAt) {
		at = c.UpdatedAt
	}
	c.RemovedAt, c.RemovedBy, c.Version = at, by, c.Version+1
	return c, nil
}
