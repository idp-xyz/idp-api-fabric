-- Both tables are append-only for the application role. The digest addresses
-- the exact artifact bytes, which can be shared by several release records.
CREATE TABLE contractregistry.release_artifact (
    artifact_digest text PRIMARY KEY,
    payload bytea NOT NULL CHECK (octet_length(payload) BETWEEN 1 AND 65536),
    CHECK (artifact_digest = 'sha256:' || encode(sha256(payload), 'hex'))
);

ALTER TABLE contractregistry.contract_revision
    ADD CONSTRAINT contract_revision_release_scope_key UNIQUE (product_id, api_asset_id, workspace_id, revision_id);

CREATE TABLE contractregistry.contract_release (
    release_id uuid PRIMARY KEY,
    product_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    api_asset_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    review_id uuid NOT NULL,
    check_run_id uuid NOT NULL,
    policy_snapshot_id uuid NOT NULL,
    policy_digest text NOT NULL CHECK (policy_digest ~ '^sha256:[0-9a-f]{64}$'),
    input_digest text NOT NULL CHECK (input_digest ~ '^sha256:[0-9a-f]{64}$'),
    artifact_digest text NOT NULL REFERENCES contractregistry.release_artifact (artifact_digest),
    release_version text NOT NULL CHECK (release_version ~ '^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$'),
    prepared_at timestamptz NOT NULL,
    prepared_by_issuer text NOT NULL,
    prepared_by_subject text NOT NULL,
    UNIQUE (product_id, api_asset_id, release_version),
    FOREIGN KEY (product_id, api_asset_id, workspace_id, revision_id)
        REFERENCES contractregistry.contract_revision (product_id, api_asset_id, workspace_id, revision_id)
);

CREATE INDEX contract_release_history
    ON contractregistry.contract_release (product_id, api_asset_id, prepared_at, release_id);
