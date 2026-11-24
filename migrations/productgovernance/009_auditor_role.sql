-- A product gets a fourth role: internal_auditor, who reads the audit records
-- of the product and nothing else (F05 AC-29). The table names the roles it
-- takes, so the role is added here, as the security roles were in 008.
--
-- Nothing about an assignment that exists changes: every row holds one of the
-- three roles there were, and a build before this one keeps granting those
-- three on the migrated database. A process of that build does not answer a
-- role it does not know, so the new role is granted once every process is
-- upgraded. No table and no privilege is added.
ALTER TABLE productgovernance.product_role_assignment
    DROP CONSTRAINT product_role_assignment_role_known,
    ADD CONSTRAINT product_role_assignment_role_known
        CHECK (role IN ('product_owner', 'security_admin', 'security_reviewer', 'internal_auditor'));
