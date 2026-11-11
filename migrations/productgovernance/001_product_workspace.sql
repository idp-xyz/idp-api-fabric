-- Product Governance owns the products, their workspaces and who holds which
-- role in a product (F03 §3.1; INV-01, INV-02).
--
-- A product is the boundary of isolation of the control plane. A workspace
-- and a role assignment carry the product they belong to, and no statement
-- reads one without naming that product.
--
-- A process inserts into these tables and reads them. It has no statement
-- that changes or removes a row yet, so the application role holds neither
-- UPDATE nor DELETE on them.

CREATE TABLE productgovernance.product (
    product_id              uuid        NOT NULL,
    -- The name people and configuration use for the product, unique among
    -- all products. It is not its identity: product_id is.
    product_key             text        NOT NULL,
    display_name            text        NOT NULL,
    -- A suspended product takes no new work; what it holds is kept (INV-01).
    status                  text        NOT NULL,
    -- The organization the product belongs to, as a reference into the
    -- identity or organization system that knows it. Nothing derives a
    -- permission from it.
    owner_identity_provider text        NOT NULL,
    owner_organization      text        NOT NULL,
    -- Counts the changes of the row, for optimistic concurrency and for the
    -- order of its events. It is not the version of any API (F03 §4.1).
    aggregate_version       bigint      NOT NULL,
    created_at              timestamptz NOT NULL,
    created_by_issuer       text        NOT NULL,
    created_by_subject      text        NOT NULL,
    updated_at              timestamptz NOT NULL,

    CONSTRAINT product_pkey PRIMARY KEY (product_id),
    CONSTRAINT product_key_unique UNIQUE (product_key),
    CONSTRAINT product_status_known CHECK (status IN ('ACTIVE', 'SUSPENDED')),
    CONSTRAINT product_version_positive CHECK (aggregate_version >= 1)
);

CREATE TABLE productgovernance.workspace (
    workspace_id       uuid        NOT NULL,
    -- A workspace belongs to exactly one product, for good (INV-02).
    product_id         uuid        NOT NULL,
    -- Unique among the workspaces of the product.
    workspace_key      text        NOT NULL,
    display_name       text        NOT NULL,
    status             text        NOT NULL,
    aggregate_version  bigint      NOT NULL,
    created_at         timestamptz NOT NULL,
    created_by_issuer  text        NOT NULL,
    created_by_subject text        NOT NULL,
    updated_at         timestamptz NOT NULL,

    CONSTRAINT workspace_pkey PRIMARY KEY (workspace_id),
    CONSTRAINT workspace_product_fkey
        FOREIGN KEY (product_id) REFERENCES productgovernance.product (product_id),
    CONSTRAINT workspace_key_unique UNIQUE (product_id, workspace_key),
    CONSTRAINT workspace_status_known CHECK (status IN ('ACTIVE')),
    CONSTRAINT workspace_version_positive CHECK (aggregate_version >= 1)
);

-- Who holds which role in which product. Authorization is decided from these
-- rows as they are when a request arrives, never from a claim of a token
-- (F05 §4.2). A row names the principal it grants to, as its issuer names it,
-- and the principal that granted it (FR-003).
CREATE TABLE productgovernance.product_role_assignment (
    product_id         uuid        NOT NULL,
    principal_issuer   text        NOT NULL,
    principal_subject  text        NOT NULL,
    role               text        NOT NULL,
    granted_at         timestamptz NOT NULL,
    granted_by_issuer  text        NOT NULL,
    granted_by_subject text        NOT NULL,

    CONSTRAINT product_role_assignment_pkey
        PRIMARY KEY (product_id, principal_issuer, principal_subject, role),
    CONSTRAINT product_role_assignment_product_fkey
        FOREIGN KEY (product_id) REFERENCES productgovernance.product (product_id),
    CONSTRAINT product_role_assignment_role_known CHECK (role IN ('product_owner'))
);
