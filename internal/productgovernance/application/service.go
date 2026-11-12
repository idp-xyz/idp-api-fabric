package application

import (
	"context"
	"errors"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

var (
	// ErrForbidden reports a caller that may know the resource exists and
	// may not do what it asked.
	ErrForbidden = errors.New("the caller may not perform this action")

	// ErrProductNotFound reports that there is no such product, or that the
	// caller may not know whether there is. The two are answered alike, so
	// that an identifier cannot be probed (F05 §5.2).
	ErrProductNotFound = errors.New("no such product")

	// ErrWorkspaceNotFound is ErrProductNotFound for a workspace.
	ErrWorkspaceNotFound = errors.New("no such workspace")

	// ErrMembershipNotFound is ErrProductNotFound for a membership. A
	// membership that was revoked is not found either: it grants nothing,
	// and nothing can be done to it.
	ErrMembershipNotFound = errors.New("no such membership")

	// ErrRoleAssignmentNotFound is ErrProductNotFound for a role assignment.
	// As with a membership, one that was revoked is not found either.
	ErrRoleAssignmentNotFound = errors.New("no such role assignment")

	// ErrOwnerIssuerNotTrusted reports an owner named under an identity
	// provider whose principals cannot authenticate here.
	ErrOwnerIssuerNotTrusted = errors.New("an owner is named under an issuer this deployment does not trust")

	// ErrMemberIssuerNotTrusted is ErrOwnerIssuerNotTrusted for the member
	// of a workspace.
	ErrMemberIssuerNotTrusted = errors.New("a member is named under an issuer this deployment does not trust")

	// ErrRefusalNotAudited accompanies a refusal whose audit record could
	// not be written. The refusal stands; this is for whoever operates the
	// service, and it never changes what the caller is told.
	ErrRefusalNotAudited = errors.New("the refusal could not be audited")
)

// The actions of Product Governance, the smallest units authorization
// decides (F05 §2.2).
const (
	ActionProductRegister = "product.register"
	ActionProductRead     = "product.read"
	ActionWorkspaceManage = "workspace.manage"
	ActionWorkspaceRead   = "workspace.read"
	ActionMemberGrant     = "member.grant"
	ActionMemberRevoke    = "member.revoke"
	ActionMemberRead      = "member.read"

	// The access matrix of F05 names no action on the roles of a product.
	// These are named after the event that announces such a role.
	ActionProductRoleGrant  = "product_role.grant"
	ActionProductRoleRevoke = "product_role.revoke"
	ActionProductRoleRead   = "product_role.read"
)

// The rules a decision is recorded under, as the access matrix of F05 names
// them. A read that is allowed is not recorded, so the read actions need no
// permit rule here.
const (
	// rulePlatformAdministrator permits product.register (AC-08). Naming
	// the owners of a product is part of registering it: an administrator
	// that names one afterwards is recorded under the same rule.
	rulePlatformAdministrator = "AC-08"
	// ruleProductOwner permits workspace.manage (AC-09).
	ruleProductOwner = "AC-09"
	// ruleMembersOfOwnProduct permits member.grant (AC-10). The matrix has
	// no row for revoking a membership: whoever may grant one may revoke
	// one, and member.revoke is recorded under the same rule. It has none
	// for the owners of a product either: an owner that names or removes an
	// owner manages who holds what in its own product, and is recorded
	// under this rule as well.
	ruleMembersOfOwnProduct = "AC-10"
	// ruleNoRole refuses whoever holds no role that permits the action
	// (DENY-03, missing_active_membership_or_grant).
	ruleNoRole = "DENY-03"
)

const (
	resourceProduct   = "Product"
	resourceWorkspace = "Workspace"
	// resourceMember is what the access matrix calls the resource of
	// member.grant. Its identifier is that of the membership.
	resourceMember = "Member"
	// resourceRoleAssignment is the resource of the actions on the roles of
	// a product. Its identifier is that of the role assignment.
	resourceRoleAssignment = "RoleAssignment"
)

// Dependencies are the ports the use cases are wired with. All are required.
type Dependencies struct {
	Transactor     ports.Transactor
	Products       ports.Products
	Workspaces     ports.Workspaces
	Roles          ports.RoleAssignments
	Memberships    ports.Memberships
	Administrators ports.PlatformAdministrators
	Issuers        ports.TrustedIssuers
	Audit          ports.AuditTrail
	Events         ports.Events
	Clock          ports.Clock
	Identifiers    ports.Identifiers
}

// ProductGovernance holds the use cases of the context (F03 §9).
//
// Every use case decides for itself whether the caller may do what it asks,
// from the state the server holds (F05 §4.2, §7). The decision is made where
// the data is read, inside the transaction of a write, and a refusal is
// recorded.
type ProductGovernance struct {
	deps Dependencies
}

// New builds the use cases.
func New(deps Dependencies) (*ProductGovernance, error) {
	missing := map[string]bool{
		"a transactor":            deps.Transactor == nil,
		"the products":            deps.Products == nil,
		"the workspaces":          deps.Workspaces == nil,
		"the role assignments":    deps.Roles == nil,
		"the memberships":         deps.Memberships == nil,
		"the administrators":      deps.Administrators == nil,
		"the trusted issuers":     deps.Issuers == nil,
		"the audit trail":         deps.Audit == nil,
		"the events":              deps.Events == nil,
		"a clock":                 deps.Clock == nil,
		"an identifier generator": deps.Identifiers == nil,
	}
	for what, absent := range missing {
		if absent {
			return nil, fmt.Errorf("product governance is wired without %s", what)
		}
	}
	return &ProductGovernance{deps: deps}, nil
}

// refusal is a decision against the caller, to be recorded once the
// transaction that found it has ended.
type refusal struct {
	err   error
	entry ports.AuditEntry
}

// refuse records a refusal and returns its error. It is called outside any
// transaction: a refusal changes nothing, so its record commits on its own.
//
// The caller is refused whether or not the record could be written. A record
// that could not is reported with the refusal, as ErrRefusalNotAudited.
func (s *ProductGovernance) refuse(ctx context.Context, refused refusal) error {
	if err := s.deps.Audit.Record(ctx, refused.entry); err != nil {
		return fmt.Errorf("%w; %w: %v", refused.err, ErrRefusalNotAudited, err)
	}
	return refused.err
}

// known refuses a request that reached a use case without a principal. The
// transport authenticates every request of these operations, so this only
// happens when the wiring is wrong; it is refused all the same, and it cannot
// be audited, since there is nobody to name.
func known(caller domain.Principal) error {
	if err := caller.Validate(); err != nil {
		return fmt.Errorf("%w: the request carries no principal", ErrForbidden)
	}
	return nil
}

// scene is what a use case reads before it decides: the product, what the
// caller holds in it and, as far as the request names them, the workspace
// and the membership, or the role assignment.
type scene struct {
	product      domain.Product
	productFound bool
	// standing is what the caller holds in the product now.
	standing        domain.Standing
	workspace       domain.Workspace
	workspaceFound  bool
	membership      domain.Membership
	membershipFound bool
	assignment      domain.RoleAssignment
	assignmentFound bool
}

// survey reads the scene of a request. An empty identifier names nothing and
// is not looked up.
//
// All of it is read whatever the answer will be, so that what is withheld
// costs what is missing costs. What the caller holds is read here, where the
// decision is made, and never kept from one request to the next: a role or a
// membership that was taken away grants nothing from that moment on
// (F05 §5.3).
func (s *ProductGovernance) survey(ctx context.Context, caller domain.Principal,
	productID domain.ProductID, workspaceID domain.WorkspaceID, membershipID domain.MembershipID) (scene, error) {
	var seen scene
	var err error

	if seen.product, seen.productFound, err = s.deps.Products.Find(ctx, productID); err != nil {
		return scene{}, fmt.Errorf("read the product: %w", err)
	}
	if seen.standing.Roles, err = s.deps.Roles.RolesOf(ctx, productID, caller); err != nil {
		return scene{}, fmt.Errorf("read the roles of the caller: %w", err)
	}
	if seen.standing.Memberships, err = s.deps.Memberships.HeldBy(ctx, productID, caller); err != nil {
		return scene{}, fmt.Errorf("read the memberships of the caller: %w", err)
	}
	if workspaceID != "" {
		if seen.workspace, seen.workspaceFound, err = s.deps.Workspaces.Find(ctx, productID, workspaceID); err != nil {
			return scene{}, fmt.Errorf("read the workspace: %w", err)
		}
	}
	if workspaceID != "" && membershipID != "" {
		if seen.membership, seen.membershipFound, err = s.deps.Memberships.Find(ctx, productID, workspaceID, membershipID); err != nil {
			return scene{}, fmt.Errorf("read the membership: %w", err)
		}
	}
	return seen, nil
}

// surveyAssignment is survey for a request that names a role assignment of
// the product. As there, an empty identifier names nothing and is not looked
// up, and all of it is read whatever the answer will be.
func (s *ProductGovernance) surveyAssignment(ctx context.Context, caller domain.Principal,
	productID domain.ProductID, assignmentID domain.RoleAssignmentID) (scene, error) {
	seen, err := s.survey(ctx, caller, productID, "", "")
	if err != nil {
		return scene{}, err
	}
	if assignmentID != "" {
		if seen.assignment, seen.assignmentFound, err = s.deps.Roles.Find(ctx, productID, assignmentID); err != nil {
			return scene{}, fmt.Errorf("read the role assignment: %w", err)
		}
	}
	return seen, nil
}

// knows reports whether the caller may know that the product exists: it
// holds something in it, or it administers the platform. A platform
// administrator knows that much and nothing of what is inside the product.
func (s *ProductGovernance) knows(standing domain.Standing, caller domain.Principal) bool {
	return !standing.Stranger() || s.deps.Administrators.Includes(caller)
}

// notOwner is the refusal of a caller that asked for what only an owner of
// the product may do. Whoever may know the product exists is told it may
// not; to anyone else there is no such product. The answer depends on the
// product alone, so it says nothing about what is inside it.
func (s *ProductGovernance) notOwner(standing domain.Standing, entry ports.AuditEntry) *refusal {
	refused := &refusal{ErrProductNotFound, entry}
	if s.knows(standing, entry.Actor) {
		refused.err = ErrForbidden
	}
	return refused
}

// RegisterProduct is what the registration of a product needs.
type RegisterProduct struct {
	Caller        domain.Principal
	CorrelationID string
	Key           string
	DisplayName   string
	Owner         domain.OrganizationRef
	// Owners are the principals that own the product from the start.
	Owners []domain.Principal
}

// RegisterProduct registers a product and makes its first owners (FR-001,
// AC-08). Only a platform administrator may.
//
// The product, the role of its owners, the audit fact and the event commit
// together or not at all (INV-13).
func (s *ProductGovernance) RegisterProduct(ctx context.Context, command RegisterProduct) (domain.Product, error) {
	if err := known(command.Caller); err != nil {
		return domain.Product{}, err
	}
	if !s.deps.Administrators.Includes(command.Caller) {
		return domain.Product{}, s.refuse(ctx, refusal{ErrForbidden, ports.AuditEntry{
			Actor: command.Caller, Action: ActionProductRegister, Rule: ruleNoRole,
			ResourceType: resourceProduct, CorrelationID: command.CorrelationID,
		}})
	}

	id, err := domain.ParseProductID(s.deps.Identifiers.NewID())
	if err != nil {
		return domain.Product{}, fmt.Errorf("generate the identifier of the product: %w", err)
	}
	product, err := domain.NewProduct(id, command.Key, command.DisplayName, command.Owner, command.Caller, s.deps.Clock.Now())
	if err != nil {
		return domain.Product{}, err
	}
	// Each first owner holds its role under an identifier of its own, as a
	// role assignment made later does.
	assignmentIDs := make([]domain.RoleAssignmentID, len(command.Owners))
	for index := range assignmentIDs {
		if assignmentIDs[index], err = domain.ParseRoleAssignmentID(s.deps.Identifiers.NewID()); err != nil {
			return domain.Product{}, fmt.Errorf("generate the identifier of a role assignment: %w", err)
		}
	}
	owners, err := product.AssignFirstOwners(assignmentIDs, command.Owners, command.Caller)
	if err != nil {
		return domain.Product{}, err
	}
	for _, owner := range owners {
		if !s.deps.Issuers.Trusts(owner.Principal.Issuer) {
			return domain.Product{}, ErrOwnerIssuerNotTrusted
		}
	}

	err = s.deps.Transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		if err := s.deps.Products.Add(ctx, product); err != nil {
			return err
		}
		for _, owner := range owners {
			if err := s.deps.Roles.Add(ctx, owner); err != nil {
				return err
			}
		}
		if err := s.deps.Audit.Record(ctx, ports.AuditEntry{
			Actor: command.Caller, Action: ActionProductRegister, Allowed: true, Rule: rulePlatformAdministrator,
			ProductID: product.ID, ResourceType: resourceProduct, ResourceID: string(product.ID),
			CorrelationID: command.CorrelationID,
		}); err != nil {
			return err
		}
		return s.deps.Events.ProductRegistered(ctx, product, command.CorrelationID)
	})
	if err != nil {
		return domain.Product{}, fmt.Errorf("register the product: %w", err)
	}
	return product, nil
}

