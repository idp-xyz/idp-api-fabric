package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type AssetID string
type ProductID string
type WorkspaceID string

type Principal struct {
	Issuer  string
	Subject string
}

type Kind string

const (
	KindHTTP  Kind = "HTTP"
	KindEvent Kind = "EVENT"
)

func (k Kind) Valid() bool { return k == KindHTTP || k == KindEvent }

type Lifecycle string

// A new asset has never been approved as stable (F03 §8.4).
const Experimental Lifecycle = "EXPERIMENTAL"

var (
	ErrInvalid = errors.New("invalid API asset")
	uuid       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	key        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$`)
)

func ValidIdentifier[ID ~string](value ID) bool { return uuid.MatchString(string(value)) }
func ValidKey(value string) bool                { return key.MatchString(value) }

// Asset owns the stable identity of an API in the Catalog. Its owner is a
// Workspace of the same product. Contract revisions refer to its ID.
type Asset struct {
	ID          AssetID
	ProductID   ProductID
	WorkspaceID WorkspaceID
	Key         string
	DisplayName string
	DomainKey   string
	Kind        Kind
	Lifecycle   Lifecycle
	Version     int64
	CreatedAt   time.Time
	CreatedBy   Principal
}

func Register(id AssetID, product ProductID, workspace WorkspaceID, assetKey, name, domainKey string, kind Kind, caller Principal, now time.Time) (Asset, error) {
	switch {
	case !ValidIdentifier(id) || !ValidIdentifier(product) || !ValidIdentifier(workspace):
		return Asset{}, fmt.Errorf("%w: asset, product and workspace require stable identifiers", ErrInvalid)
	case !ValidKey(assetKey) || !ValidKey(domainKey):
		return Asset{}, fmt.Errorf("%w: asset and domain keys are lower-case keys", ErrInvalid)
	case !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > 120 ||
		strings.TrimSpace(name) != name || strings.ContainsFunc(name, unicode.IsControl):
		return Asset{}, fmt.Errorf("%w: the display name must be 1 to 120 characters without surrounding whitespace or control characters", ErrInvalid)
	case !kind.Valid():
		return Asset{}, fmt.Errorf("%w: the API kind must be HTTP or EVENT", ErrInvalid)
	case caller.Issuer == "" || caller.Subject == "" || now.IsZero():
		return Asset{}, fmt.Errorf("%w: registration needs an authenticated actor and time", ErrInvalid)
	}
	return Asset{
		ID: id, ProductID: product, WorkspaceID: workspace, Key: assetKey, DisplayName: name,
		DomainKey: domainKey, Kind: kind, Lifecycle: Experimental, Version: 1,
		CreatedAt: now.UTC().Truncate(time.Microsecond), CreatedBy: caller,
	}, nil
}
