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
	// Slots are the slots of the target on the assets named that have taken
	// an operation: a page of at most limit of them, behind after. No asset
	// named is no slot, and is never read as every asset.
	Slots(ctx context.Context, target domain.Target, assets []string, after *SlotPosition, limit int) ([]PlacedSlot, error)
	// AssetSlots are the slots of the asset on the targets of its product
	// that have taken an operation, a page of them in the order of Slots.
	AssetSlots(ctx context.Context, productID, assetID string, after *SlotPosition, limit int) ([]PlacedSlot, error)
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

// SlotPosition is where a page of slots starts: behind the slot that took
// its first operation at At, of the identifier ID. The slots are listed in
// that order, and neither value of a slot ever changes, so a slot keeps its
// place for as long as it is listed.
type SlotPosition struct {
	At time.Time
	ID string
}

// PlacedSlot is a slot of a list with the target it is on and the position
// it has in the list.
type PlacedSlot struct {
	Slot   domain.Slot
	Target domain.Target
	// Since is the instant the first operation of the slot was accepted.
	Since time.Time
}

// Position is where the page that follows the slot starts.
func (s PlacedSlot) Position() SlotPosition { return SlotPosition{At: s.Since, ID: s.Slot.ID} }

type AssetAccess struct {
	WorkspaceID    string
	Reader         bool
	ArtifactReader bool
	Publisher      bool
	Active         bool
}

// ReadableAsset is an asset whose slots a caller reads, with the workspace
// that owns it.
type ReadableAsset struct {
	AssetID     string
	WorkspaceID string
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
	// ReadableAssets are the assets of the product whose slots the caller
	// reads now, as Asset says of one of them: those of the workspaces where
	// it holds the author, the reviewer or the publisher grant. A caller who
	// cannot know the product is refused as for a product that is missing,
	// and one who holds none of the grants in any workspace of it is
	// refused. One who reads workspaces that own no asset is answered with
	// no asset, which is an answer and not a refusal.
	ReadableAssets(ctx context.Context, request domain.Request) ([]ReadableAsset, error)
	// BlockScope checks optional asset/release IDs for a security operator
	// who already holds the product grant for the target.
	BlockScope(ctx context.Context, block domain.AccessBlock) error
	// Artifact reads the artifact of a release of the asset, for a slot
	// that points at it to serve, and verifies it against the digest the
	// release names. It asks nothing of the approval the release rests on:
	// an approval decides what is put on a slot, and what is on a slot is
	// served until it is withdrawn or blocked.
	Artifact(ctx context.Context, request domain.Request, workspaceID, releaseID string) (Artifact, error)
	// Admission verifies that the caller may put the release on a slot of
	// the target: the release and its artifact are what was recorded, and
	// the caller had no part in the revision, its check or its approval.
	// A release that the slot is to take for the first time is admitted on
	// an approval that holds for the target now, on a check that compared
	// the revision with the one of previousID where that is another one.
	// returning admits a release the slot served before without asking
	// whether its approval still holds.
	Admission(ctx context.Context, request domain.Request, workspaceID string, target domain.Target, releaseID, previousID string, returning bool) error
	// Approval verifies that the approval a release rests on holds for
	// the target now. unapproved is why it does not: the refusal an
	// activation of the release would meet. It is nil when the approval
	// holds. Anything else that goes wrong, a release or evidence that is
	// not what was recorded, is err.
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