// GetProduct asks for one product.
type GetProduct struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
}

// GetProduct returns a product to a platform administrator and to whoever
// holds something in it: a role in the product or a membership in force of
// one of its workspaces. To anyone else the product does not exist.
func (s *ProductGovernance) GetProduct(ctx context.Context, query GetProduct) (domain.Product, error) {
	if err := known(query.Caller); err != nil {
		return domain.Product{}, err
	}
	id, err := domain.ParseProductID(query.ProductID)
	if err != nil {
		return domain.Product{}, ErrProductNotFound
	}
	seen, err := s.survey(ctx, query.Caller, id, "", "")
	if err != nil {
		return domain.Product{}, err
	}

	if !seen.productFound {
		return domain.Product{}, ErrProductNotFound
	}
	if !s.knows(seen.standing, query.Caller) {
		return domain.Product{}, s.refuse(ctx, refusal{ErrProductNotFound, ports.AuditEntry{
			Actor: query.Caller, Action: ActionProductRead, Rule: ruleNoRole,
			ProductID: id, ResourceType: resourceProduct, ResourceID: string(id),
			CorrelationID: query.CorrelationID,
		}})
	}
	return seen.product, nil
}

// CreateWorkspace is what the creation of a workspace needs.
type CreateWorkspace struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	Key           string
	DisplayName   string
}

