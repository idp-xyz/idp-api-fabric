ALTER TABLE policyreview.check_run
    ADD COLUMN baseline_revision_id uuid,
    ADD COLUMN compatibility_document bytea,
    ADD CONSTRAINT check_run_compatibility_whole CHECK (
        (baseline_revision_id IS NULL AND compatibility_document IS NULL)
        OR (baseline_revision_id IS NOT NULL AND baseline_revision_id <> revision_id
            AND compatibility_document IS NOT NULL AND octet_length(compatibility_document) > 0)
    ),
    DROP CONSTRAINT check_run_once_key,
    ADD CONSTRAINT check_run_once_key UNIQUE NULLS NOT DISTINCT (
        revision_id, policy_snapshot_id, tool_digest, baseline_revision_id
    );
