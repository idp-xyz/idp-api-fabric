-- A check run is evidence: what was read, the policy it was held to, the
-- tools that read it and what they found. The four tables are append-only
-- for the application role. A check run names the revision it is of by the
-- identifiers Contract Registry and API Catalog issued; no table here refers
-- to a table of another context.

-- The policy a check was held to, written out as the check knew it. A
-- product has one snapshot of a policy: the rule set that applies to a
-- product may come to differ from that of another, and a snapshot is what
-- applied to this one. The digest is the digest of the document, and the
-- table says so itself.
CREATE TABLE policyreview.policy_snapshot (
    policy_snapshot_id uuid PRIMARY KEY,
    product_id         uuid NOT NULL,
    policy_set_id      text NOT NULL CHECK (char_length(policy_set_id) BETWEEN 1 AND 60),
    policy_digest      text NOT NULL,
    policy_document    bytea NOT NULL CHECK (octet_length(policy_document) > 0),
    recorded_at        timestamptz NOT NULL,
    CONSTRAINT policy_snapshot_digest_check
        CHECK (policy_digest = 'sha256:' || encode(sha256(policy_document), 'hex')),
    UNIQUE (product_id, policy_digest),
    UNIQUE (product_id, policy_snapshot_id)
);

CREATE TABLE policyreview.check_run (
    check_run_id         uuid PRIMARY KEY,
    product_id           uuid NOT NULL,
    workspace_id         uuid NOT NULL,
    api_asset_id         uuid NOT NULL,
    revision_id          uuid NOT NULL,
    policy_snapshot_id   uuid NOT NULL,
    -- The digest of the closure that was read: ref_closure_digest of the
    -- revision.
    input_digest         text NOT NULL CHECK (input_digest ~ '^sha256:[0-9a-f]{64}$'),
    tool_manifest        bytea NOT NULL CHECK (octet_length(tool_manifest) > 0),
    tool_digest          text NOT NULL,
    -- What the outcomes of the rules add up to. A run that is recorded has
    -- been concluded, so it is never NOT_RUN.
    status               text NOT NULL CHECK (status IN ('PASS', 'WARN', 'FAIL', 'UNKNOWN')),
    started_at           timestamptz NOT NULL,
    completed_at         timestamptz NOT NULL,
    requested_by_issuer  text NOT NULL CHECK (requested_by_issuer <> ''),
    requested_by_subject text NOT NULL CHECK (requested_by_subject <> ''),
    CONSTRAINT check_run_tool_digest_check
        CHECK (tool_digest = 'sha256:' || encode(sha256(tool_manifest), 'hex')),
    CONSTRAINT check_run_period_check CHECK (completed_at >= started_at),
    -- The same input under the same policy by the same tools concludes the
    -- same, so it is checked once.
    CONSTRAINT check_run_once_key UNIQUE (revision_id, policy_snapshot_id, tool_digest),
    CONSTRAINT check_run_policy_fkey FOREIGN KEY (product_id, policy_snapshot_id)
        REFERENCES policyreview.policy_snapshot (product_id, policy_snapshot_id)
);

-- The checks of a revision, in the order they were concluded.
CREATE INDEX check_run_history
    ON policyreview.check_run (product_id, api_asset_id, revision_id, completed_at, check_run_id);

-- One row for each rule of the policy: what the check concluded of it. The
-- rule itself is in the policy snapshot.
CREATE TABLE policyreview.check_rule_outcome (
    check_run_id  uuid NOT NULL REFERENCES policyreview.check_run (check_run_id),
    rule_id       text NOT NULL CHECK (char_length(rule_id) BETWEEN 1 AND 60),
    -- The place of the rule in the policy.
    rule_position integer NOT NULL CHECK (rule_position >= 0),
    status        text NOT NULL CHECK (status IN ('PASS', 'WARN', 'FAIL', 'UNKNOWN', 'NOT_RUN')),
    -- Every finding of the rule, also those check_finding does not keep.
    finding_count integer NOT NULL CHECK (finding_count >= 0),
    tool_name     text,
    tool_version  text,
    PRIMARY KEY (check_run_id, rule_id),
    UNIQUE (check_run_id, rule_position),
    -- A rule that was not run was evaluated by no tool, and every other one
    -- by a tool that is named with its version.
    CONSTRAINT check_rule_outcome_tool_check
        CHECK ((status = 'NOT_RUN') = (tool_name IS NULL) AND (tool_name IS NULL) = (tool_version IS NULL)),
    -- A rule passed or was not run without a finding, and failed or warned
    -- with one.
    CONSTRAINT check_rule_outcome_findings_check
        CHECK (CASE WHEN status IN ('PASS', 'NOT_RUN') THEN finding_count = 0
                    WHEN status IN ('FAIL', 'WARN') THEN finding_count > 0
                    ELSE true
               END)
);

CREATE TABLE policyreview.check_finding (
    check_run_id uuid NOT NULL,
    -- The place of the finding among the findings of the run.
    ordinal      integer NOT NULL CHECK (ordinal >= 1),
    rule_id      text NOT NULL,
    level        text NOT NULL CHECK (level IN ('BLOCK', 'WARN')),
    file_path    text NOT NULL CHECK (char_length(file_path) BETWEEN 1 AND 200),
    -- A JSON Pointer inside the file, when the place has one.
    pointer      text CHECK (pointer ~ '^/' AND char_length(pointer) <= 1024),
    -- A line of the file, counted from 1, when it is known.
    line         integer CHECK (line >= 1),
    message      text NOT NULL CHECK (char_length(message) BETWEEN 1 AND 300),
    PRIMARY KEY (check_run_id, ordinal),
    -- A finding is a finding of a rule of its run.
    CONSTRAINT check_finding_rule_fkey FOREIGN KEY (check_run_id, rule_id)
        REFERENCES policyreview.check_rule_outcome (check_run_id, rule_id)
);