// CreateWorkspace gives a product a workspace (FR-002, AC-09). Only an owner
// of that product may. An owner of another product is a stranger to this
// one: to a stranger the product does not exist. A member of one of its
// workspaces knows the product and may not give it another.
//
// The decision is made inside the transaction of the write, from what the
// caller holds then (F07 §5.2). The workspace, the audit fact and the event
// commit together or not at all (INV-13).
func (s *ProductGovernance) CreateWorkspace(ctx context.Context, command CreateWorkspace) (domain.Workspace, error) {
	if err := known(command.Caller); err != nil {
		return domain.Workspace{}, err
	}
	productID, err := domain.ParseProductID(command.ProductID)
	if err != nil {
		return domain.Workspace{}, ErrProductNotFound
	}

	var workspace domain.Workspace
	var refused *refusal
	err = s.deps.Transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		seen, err := s.survey(ctx, command.Caller, productID, "", "")
		if err != nil {
			return err
		}
		if !seen.productFound {
			return ErrProductNotFound
		}
		if !seen.standing.Owns() {
			refused = s.notOwner(seen.standing, ports.AuditEntry{
				Actor: command.Caller, Action: ActionWorkspaceManage, Rule: ruleNoRole,
				ProductID: productID, ResourceType: resourceWorkspace, CorrelationID: command.CorrelationID,
			})
			return refused.err
		}

		id, err := domain.ParseWorkspaceID(s.deps.Identifiers.NewID())
		if err != nil {
			return fmt.Errorf("generate the identifier of the workspace: %w", err)
		}
		workspace, err = domain.NewWorkspace(id, seen.product, command.Key, command.DisplayName, command.Caller, s.deps.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.deps.Workspaces.Add(ctx, workspace); err != nil {
			return err
		}
		if err := s.deps.Audit.Record(ctx, ports.AuditEntry{
			Actor: command.Caller, Action: ActionWorkspaceManage, Allowed: true, Rule: ruleProductOwner,
			ProductID: productID, ResourceType: resourceWorkspace, ResourceID: string(workspace.ID),
			CorrelationID: command.CorrelationID,
		}); err != nil {
			return err
		}
		return s.deps.Events.WorkspaceCreated(ctx, workspace, command.CorrelationID)
	})
	if refused != nil {
		return domain.Workspace{}, s.refuse(ctx, *refused)
	}
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("create the workspace: %w", err)
	}
	return workspace, nil
}

