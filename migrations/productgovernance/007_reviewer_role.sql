-- A workspace gets a third role: internal_reviewer, who reads the contract
-- revisions of the workspace and what was checked of them, to decide on a
-- review. The table names the roles it takes, so the role is added here.
--
-- Nothing about a membership that exists changes: every row holds one of the
-- two roles there were, and a build before this one keeps granting those two
-- on the migrated database.
ALTER TABLE productgovernance.workspace_membership
    DROP CONSTRAINT workspace_membership_role_known,
    ADD CONSTRAINT workspace_membership_role_known
        CHECK (role IN ('workspace_maintainer', 'internal_author', 'internal_reviewer'));
