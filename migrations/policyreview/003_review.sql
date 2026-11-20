-- A review asks for a decision on one revision and is concluded once. A
-- request and a conclusion are evidence like a check run: the three tables
-- are append-only for the application role. A request names the revision by
-- the identifiers Contract Registry and API Catalog issued; no table here
-- refers to a table of another context.

-- A request rests on one check of the revision it is of, and the tables say
-- so themselves.
ALTER TABLE policyreview.check_run
    ADD CONSTRAINT check_run_of_revision_key UNIQUE (revision_id, check_run_id);

CREATE TABLE policyreview.review_request (
    review_id               uuid PRIMARY KEY,
    product_id              uuid NOT NULL,
    workspace_id            uuid NOT NULL,
    api_asset_id            uuid NOT NULL,
    revision_id             uuid NOT NULL,
    -- The version the status of the revision took when this request put it
    -- in review. A revision is recorded at version 1, so the first request
    -- finds it at 2. The registry gives a version once, so a revision is in
    -- review under one request at a time.
    revision_status_version bigint NOT NULL CHECK (revision_status_version >= 2),
    check_run_id            uuid NOT NULL,
    -- What the check read: the closure of the revision.
    input_digest            text NOT NULL CHECK (input_digest ~ '^sha256:[0-9a-f]{64}$'),
    note                    text CHECK (char_length(note) BETWEEN 1 AND 500),
    requested_at            timestamptz NOT NULL,
    requested_by_issuer     text NOT NULL CHECK (requested_by_issuer <> ''),
    requested_by_subject    text NOT NULL CHECK (requested_by_subject <> ''),
    CONSTRAINT review_request_once_key UNIQUE (revision_id, revision_status_version),
    CONSTRAINT review_request_check_fkey FOREIGN KEY (revision_id, check_run_id)
        REFERENCES policyreview.check_run (revision_id, check_run_id)
);

-- The reviews of a revision, in the order they were asked for.
CREATE INDEX review_request_history
    ON policyreview.review_request (product_id, api_asset_id, revision_id, requested_at, review_id);

-- What a review is asked to approve the revision for: audiences in
-- environments, as Product Governance names them.
CREATE TABLE policyreview.review_scope (
    review_id   uuid NOT NULL REFERENCES policyreview.review_request (review_id),
    audience    text NOT NULL CHECK (audience IN ('public', 'partner', 'internal')),
    environment text NOT NULL CHECK (environment IN ('sandbox', 'production')),
    PRIMARY KEY (review_id, audience, environment)
);

-- How a review ended. A review has one conclusion, and so far there is one
-- way to end: whoever asked for it withdraws it.
CREATE TABLE policyreview.review_conclusion (
    review_id            uuid PRIMARY KEY REFERENCES policyreview.review_request (review_id),
    outcome              text NOT NULL CHECK (outcome IN ('WITHDRAWN')),
    reason               text CHECK (char_length(reason) BETWEEN 1 AND 1000),
    concluded_at         timestamptz NOT NULL,
    concluded_by_issuer  text NOT NULL CHECK (concluded_by_issuer <> ''),
    concluded_by_subject text NOT NULL CHECK (concluded_by_subject <> '')
);