// GetWorkspace asks for one workspace of one product.
type GetWorkspace struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	WorkspaceID   string
}

// GetWorkspace returns a workspace to an owner of its product and to a
// member of the workspace itself. Being a member of one workspace of a
// product gives no view of another (F03 §2), and neither does registering
// the product: a platform administrator knows the product exists and nothing
// of what is inside it.
//
// Whoever may not read the workspace is answered as far as it may know: no
// such product to a stranger, no such workspace to whoever knows the
// product.
func (s *ProductGovernance) GetWorkspace(ctx context.Context, query GetWorkspace) (domain.Workspace, error) {
	if err := known(query.Caller); err != nil {
		return domain.Workspace{}, err
	}
	productID, err := domain.ParseProductID(query.ProductID)
	if err != nil {
		return domain.Workspace{}, ErrProductNotFound
	}
	// An identifier that is none names nothing. The product is read all the
	// same: whether the caller is told that the product or that the
	// workspace is missing depends on it.
	workspaceID, _ := domain.ParseWorkspaceID(query.WorkspaceID)

	seen, err := s.survey(ctx, query.Caller, productID, workspaceID, "")
	if err != nil {
		return domain.Workspace{}, err
	}

	if !seen.productFound {
		return domain.Workspace{}, ErrProductNotFound
	}
	if !seen.standing.Owns() && !seen.standing.MemberOf(workspaceID) {
		refused := refusal{ErrWorkspaceNotFound, ports.AuditEntry{
			Actor: query.Caller, Action: ActionWorkspaceRead, Rule: ruleNoRole,
			ProductID: productID, ResourceType: resourceWorkspace, CorrelationID: query.CorrelationID,
		}}
		if seen.workspaceFound {
			refused.entry.ResourceID = string(workspaceID)
		}
		if !s.knows(seen.standing, query.Caller) {
			refused.err = ErrProductNotFound
		}
		return domain.Workspace{}, s.refuse(ctx, refused)
	}
	if !seen.workspaceFound {
		return domain.Workspace{}, ErrWorkspaceNotFound
	}
	return seen.workspace, nil
}

