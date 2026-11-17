-- The Catalog owns the API asset. Product and Workspace are references
-- checked through Product Governance during registration (F03 §3.2).
CREATE TABLE apicatalog.api_asset (
    api_asset_id     uuid        PRIMARY KEY,
    product_id       uuid        NOT NULL,
    workspace_id     uuid        NOT NULL,
    asset_key        text        NOT NULL,
    display_name     text        NOT NULL,
    domain_key       text        NOT NULL,
    api_kind         text        NOT NULL CHECK (api_kind IN ('HTTP', 'EVENT')),
    lifecycle        text        NOT NULL CHECK (lifecycle IN ('EXPERIMENTAL', 'STABLE', 'DEPRECATED', 'RETIRED')),
    aggregate_version bigint     NOT NULL CHECK (aggregate_version >= 1),
    created_at       timestamptz NOT NULL,
    created_by_issuer text       NOT NULL,
    created_by_subject text      NOT NULL,
    CONSTRAINT api_asset_product_key_unique UNIQUE (product_id, asset_key),
    CONSTRAINT api_asset_key_format CHECK (asset_key ~ '^[a-z][a-z0-9-]{0,61}[a-z0-9]$'),
    CONSTRAINT api_asset_domain_format CHECK (domain_key ~ '^[a-z][a-z0-9-]{0,61}[a-z0-9]$'),
    CONSTRAINT api_asset_display_name_size CHECK (char_length(display_name) BETWEEN 1 AND 120)
);

CREATE INDEX api_asset_product_order
    ON apicatalog.api_asset (product_id, created_at, api_asset_id);

CREATE INDEX api_asset_workspace_order
    ON apicatalog.api_asset (product_id, workspace_id, created_at, api_asset_id);
