package ports

import (
	"context"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
)

type TargetLock int

const (
	Unlocked TargetLock = iota
	HoldTarget
	EditBlock
)

type Store interface {
	PutTarget(context.Context, domain.Target) (domain.Target, bool, error)
	Target(ctx context.Context, productID, targetID string, lock TargetLock) (domain.Target, bool, error)
	Targets(ctx context.Context, productID string, after *TargetPosition, limit int) ([]domain.Target, error)
	Slot(ctx context.Context, request domain.Request, locked bool) (domain.Slot, bool, error)
	EnsureSlot(context.Context, domain.Slot) error
	OperationByKey(ctx context.Context, slotID, keyDigest string) (domain.Operation, bool, error)
	Operations(ctx context.Context, slotID string, after int64, limit int) ([]domain.Operation, error)
	// PriorRelease reports whether a release was ever active on the slot.
	PriorRelease(ctx context.Context, slotID, releaseID string) (bool, error)
	// LatestRelease is the last release activated or restored to the slot,
	// even after a withdrawal clears its pointer.
	LatestRelease(ctx context.Context, slotID string) (string, error)
	Apply(ctx context.Context, slot domain.Slot, op domain.Operation, keyDigest string) error
	HasBlock(ctx context.Context, target domain.Target, assetID, releaseID string) (bool, error)
	Block(ctx context.Context, target domain.Target, id string) (domain.AccessBlock, bool, error)
	Blocks(ctx context.Context, target domain.Target, after int64, limit int) ([]domain.AccessBlock, error)
	PutBlock(context.Context, domain.AccessBlock) (domain.AccessBlock, bool, error)
	HasActiveRelease(context.Context, domain.AccessBlock) (bool, error)
	ClearBlock(context.Context, domain.AccessBlock) error
}

type TargetPosition struct {
	At time.Time
	ID string
}

type AssetAccess struct {
	WorkspaceID string
	Reader      bool
	Publisher   bool
	Active      bool
}

type Artifact struct {
	Digest  string
	Payload []byte
}

type Authority interface {
	ProductKnown(ctx context.Context, request domain.Request) error
	TargetManager(ctx context.Context, request domain.Request) (active bool, err error)
	Security(ctx context.Context, request domain.Request, reviewing bool) error
	ReadBlocks(ctx context.Context, request domain.Request) error
	Config(ctx context.Context, productID, configID string) (audience, environment string, err error)
	Asset(ctx context.Context, request domain.Request) (AssetAccess, error)
	// BlockScope checks optional asset/release IDs for a security operator
	// who already holds the product grant for the target.
	BlockScope(ctx context.Context, block domain.AccessBlock) error
	// Material verifies a release, its approval and its digest. previousID
	// pins the comparison for activation, and reading omits publisher duties.
	Material(ctx context.Context, request domain.Request, workspaceID string, target domain.Target, releaseID, previousID string, reading bool) (Artifact, error)
	// Approval verifies, as a read of the artifact does, that the approval
	// a release rests on holds for the target now. unapproved is why it
	// does not: the refusal an activation of the release would meet. It is
	// nil when the approval holds. Anything else that goes wrong, a
	// release or evidence that is not what was recorded, is err.
	Approval(ctx context.Context, request domain.Request, workspaceID string, target domain.Target, releaseID string) (unapproved, err error)
}

type AuditEntry struct {
	Request    domain.Request
	Action     string
	Rule       string
	Resource   string
	ResourceID string
	Allowed    bool
}

type Event struct {
	Name             string
	Aggregate        string
	AggregateID      string
	AggregateVersion int64
	TargetID         string
	AssetID          string
	ReleaseID        string
	BlockGeneration  int64
	At               time.Time
}

type Transactions interface {
	WithinTransaction(context.Context, func(context.Context) error) error
}

type Clock interface{ Now() time.Time }
type Identifiers interface{ NewID() string }