// GrantMembership is what making a principal a member of a workspace needs.
type GrantMembership struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	WorkspaceID   string
	// Member is the principal that becomes a member.
	Member domain.Principal
	// Role is the role the member holds in the workspace.
	Role string
}

// GrantMembership makes a principal a member of one workspace of a product,
// in one role (FR-003, AC-10). Only an owner of that product may. Whoever
// may know the product exists and does not own it is told it may not; to
// anyone else there is no such product. Neither answer depends on the
// workspace.
//
// The decision is made inside the transaction of the write, from what the
// caller holds then. The membership, the audit fact and the event commit
// together or not at all (INV-13).
func (s *ProductGovernance) GrantMembership(ctx context.Context, command GrantMembership) (domain.Membership, error) {
	if err := known(command.Caller); err != nil {
		return domain.Membership{}, err
	}
	productID, err := domain.ParseProductID(command.ProductID)
	if err != nil {
		return domain.Membership{}, ErrProductNotFound
	}
	workspaceID, _ := domain.ParseWorkspaceID(command.WorkspaceID)

	var membership domain.Membership
	var refused *refusal
	err = s.deps.Transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		seen, err := s.survey(ctx, command.Caller, productID, workspaceID, "")
		if err != nil {
			return err
		}
		if !seen.productFound {
			return ErrProductNotFound
		}
		if !seen.standing.Owns() {
			refused = s.notOwner(seen.standing, ports.AuditEntry{
				Actor: command.Caller, Action: ActionMemberGrant, Rule: ruleNoRole,
				ProductID: productID, ResourceType: resourceMember, CorrelationID: command.CorrelationID,
			})
			return refused.err
		}
		if !seen.workspaceFound {
			return ErrWorkspaceNotFound
		}

		id, err := domain.ParseMembershipID(s.deps.Identifiers.NewID())
		if err != nil {
			return fmt.Errorf("generate the identifier of the membership: %w", err)
		}
		membership, err = domain.GrantMembership(id, seen.product, seen.workspace,
			command.Member, domain.Role(command.Role), command.Caller, s.deps.Clock.Now())
		if err != nil {
			return err
		}
		// A membership of a principal that can never authenticate would
		// never be used.
		if !s.deps.Issuers.Trusts(command.Member.Issuer) {
			return ErrMemberIssuerNotTrusted
		}
		if err := s.deps.Memberships.Add(ctx, membership); err != nil {
			return err
		}
		if err := s.deps.Audit.Record(ctx, ports.AuditEntry{
			Actor: command.Caller, Action: ActionMemberGrant, Allowed: true, Rule: ruleMembersOfOwnProduct,
			ProductID: productID, ResourceType: resourceMember, ResourceID: string(membership.ID),
			CorrelationID: command.CorrelationID,
		}); err != nil {
			return err
		}
		return s.deps.Events.MembershipChanged(ctx, membership, command.CorrelationID)
	})
	if refused != nil {
		return domain.Membership{}, s.refuse(ctx, *refused)
	}
	if err != nil {
		return domain.Membership{}, fmt.Errorf("grant the membership: %w", err)
	}
	return membership, nil
}

// GetMembership asks for one membership of one workspace.
type GetMembership struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	WorkspaceID   string
	MembershipID  string
}

