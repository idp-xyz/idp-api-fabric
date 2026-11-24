-- The reviews of a workspace are read as a queue: oldest first, a page at a
-- time, to whoever reads the reviews of the workspace. A request names its
-- workspace and is written once, so the instant it was asked for and its
-- identifier never change: a review keeps its place for as long as it is
-- listed, and a page starts behind the last review of the page before.
--
-- The index is in the order of that list and is read forward, so a page costs
-- what it scans and not what the table holds. The one index that starts with
-- the product is the history of a revision, which is ordered by asset and
-- revision first: without this one a page would read every review of every
-- workspace of the product and sort them.
--
-- Whether a review is still open is no column: it is a conclusion that is
-- not there. No index of this table can say that, and none is added for it.
-- The step changes no row and grants nothing: what the application role may
-- do on the table is declared in ApplicationPrivileges, and is what it was.
CREATE INDEX review_request_queue_order
    ON policyreview.review_request (product_id, workspace_id, requested_at, review_id);
