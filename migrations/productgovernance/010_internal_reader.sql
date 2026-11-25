ALTER TABLE productgovernance.workspace_membership
    DROP CONSTRAINT workspace_membership_role_known,
    ADD CONSTRAINT workspace_membership_role_known
        CHECK (role IN ('workspace_maintainer', 'internal_author', 'internal_reviewer', 'internal_publisher', 'internal_reader'));
