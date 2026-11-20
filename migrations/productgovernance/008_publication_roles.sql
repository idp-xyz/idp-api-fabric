ALTER TABLE productgovernance.workspace_membership
    DROP CONSTRAINT workspace_membership_role_known,
    ADD CONSTRAINT workspace_membership_role_known
        CHECK (role IN ('workspace_maintainer', 'internal_author', 'internal_reviewer', 'internal_publisher'));

ALTER TABLE productgovernance.product_role_assignment
    DROP CONSTRAINT product_role_assignment_role_known,
    ADD CONSTRAINT product_role_assignment_role_known
        CHECK (role IN ('product_owner', 'security_admin', 'security_reviewer'));
