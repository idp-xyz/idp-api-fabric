-- Source evidence and revisions are append-only for the application role.
-- Catalog and Workspace IDs are references checked at the use-case boundary.
CREATE TABLE contractregistry.source_snapshot (
    source_snapshot_id  uuid PRIMARY KEY,
    product_id          uuid NOT NULL,
    api_asset_id        uuid NOT NULL,
    source_kind         text NOT NULL CHECK (source_kind = 'UPLOAD'),
    entrypoint          text NOT NULL CHECK (char_length(entrypoint) BETWEEN 1 AND 200),
    source_digest       text NOT NULL CHECK (source_digest ~ '^sha256:[0-9a-f]{64}$'),
    ref_closure_digest  text NOT NULL CHECK (ref_closure_digest ~ '^sha256:[0-9a-f]{64}$'),
    ref_closure_manifest bytea NOT NULL CHECK (octet_length(ref_closure_manifest) > 0),
    captured_at         timestamptz NOT NULL,
    captured_by_issuer  text NOT NULL,
    captured_by_subject text NOT NULL,
    UNIQUE (product_id, api_asset_id, source_snapshot_id),
    UNIQUE (product_id, api_asset_id, ref_closure_digest)
);

CREATE TABLE contractregistry.source_file (
    source_snapshot_id uuid NOT NULL REFERENCES contractregistry.source_snapshot (source_snapshot_id),
    path               text NOT NULL CHECK (char_length(path) BETWEEN 1 AND 200),
    content            bytea NOT NULL CHECK (octet_length(content) > 0),
    PRIMARY KEY (source_snapshot_id, path)
);

CREATE TABLE contractregistry.contract_revision (
    revision_id        uuid PRIMARY KEY,
    product_id         uuid NOT NULL,
    workspace_id       uuid NOT NULL,
    api_asset_id       uuid NOT NULL,
    source_snapshot_id uuid NOT NULL UNIQUE,
    revision_sequence  bigint NOT NULL CHECK (revision_sequence > 0),
    spec_version       text NOT NULL,
    status             text NOT NULL CHECK (status = 'DRAFT'),
    UNIQUE (product_id, api_asset_id, revision_sequence),
    FOREIGN KEY (product_id, api_asset_id, source_snapshot_id)
        REFERENCES contractregistry.source_snapshot (product_id, api_asset_id, source_snapshot_id)
);
