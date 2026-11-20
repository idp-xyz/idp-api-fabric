-- A revision gets a status that moves: it is recorded as a draft, is in
-- review once it is submitted, and leaves the review approved, rejected or
-- withdrawn (F03 §8.1). Its content does not move with it: a revision is
-- still the record of one source, and what is wrong with it is mended in
-- another one.
--
-- Every revision there is stays the draft it is, at version 1 of its status.
-- A build before this one writes drafts and names no version, so the column
-- has a default, and that build keeps recording on the migrated database.
ALTER TABLE contractregistry.contract_revision
    ADD COLUMN status_version bigint NOT NULL DEFAULT 1,
    DROP CONSTRAINT contract_revision_status_check,
    ADD CONSTRAINT contract_revision_status_check
        CHECK (status IN ('DRAFT', 'IN_REVIEW', 'APPROVED', 'REJECTED', 'WITHDRAWN')),
    ADD CONSTRAINT contract_revision_status_version_check CHECK (status_version >= 1);

-- The application role needs UPDATE to move a status, and UPDATE alone would
-- let it rewrite a revision. The table decides instead, for every writer: a
-- revision begins as a draft; its status moves one step at a time along the
-- review, each move at the next version; and nothing else of it changes.
-- Approved and rejected are where a review ends, and nothing leaves them.
CREATE FUNCTION contractregistry.contract_revision_begins_as_draft() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status <> 'DRAFT' OR NEW.status_version <> 1 THEN
        RAISE EXCEPTION 'a revision is recorded as a draft, at version 1 of its status'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER contract_revision_begins_as_draft
    BEFORE INSERT ON contractregistry.contract_revision
    FOR EACH ROW EXECUTE FUNCTION contractregistry.contract_revision_begins_as_draft();

CREATE FUNCTION contractregistry.contract_revision_only_moves() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.revision_id, NEW.product_id, NEW.workspace_id, NEW.api_asset_id,
        NEW.source_snapshot_id, NEW.revision_sequence, NEW.spec_version)
       IS DISTINCT FROM
       (OLD.revision_id, OLD.product_id, OLD.workspace_id, OLD.api_asset_id,
        OLD.source_snapshot_id, OLD.revision_sequence, OLD.spec_version)
    THEN
        RAISE EXCEPTION 'a revision is a record: only its status moves'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NEW.status_version <> OLD.status_version + 1
       OR NOT ((OLD.status IN ('DRAFT', 'WITHDRAWN') AND NEW.status = 'IN_REVIEW')
               OR (OLD.status = 'IN_REVIEW' AND NEW.status IN ('APPROVED', 'REJECTED', 'WITHDRAWN')))
    THEN
        RAISE EXCEPTION 'the status of a revision moves along its review, one step and one version at a time'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER contract_revision_only_moves
    BEFORE UPDATE ON contractregistry.contract_revision
    FOR EACH ROW EXECUTE FUNCTION contractregistry.contract_revision_only_moves();
