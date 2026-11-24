package application

import (
	"context"
	"errors"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

// maxSlotPage is the most slots a page holds, as the contract states.
const maxSlotPage = 100

// view is a slot as it is read, whoever reads it and in whichever way: what
// it points at, and what stands between that release and whoever asks for its
// artifact, an access block and an approval that does not hold any more.
// Reading one slot and listing many go through it and through nothing else,
// so that a slot is the same slot wherever it is read.
//
// request names the product and the asset of the slot, and workspaceID is the
// workspace that owns the asset.
func (s *Publication) view(ctx context.Context, request domain.Request, workspaceID string, target domain.Target, slot domain.Slot) (domain.SlotView, error) {
	blocked, err := s.deps.Store.HasBlock(ctx, target, request.AssetID, slot.ActiveReleaseID)
	if err != nil {
		return domain.SlotView{}, err
	}
	view := domain.SlotView{Slot: slot, Blocked: blocked}
	if slot.ActiveReleaseID != "" {
		view.Unapproved, err = s.deps.Authority.Approval(ctx, request, workspaceID, target, slot.ActiveReleaseID)
	}
	return view, err
}

// SlotPage is a page of slots, each as getPublicationSlot reads it. Next is
// where the page that follows starts, for as long as slots follow.
type SlotPage struct {
	Items []domain.SlotView
	Next  *ports.SlotPosition
}

// ListSlots reads the slots of a target that have taken an operation and
// belong to assets the caller reads. What the caller reads is decided before
// the target is looked at, so that whoever may not read learns nothing of
// whether it exists, and it is decided again with every page, in the
// transaction that reads the page. Unlike the read of one slot it does not
// lock the target: a list is no point where an access block has to be ordered
// with what is served, and it is read from what is there when it is read.
func (s *Publication) ListSlots(ctx context.Context, request domain.Request, after *ports.SlotPosition, limit int) (SlotPage, error) {
	if limit < 1 || limit > maxSlotPage {
		return SlotPage{}, domain.ErrInvalid
	}
	var page SlotPage
	err := s.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		readable, err := s.deps.Authority.ReadableAssets(tx, request)
		if err != nil {
			return err
		}
		target, err := s.target(tx, request, ports.Unlocked)
		if err != nil {
			return err
		}
		assets := make([]string, 0, len(readable))
		owners := make(map[string]string, len(readable))
		for _, asset := range readable {
			assets = append(assets, asset.AssetID)
			owners[asset.AssetID] = asset.WorkspaceID
		}
		rows, err := s.deps.Store.Slots(tx, target, assets, after, limit+1)
		if err != nil {
			return err
		}
		page, err = s.slotPage(tx, request, rows, limit, func(slot domain.Slot) (string, error) {
			workspace, found := owners[slot.AssetID]
			if !found {
				return "", errors.New("the store listed a slot of an asset that the caller does not read")
			}
			return workspace, nil
		})
		return err
	})
	return page, s.refused(ctx, request, "publication.read", "PublicationSlot", err)
}

// ListAssetPublications reads the slots of an asset on the targets of its
// product that have taken an operation. Whether the caller reads the slots of
// the asset is decided as for the read of one, by the same function.
func (s *Publication) ListAssetPublications(ctx context.Context, request domain.Request, after *ports.SlotPosition, limit int) (SlotPage, error) {
	if limit < 1 || limit > maxSlotPage {
		return SlotPage{}, domain.ErrInvalid
	}
	var page SlotPage
	err := s.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		access, err := s.asset(tx, request, false)
		if err != nil {
			return err
		}
		rows, err := s.deps.Store.AssetSlots(tx, request.ProductID, request.AssetID, after, limit+1)
		if err != nil {
			return err
		}
		page, err = s.slotPage(tx, request, rows, limit, func(domain.Slot) (string, error) { return access.WorkspaceID, nil })
		return err
	})
	return page, s.refused(ctx, request, "publication.read", "PublicationSlot", err)
}

// slotPage reads each slot of a page as view does. The store was asked for a
// row more than the page holds: that one is not read, it only says that
// slots follow. workspaceOf is the workspace that owns the asset of a slot.
func (s *Publication) slotPage(ctx context.Context, request domain.Request, rows []ports.PlacedSlot, limit int,
	workspaceOf func(domain.Slot) (string, error)) (SlotPage, error) {
	page := SlotPage{Items: make([]domain.SlotView, 0, min(len(rows), limit))}
	for index, row := range rows {
		if index == limit {
			next := rows[limit-1].Position()
			page.Next = &next
			break
		}
		workspace, err := workspaceOf(row.Slot)
		if err != nil {
			return SlotPage{}, err
		}
		asked := request
		asked.TargetID, asked.AssetID = row.Target.ID, row.Slot.AssetID
		item, err := s.view(ctx, asked, workspace, row.Target, row.Slot)
		if err != nil {
			return SlotPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}