// GetMembership returns a membership in force to an owner of its product.
// The members of a workspace are the owner's to manage and nobody else's to
// read, not even the member's own.
//
// Whoever does not own the product is answered as far as it may know: no
// such product to a stranger, no such workspace to whoever may not read the
// workspace, no such membership to a member of it.
func (s *ProductGovernance) GetMembership(ctx context.Context, query GetMembership) (domain.Membership, error) {
	if err := known(query.Caller); err != nil {
		return domain.Membership{}, err
	}
	productID, err := domain.ParseProductID(query.ProductID)
	if err != nil {
		return domain.Membership{}, ErrProductNotFound
	}
	workspaceID, _ := domain.ParseWorkspaceID(query.WorkspaceID)
	membershipID, _ := domain.ParseMembershipID(query.MembershipID)

	seen, err := s.survey(ctx, query.Caller, productID, workspaceID, membershipID)
	if err != nil {
		return domain.Membership{}, err
	}

	if !seen.productFound {
		return domain.Membership{}, ErrProductNotFound
	}
	if !seen.standing.Owns() {
		refused := refusal{ErrMembershipNotFound, ports.AuditEntry{
			Actor: query.Caller, Action: ActionMemberRead, Rule: ruleNoRole,
			ProductID: productID, ResourceType: resourceMember, CorrelationID: query.CorrelationID,
		}}
		if seen.membershipFound {
			refused.entry.ResourceID = string(membershipID)
		}
		switch {
		case !s.knows(seen.standing, query.Caller):
			refused.err = ErrProductNotFound
		case !seen.standing.MemberOf(workspaceID):
			refused.err = ErrWorkspaceNotFound
		}
		return domain.Membership{}, s.refuse(ctx, refused)
	}
	if !seen.workspaceFound {
		return domain.Membership{}, ErrWorkspaceNotFound
	}
	if !seen.membershipFound {
		return domain.Membership{}, ErrMembershipNotFound
	}
	return seen.membership, nil
}

// RevokeMembership names the membership to end.
type RevokeMembership struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	WorkspaceID   string
	MembershipID  string
}

// RevokeMembership ends a membership (FR-003). Only an owner of the product
// may, and whoever does not own it is answered as for a grant. A suspended
// product takes no new member; its members can still be removed.
//
// From the moment the revocation commits the membership grants nothing:
// every decision reads what a caller holds when it is made. A membership
// that is not in force is not found, whoever ended it, so a revocation that
// is sent twice finds nothing to do the second time.
//
// The decision is made inside the transaction of the write. The revocation,
// the audit fact and the event commit together or not at all (INV-13).
func (s *ProductGovernance) RevokeMembership(ctx context.Context, command RevokeMembership) error {
	if err := known(command.Caller); err != nil {
		return err
	}
	productID, err := domain.ParseProductID(command.ProductID)
	if err != nil {
		return ErrProductNotFound
	}
	workspaceID, _ := domain.ParseWorkspaceID(command.WorkspaceID)
	membershipID, _ := domain.ParseMembershipID(command.MembershipID)

	var refused *refusal
	err = s.deps.Transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		seen, err := s.survey(ctx, command.Caller, productID, workspaceID, membershipID)
		if err != nil {
			return err
		}
		if !seen.productFound {
			return ErrProductNotFound
		}
		if !seen.standing.Owns() {
			entry := ports.AuditEntry{
				Actor: command.Caller, Action: ActionMemberRevoke, Rule: ruleNoRole,
				ProductID: productID, ResourceType: resourceMember, CorrelationID: command.CorrelationID,
			}
			if seen.membershipFound {
				entry.ResourceID = string(membershipID)
			}
			refused = s.notOwner(seen.standing, entry)
			return refused.err
		}
		if !seen.workspaceFound {
			return ErrWorkspaceNotFound
		}
		if !seen.membershipFound {
			return ErrMembershipNotFound
		}

		revoked, err := seen.membership.Revoke(command.Caller, s.deps.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.deps.Memberships.Revoke(ctx, revoked); err != nil {
			if errors.Is(err, ports.ErrMembershipGone) {
				// Somebody else ended it between the read and the write.
				// It is not in force, which is what was asked for, and the
				// revocation that did it has its own record.
				return ErrMembershipNotFound
			}
			return err
		}
		if err := s.deps.Audit.Record(ctx, ports.AuditEntry{
			Actor: command.Caller, Action: ActionMemberRevoke, Allowed: true, Rule: ruleMembersOfOwnProduct,
			ProductID: productID, ResourceType: resourceMember, ResourceID: string(revoked.ID),
			CorrelationID: command.CorrelationID,
		}); err != nil {
			return err
		}
		return s.deps.Events.MembershipChanged(ctx, revoked, command.CorrelationID)
	})
	if refused != nil {
		return s.refuse(ctx, *refused)
	}
	if err != nil {
		return fmt.Errorf("revoke the membership: %w", err)
	}
	return nil
}

