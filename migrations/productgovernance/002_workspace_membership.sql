-- Product Governance owns who is a member of which workspace (F03 §2, §4;
-- INV-02).
--
-- A membership makes one principal hold one role in one workspace. It is in
-- force from the moment it is granted until it is revoked. A revoked row is
-- kept, as the record of who held what and when (F03 §13), and grants
-- nothing. Authorization is decided from the rows in force when a request
-- arrives, never from a claim of a token (F05 §4.2).
--
-- A process inserts a membership, reads it and ends it. Ending it is the one
-- change a row sees, and the trigger below holds every writer to that. A
-- process has no statement that removes a membership, so the application role
-- holds no DELETE on the table.

-- A membership names its workspace together with the product of that
-- workspace, and the pair is a reference, not two. A reference needs its
-- target to be unique. The pair is: the workspace identifier alone is.
ALTER TABLE productgovernance.workspace
    ADD CONSTRAINT workspace_product_pair_unique UNIQUE (product_id, workspace_id);

CREATE TABLE productgovernance.workspace_membership (
    membership_id      uuid        NOT NULL,
    -- The product the workspace belongs to. No statement reads a membership
    -- without naming it.
    product_id         uuid        NOT NULL,
    workspace_id       uuid        NOT NULL,
    -- The member, as its issuer names it.
    principal_issuer   text        NOT NULL,
    principal_subject  text        NOT NULL,
    role               text        NOT NULL,
    -- Counts the changes of the row, for the order of its events: 1 when the
    -- membership is granted, 2 once it is revoked.
    membership_version bigint      NOT NULL,
    granted_at         timestamptz NOT NULL,
    granted_by_issuer  text        NOT NULL,
    granted_by_subject text        NOT NULL,
    -- NULL for as long as the membership is in force.
    revoked_at         timestamptz,
    revoked_by_issuer  text,
    revoked_by_subject text,

    CONSTRAINT workspace_membership_pkey PRIMARY KEY (membership_id),
    CONSTRAINT workspace_membership_workspace_fkey
        FOREIGN KEY (product_id, workspace_id)
        REFERENCES productgovernance.workspace (product_id, workspace_id),
    CONSTRAINT workspace_membership_role_known
        CHECK (role IN ('workspace_maintainer', 'internal_author')),
    CONSTRAINT workspace_membership_version_positive CHECK (membership_version >= 1),
    -- A revocation says when and by whom, or there is none.
    CONSTRAINT workspace_membership_revocation_whole CHECK (
        (revoked_at IS NULL AND revoked_by_issuer IS NULL AND revoked_by_subject IS NULL)
        OR (revoked_at IS NOT NULL AND revoked_by_issuer IS NOT NULL AND revoked_by_subject IS NOT NULL)),
    CONSTRAINT workspace_membership_interval CHECK (revoked_at IS NULL OR revoked_at >= granted_at)
);

-- A principal holds a role in a workspace at most once at a time: there is
-- one record in force (F03 §4). Once it is revoked the role can be granted
-- again, as a new membership.
CREATE UNIQUE INDEX workspace_membership_in_force_unique
    ON productgovernance.workspace_membership (workspace_id, principal_issuer, principal_subject, role)
    WHERE revoked_at IS NULL;

-- What a principal holds in a product is read on every request about it.
CREATE INDEX workspace_membership_held_by
    ON productgovernance.workspace_membership (product_id, principal_issuer, principal_subject)
    WHERE revoked_at IS NULL;

-- The application role needs UPDATE to end a membership, and UPDATE alone
-- would let it rewrite one: give it to somebody else, or bring a revoked one
-- back. The table decides instead, for every writer: a membership in force
-- gets its revocation and its next version, and nothing else about it moves.
-- A revoked row never changes again.
CREATE FUNCTION productgovernance.workspace_membership_only_ends() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.revoked_at IS NOT NULL THEN
        RAISE EXCEPTION 'a revoked membership is a record and does not change'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NEW.revoked_at IS NULL
       OR NEW.membership_version <> OLD.membership_version + 1
       OR (NEW.membership_id, NEW.product_id, NEW.workspace_id, NEW.principal_issuer, NEW.principal_subject,
           NEW.role, NEW.granted_at, NEW.granted_by_issuer, NEW.granted_by_subject)
          IS DISTINCT FROM
          (OLD.membership_id, OLD.product_id, OLD.workspace_id, OLD.principal_issuer, OLD.principal_subject,
           OLD.role, OLD.granted_at, OLD.granted_by_issuer, OLD.granted_by_subject)
    THEN
        RAISE EXCEPTION 'the one change of a membership is its revocation'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER workspace_membership_only_ends
    BEFORE UPDATE ON productgovernance.workspace_membership
    FOR EACH ROW EXECUTE FUNCTION productgovernance.workspace_membership_only_ends();
