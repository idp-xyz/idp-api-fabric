package application

import (
	"context"
	"errors"
	"fmt"
	"slices"

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

	// ErrOwnerIssuerNotTrusted reports an owner named under an identity
	// provider whose principals cannot authenticate here.
	ErrOwnerIssuerNotTrusted = errors.New("an owner is named under an issuer this deployment does not trust")

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
)

// The rules a decision is recorded under, as the access matrix of F05 names
// them. A read that is allowed is not recorded, so the two read actions need
// no permit rule here.
const (
	// rulePlatformAdministrator permits product.register (AC-08).
	rulePlatformAdministrator = "AC-08"
	// ruleProductOwner permits workspace.manage (AC-09).
	ruleProductOwner = "AC-09"
	// ruleNoRole refuses whoever holds no role that permits the action
	// (DENY-03, missing_active_membership_or_grant).
	ruleNoRole = "DENY-03"
)

const (
	resourceProduct   = "Product"
	resourceWorkspace = "Workspace"
)

// Dependencies are the ports the use cases are wired with. All are required.
type Dependencies struct {
	Transactor     ports.Transactor
	Products       ports.Products
	Workspaces     ports.Workspaces
	Roles          ports.RoleAssignments
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
	owners, err := product.AssignFirstOwners(command.Owners, command.Caller)
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
		if err := s.deps.Roles.Add(ctx, owners); err != nil {
			return err
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
// holds a role in it. To anyone else the product does not exist.
func (s *ProductGovernance) GetProduct(ctx context.Context, query GetProduct) (domain.Product, error) {
	if err := known(query.Caller); err != nil {
		return domain.Product{}, err
	}
	id, err := domain.ParseProductID(query.ProductID)
	if err != nil {
		return domain.Product{}, ErrProductNotFound
	}
	// Both are read whether or not the product exists, so that a product
	// that is withheld costs what a product that is missing costs.
	product, found, err := s.deps.Products.Find(ctx, id)
	if err != nil {
		return domain.Product{}, fmt.Errorf("read the product: %w", err)
	}
	roles, err := s.deps.Roles.RolesOf(ctx, id, query.Caller)
	if err != nil {
		return domain.Product{}, fmt.Errorf("read the roles of the caller: %w", err)
	}

	if !found {
		return domain.Product{}, ErrProductNotFound
	}
	if len(roles) == 0 && !s.deps.Administrators.Includes(query.Caller) {
		return domain.Product{}, s.refuse(ctx, refusal{ErrProductNotFound, ports.AuditEntry{
			Actor: query.Caller, Action: ActionProductRead, Rule: ruleNoRole,
			ProductID: id, ResourceType: resourceProduct, ResourceID: string(id),
			CorrelationID: query.CorrelationID,
		}})
	}
	return product, nil
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
// one: to a stranger the product does not exist.
//
// The decision is made inside the transaction of the write, from the roles
// as they are then (F07 §5.2). The workspace, the audit fact and the event
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
		product, found, err := s.deps.Products.Find(ctx, productID)
		if err != nil {
			return fmt.Errorf("read the product: %w", err)
		}
		roles, err := s.deps.Roles.RolesOf(ctx, productID, command.Caller)
		if err != nil {
			return fmt.Errorf("read the roles of the caller: %w", err)
		}
		if !found {
			return ErrProductNotFound
		}
		if !slices.Contains(roles, domain.RoleProductOwner) {
			// Whoever may know the product exists is told it may not do
			// this. To anyone else there is no such product.
			refused = &refusal{ErrProductNotFound, ports.AuditEntry{
				Actor: command.Caller, Action: ActionWorkspaceManage, Rule: ruleNoRole,
				ProductID: productID, ResourceType: resourceWorkspace, CorrelationID: command.CorrelationID,
			}}
			if len(roles) > 0 || s.deps.Administrators.Includes(command.Caller) {
				refused.err = ErrForbidden
			}
			return refused.err
		}

		id, err := domain.ParseWorkspaceID(s.deps.Identifiers.NewID())
		if err != nil {
			return fmt.Errorf("generate the identifier of the workspace: %w", err)
		}
		workspace, err = domain.NewWorkspace(id, product, command.Key, command.DisplayName, command.Caller, s.deps.Clock.Now())
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

// GetWorkspace returns a workspace to whoever holds a role in its product. A
// platform administrator knows the product exists and nothing of what is
// inside it: registering a product gives no view of its workspaces.
func (s *ProductGovernance) GetWorkspace(ctx context.Context, query GetWorkspace) (domain.Workspace, error) {
	if err := known(query.Caller); err != nil {
		return domain.Workspace{}, err
	}
	productID, err := domain.ParseProductID(query.ProductID)
	if err != nil {
		return domain.Workspace{}, ErrProductNotFound
	}
	workspaceID, err := domain.ParseWorkspaceID(query.WorkspaceID)
	if err != nil {
		// Read the product all the same: whether the caller is told that the
		// product or that the workspace is missing depends on it.
		workspaceID = ""
	}

	_, productFound, err := s.deps.Products.Find(ctx, productID)
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("read the product: %w", err)
	}
	roles, err := s.deps.Roles.RolesOf(ctx, productID, query.Caller)
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("read the roles of the caller: %w", err)
	}
	var workspace domain.Workspace
	var workspaceFound bool
	if workspaceID != "" {
		if workspace, workspaceFound, err = s.deps.Workspaces.Find(ctx, productID, workspaceID); err != nil {
			return domain.Workspace{}, fmt.Errorf("read the workspace: %w", err)
		}
	}

	if !productFound {
		return domain.Workspace{}, ErrProductNotFound
	}
	if len(roles) == 0 {
		refused := refusal{ErrProductNotFound, ports.AuditEntry{
			Actor: query.Caller, Action: ActionWorkspaceRead, Rule: ruleNoRole,
			ProductID: productID, ResourceType: resourceWorkspace, CorrelationID: query.CorrelationID,
		}}
		if workspaceFound {
			refused.entry.ResourceID = string(workspaceID)
		}
		if s.deps.Administrators.Includes(query.Caller) {
			refused.err = ErrWorkspaceNotFound
		}
		return domain.Workspace{}, s.refuse(ctx, refused)
	}
	if !workspaceFound {
		return domain.Workspace{}, ErrWorkspaceNotFound
	}
	return workspace, nil
}
