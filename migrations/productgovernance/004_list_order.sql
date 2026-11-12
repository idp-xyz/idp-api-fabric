-- Product Governance lists what it owns a page at a time (F04 §3.1).
--
-- A list is ordered by the instant a row was created or granted and then by
-- its identifier, which tells apart the rows of one instant. Neither column
-- ever changes, so a row keeps its place for as long as it is listed, and a
-- page starts behind the last row of the page before.
--
-- Each index below is in the order of one list. A page is read where it
-- starts, so it costs what it returns, however far into the list it is. None
-- of them changes what a table accepts, and a process needs no privilege it
-- does not hold already.

-- Every product, for whoever administers the platform.
CREATE INDEX product_list_order
    ON productgovernance.product (created_at, product_id);

-- The workspaces of one product.
CREATE INDEX workspace_list_order
    ON productgovernance.workspace (product_id, created_at, workspace_id);

-- The memberships in force of one workspace. A revoked row is in no list.
CREATE INDEX workspace_membership_list_order
    ON productgovernance.workspace_membership (product_id, workspace_id, granted_at, membership_id)
    WHERE revoked_at IS NULL;

-- The role assignments in force of one product. A revoked row is in no list.
CREATE INDEX product_role_assignment_list_order
    ON productgovernance.product_role_assignment (product_id, granted_at, role_assignment_id)
    WHERE revoked_at IS NULL;

-- The products a principal holds something in are found from the principal,
-- not by asking every product: somebody holds something in a few products,
-- however many there are. The indexes of the rows in force that were there
-- start with the product or the workspace, and answer for one of those only.
-- These two start with the principal and carry the product, so what it
-- holds across the products is read from them alone.
CREATE INDEX product_role_assignment_of_principal
    ON productgovernance.product_role_assignment (principal_issuer, principal_subject, product_id)
    WHERE revoked_at IS NULL;

CREATE INDEX workspace_membership_of_principal
    ON productgovernance.workspace_membership (principal_issuer, principal_subject, product_id)
    WHERE revoked_at IS NULL;
