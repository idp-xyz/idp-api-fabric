-- A source snapshot says where its files came from. With the files, a
-- submitter may declare a Git origin: the repository, the commit the files
-- were read at and, when it names one, the reference that pointed at the
-- commit. The registry keeps the declaration as evidence and does not read
-- the repository.
--
-- The columns are nullable: a snapshot without an origin is an UPLOAD, as
-- every row a build before this one wrote is, and as the rows are that such
-- a build still writes while this one is rolled out.
ALTER TABLE contractregistry.source_snapshot
    ADD COLUMN origin_repository text,
    ADD COLUMN origin_commit     text,
    ADD COLUMN origin_ref        text,
    DROP CONSTRAINT source_snapshot_source_kind_check,
    ADD CONSTRAINT source_snapshot_source_kind_check
        CHECK (source_kind IN ('UPLOAD', 'GIT')),
    -- A GIT snapshot names its repository and commit, and only a GIT
    -- snapshot has an origin.
    ADD CONSTRAINT source_snapshot_origin_check
        CHECK (CASE source_kind
                   WHEN 'GIT' THEN origin_repository IS NOT NULL AND origin_commit IS NOT NULL
                   ELSE origin_repository IS NULL AND origin_commit IS NULL AND origin_ref IS NULL
               END),
    -- The model holds an origin to its form. The table keeps what the
    -- indexes below rely on and what must never be stored: a commit is a
    -- full object name, and nothing but an https URL is a repository.
    ADD CONSTRAINT source_snapshot_origin_repository_check
        CHECK (origin_repository ~ '^https://[^/@[:space:]]+(/[^/?#@[:space:]]+)+$'
               AND char_length(origin_repository) <= 300),
    ADD CONSTRAINT source_snapshot_origin_commit_check
        CHECK (origin_commit ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
    ADD CONSTRAINT source_snapshot_origin_ref_check
        CHECK (origin_ref ~ '^refs/[^[:space:]]+$' AND char_length(origin_ref) <= 200),
    -- Replaced below: content is recorded once per source, not once per
    -- asset.
    DROP CONSTRAINT source_snapshot_product_id_api_asset_id_ref_closure_digest_key;

-- The uploads of an asset are one source and each repository is another
-- (ADR-0003: a revision has one source). The same closure is recorded once
-- from each: uploaded by hand and submitted from a repository, it is two
-- snapshots and two revisions, each with its own evidence.
CREATE UNIQUE INDEX source_snapshot_upload_closure_key
    ON contractregistry.source_snapshot (product_id, api_asset_id, ref_closure_digest)
    WHERE source_kind = 'UPLOAD';
CREATE UNIQUE INDEX source_snapshot_git_closure_key
    ON contractregistry.source_snapshot (product_id, api_asset_id, origin_repository, ref_closure_digest)
    WHERE source_kind = 'GIT';

-- A commit never changes, so an entrypoint of a commit is recorded with one
-- closure. A second declaration with another closure contradicts the first.
CREATE UNIQUE INDEX source_snapshot_git_commit_key
    ON contractregistry.source_snapshot (product_id, api_asset_id, origin_repository, origin_commit, entrypoint)
    WHERE source_kind = 'GIT';