// GrantProductRole is what making a principal hold a role in a product
// needs.
type GrantProductRole struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	// Principal is who gets the role.
	Principal domain.Principal
	// Role is the role the principal holds in the product.
	Role string
}

// GrantProductRole makes a principal hold a role in a product: the role of
// an owner, the only one a product has (FR-003). An owner of that product
// may (AC-10), and so may a platform administrator (AC-08): naming the
// owners of a product is part of registering it, and it is the only way to
// repair a product whose owner was mistyped. It gives the administrator
// nothing inside the product. Whoever else may know the product exists is
// told it may not; to anyone else there is no such product.
//
// The decision is made inside the transaction of the write, from what the
// caller holds then. The assignment, the audit fact and the event commit
// together or not at all (INV-13).
func (s *ProductGovernance) GrantProductRole(ctx context.Context, command GrantProductRole) (domain.RoleAssignment, error) {
	if err := known(command.Caller); err != nil {
		return domain.RoleAssignment{}, err
	}
	productID, err := domain.ParseProductID(command.ProductID)
	if err != nil {
		return domain.RoleAssignment{}, ErrProductNotFound
	}

	var assignment domain.RoleAssignment
	var refused *refusal
	err = s.deps.Transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		seen, err := s.survey(ctx, command.Caller, productID, "", "")
		if err != nil {
			return err
		}
		if !seen.productFound {
			return ErrProductNotFound
		}
		// An owner acts as one, whatever else it is: an administrator that
		// owns the product is recorded under the rule of the owners.
		rule := ruleMembersOfOwnProduct
		if !seen.standing.Owns() {
			if !s.deps.Administrators.Includes(command.Caller) {
				refused = s.notOwner(seen.standing, ports.AuditEntry{
					Actor: command.Caller, Action: ActionProductRoleGrant, Rule: ruleNoRole,
					ProductID: productID, ResourceType: resourceRoleAssignment, CorrelationID: command.CorrelationID,
				})
				return refused.err
			}
			rule = rulePlatformAdministrator
		}

		id, err := domain.ParseRoleAssignmentID(s.deps.Identifiers.NewID())
		if err != nil {
			return fmt.Errorf("generate the identifier of the role assignment: %w", err)
		}
		assignment, err = domain.GrantProductRole(id, seen.product,
			command.Principal, domain.Role(command.Role), command.Caller, s.deps.Clock.Now())
		if err != nil {
			return err
		}
		// A role of a principal that can never authenticate would never be
		// used.
		if !s.deps.Issuers.Trusts(command.Principal.Issuer) {
			return ErrOwnerIssuerNotTrusted
		}
		if err := s.deps.Roles.Add(ctx, assignment); err != nil {
			return err
		}
		if err := s.deps.Audit.Record(ctx, ports.AuditEntry{
			Actor: command.Caller, Action: ActionProductRoleGrant, Allowed: true, Rule: rule,
			ProductID: productID, ResourceType: resourceRoleAssignment, ResourceID: string(assignment.ID),
			CorrelationID: command.CorrelationID,
		}); err != nil {
			return err
		}
		return s.deps.Events.ProductRoleChanged(ctx, assignment, command.CorrelationID)
	})
	if refused != nil {
		return domain.RoleAssignment{}, s.refuse(ctx, *refused)
	}
	if err != nil {
		return domain.RoleAssignment{}, fmt.Errorf("grant the role: %w", err)
	}
	return assignment, nil
}

// GetProductRole asks for one role assignment of one product.
type GetProductRole struct {
	Caller           domain.Principal
	CorrelationID    string
	ProductID        string
	RoleAssignmentID string
}

// GetProductRole returns a role assignment in force to an owner of its
// product. Who owns a product is the owners' to manage and nobody else's to
// read: a platform administrator names an owner and reads none.
//
// Whoever does not own the product is answered as far as it may know: no
// such product to a stranger, no such role assignment to whoever knows the
// product.
func (s *ProductGovernance) GetProductRole(ctx context.Context, query GetProductRole) (domain.RoleAssignment, error) {
	if err := known(query.Caller); err != nil {
		return domain.RoleAssignment{}, err
	}
	productID, err := domain.ParseProductID(query.ProductID)
	if err != nil {
		return domain.RoleAssignment{}, ErrProductNotFound
	}
	// An identifier that is none names nothing. The rest is read all the
	// same: what the caller is told depends on it.
	assignmentID, _ := domain.ParseRoleAssignmentID(query.RoleAssignmentID)

	seen, err := s.surveyAssignment(ctx, query.Caller, productID, assignmentID)
	if err != nil {
		return domain.RoleAssignment{}, err
	}

	if !seen.productFound {
		return domain.RoleAssignment{}, ErrProductNotFound
	}
	if !seen.standing.Owns() {
		refused := refusal{ErrRoleAssignmentNotFound, ports.AuditEntry{
			Actor: query.Caller, Action: ActionProductRoleRead, Rule: ruleNoRole,
			ProductID: productID, ResourceType: resourceRoleAssignment, CorrelationID: query.CorrelationID,
		}}
		if seen.assignmentFound {
			refused.entry.ResourceID = string(assignmentID)
		}
		if !s.knows(seen.standing, query.Caller) {
			refused.err = ErrProductNotFound
		}
		return domain.RoleAssignment{}, s.refuse(ctx, refused)
	}
	if !seen.assignmentFound {
		return domain.RoleAssignment{}, ErrRoleAssignmentNotFound
	}
	return seen.assignment, nil
}

