package domain

import (
	"cmp"
	"slices"
)

// HeldInWorkspace is what a principal holds in one workspace: its roles
// there, each once and in the order of their names. A workspace in which it
// holds nothing is not one.
type HeldInWorkspace struct {
	WorkspaceID WorkspaceID
	Roles       []Role
}

// HeldRoles returns the roles of the product the principal holds, each once
// and in the order of their names, so that the same standing is told the same
// way every time. It is empty, never nil, for a principal that holds none, and
// the standing itself is left as it was.
func (s Standing) HeldRoles() []Role {
	held := make([]Role, 0, len(s.Roles))
	held = append(held, s.Roles...)
	slices.Sort(held)
	return slices.Compact(held)
}

// HeldInWorkspaces returns, for each workspace the principal is a member of
// now, the roles it holds there, by the identifier of the workspace. A
// membership that was revoked is not held, and a workspace in which every
// membership was is not among them (F03 §13). Several roles in one workspace
// make one entry. It is empty, never nil, for a principal that is a member of
// none.
func (s Standing) HeldInWorkspaces() []HeldInWorkspace {
	roles := make(map[WorkspaceID][]Role)
	for _, membership := range s.Memberships {
		if membership.InForce() {
			roles[membership.WorkspaceID] = append(roles[membership.WorkspaceID], membership.Role)
		}
	}
	held := make([]HeldInWorkspace, 0, len(roles))
	for workspace, in := range roles {
		slices.Sort(in)
		held = append(held, HeldInWorkspace{WorkspaceID: workspace, Roles: slices.Compact(in)})
	}
	slices.SortFunc(held, func(a, b HeldInWorkspace) int { return cmp.Compare(a.WorkspaceID, b.WorkspaceID) })
	return held
}
