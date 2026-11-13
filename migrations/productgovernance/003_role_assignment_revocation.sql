-- Product Governance owns who holds which role in a product (F03 §2, §4).
--
-- A role assignment was written once, when its product was registered, and
-- never ended: it had no identifier of its own and no statement changed it.
-- From here on it is a record like a membership of a workspace. It is in
-- force from the moment it is granted until it is revoked. A revoked row is
-- kept, as the record of who held what and when (F03 §13), and grants
-- nothing. Authorization is decided from the rows in force when a request
-- arrives, never from a claim of a token (F05 §4.2).
--
-- A process inserts an assignment, reads it and ends it. Ending it is the one
-- change a row sees, and the trigger below holds every writer to that. A
-- process has no statement that removes an assignment, so the application
-- role holds no DELETE on the table.

-- The rows written before this migration are the first owners of the
-- products registered so far. None of them was ever ended: each stays in
-- force, at its first version, and gets an identifier of its own here.
--
-- The two defaults stay. The build before this one writes the first owners
-- of a product without an identifier or a version, and may still run while
-- this one is rolled out: what it writes is an assignment in force like any
-- other. A process of this build names both.
ALTER TABLE productgovernance.product_role_assignment
    ADD COLUMN role_assignment_id      uuid        NOT NULL DEFAULT gen_random_uuid(),
    -- Counts the changes of the row, for the order of its events: 1 when the
    -- role is granted, 2 once it is revoked.
    ADD COLUMN role_assignment_version bigint      NOT NULL DEFAULT 1,
    -- NULL for as long as the assignment is in force.
    ADD COLUMN revoked_at              timestamptz,
    ADD COLUMN revoked_by_issuer       text,
    ADD COLUMN revoked_by_subject      text;

-- An assignment is identified by its identifier from now on, not by what it
-- grants to whom: once it is revoked, the same role can be granted to the
-- same principal again, as another assignment.
ALTER TABLE productgovernance.product_role_assignment
    DROP CONSTRAINT product_role_assignment_pkey,
    ADD CONSTRAINT product_role_assignment_pkey PRIMARY KEY (role_assignment_id),
    ADD CONSTRAINT product_role_assignment_version_positive CHECK (role_assignment_version >= 1),
    -- A revocation says when and by whom, or there is none.
    ADD CONSTRAINT product_role_assignment_revocation_whole CHECK (
        (revoked_at IS NULL AND revoked_by_issuer IS NULL AND revoked_by_subject IS NULL)
        OR (revoked_at IS NOT NULL AND revoked_by_issuer IS NOT NULL AND revoked_by_subject IS NOT NULL)),
    ADD CONSTRAINT product_role_assignment_interval CHECK (revoked_at IS NULL OR revoked_at >= granted_at);

-- A principal holds a role in a product at most once at a time: there is one
-- record in force, which is what the primary key held while no assignment
-- ended. Once it is revoked the role can be granted again, as a new
-- assignment. What a principal holds in a product is read on every request
-- about it, through this index.
CREATE UNIQUE INDEX product_role_assignment_in_force_unique
    ON productgovernance.product_role_assignment (product_id, principal_issuer, principal_subject, role)
    WHERE revoked_at IS NULL;

-- The application role needs UPDATE to end an assignment, and UPDATE alone
-- would let it rewrite one: make somebody else the owner, or bring a revoked
-- one back. The table decides instead, for every writer: an assignment in
-- force gets its revocation and its next version, and nothing else about it
-- moves. A revoked row never changes again.
--
-- That a product keeps an owner is not decided here. It takes counting the
-- owners in force while no other revocation can end one of them, and the use
-- case that revokes does that, in its transaction.
CREATE FUNCTION productgovernance.product_role_assignment_only_ends() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.revoked_at IS NOT NULL THEN
        RAISE EXCEPTION 'a revoked role assignment is a record and does not change'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NEW.revoked_at IS NULL
       OR NEW.role_assignment_version <> OLD.role_assignment_version + 1
       OR (NEW.role_assignment_id, NEW.product_id, NEW.principal_issuer, NEW.principal_subject,
           NEW.role, NEW.granted_at, NEW.granted_by_issuer, NEW.granted_by_subject)
          IS DISTINCT FROM
          (OLD.role_assignment_id, OLD.product_id, OLD.principal_issuer, OLD.principal_subject,
           OLD.role, OLD.granted_at, OLD.granted_by_issuer, OLD.granted_by_subject)
    THEN
        RAISE EXCEPTION 'the one change of a role assignment is its revocation'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER product_role_assignment_only_ends
    BEFORE UPDATE ON productgovernance.product_role_assignment
    FOR EACH ROW EXECUTE FUNCTION productgovernance.product_role_assignment_only_ends();