// RevokeProductRole names the role assignment to end.
type RevokeProductRole struct {
	Caller           domain.Principal
	CorrelationID    string
	ProductID        string
	RoleAssignmentID string
}

// RevokeProductRole ends a role assignment (FR-003). Only an owner of the
// product may, its own assignment included: a platform administrator names
// owners and removes none. Whoever does not own the product and may know it
// exists is told it may not; to anyone else there is no such product. A
// suspended product takes no new owner; its owners can still be removed.
//
// A product always has an owner: the last assignment in force that makes
// somebody one is not revoked (domain.ErrLastOwner). The owners in force are
// locked before anything is read. What the caller holds, and how many owners
// there are, is then read where no other revocation can end one of them: of
// two owners who revoke each other at once, the second finds that it owns
// the product no more.
//
// From the moment the revocation commits the assignment grants nothing:
// every decision reads what a caller holds when it is made. An assignment
// that is not in force is not found, whoever ended it, so a revocation that
// is sent twice finds nothing to do the second time.
//
// The decision is made inside the transaction of the write. The revocation,
// the audit fact and the event commit together or not at all (INV-13).
func (s *ProductGovernance) RevokeProductRole(ctx context.Context, command RevokeProductRole) error {
	if err := known(command.Caller); err != nil {
		return err
	}
	productID, err := domain.ParseProductID(command.ProductID)
	if err != nil {
		return ErrProductNotFound
	}
	assignmentID, _ := domain.ParseRoleAssignmentID(command.RoleAssignmentID)

	var refused *refusal
	err = s.deps.Transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		owners, err := s.deps.Roles.LockOwners(ctx, productID)
		if err != nil {
			return fmt.Errorf("lock the owners of the product: %w", err)
		}
		seen, err := s.surveyAssignment(ctx, command.Caller, productID, assignmentID)
		if err != nil {
			return err
		}
		if !seen.productFound {
			return ErrProductNotFound
		}
		if !seen.standing.Owns() {
			entry := ports.AuditEntry{
				Actor: command.Caller, Action: ActionProductRoleRevoke, Rule: ruleNoRole,
				ProductID: productID, ResourceType: resourceRoleAssignment, CorrelationID: command.CorrelationID,
			}
			if seen.assignmentFound {
				entry.ResourceID = string(assignmentID)
			}
			refused = s.notOwner(seen.standing, entry)
			return refused.err
		}
		if !seen.assignmentFound {
			return ErrRoleAssignmentNotFound
		}

		revoked, err := seen.assignment.Revoke(command.Caller, s.deps.Clock.Now(), owners)
		if err != nil {
			return err
		}
		if err := s.deps.Roles.Revoke(ctx, revoked); err != nil {
			if errors.Is(err, ports.ErrRoleAssignmentGone) {
				// It was ended between the read and the write, by something
				// that did not lock the owners first. It is not in force,
				// which is what was asked for, and this revocation leaves
				// no record of its own.
				return ErrRoleAssignmentNotFound
			}
			return err
		}
		if err := s.deps.Audit.Record(ctx, ports.AuditEntry{
			Actor: command.Caller, Action: ActionProductRoleRevoke, Allowed: true, Rule: ruleMembersOfOwnProduct,
			ProductID: productID, ResourceType: resourceRoleAssignment, ResourceID: string(revoked.ID),
			CorrelationID: command.CorrelationID,
		}); err != nil {
			return err
		}
		return s.deps.Events.ProductRoleChanged(ctx, revoked, command.CorrelationID)
	})
	if refused != nil {
		return s.refuse(ctx, *refused)
	}
	if err != nil {
		return fmt.Errorf("revoke the role: %w", err)
	}
	return nil
}
