-- No review is asked for or decided any more (CR-0001), so a revision that
-- is in review would stay there for good. Every one is withdrawn: the move a
-- withdrawal of its review made, one step and at the next version of its
-- status, as the table lets every move be made. A revision that left its
-- review stays where it is.
--
-- Policy & Review concludes the reviews these revisions were in as
-- withdrawn, in a step of its own (policyreview/007_open_reviews_withdrawn):
-- each context changes its own tables. A revision is in review under one
-- open review, which is not supplementary, so the two steps move the same
-- revisions.
UPDATE contractregistry.contract_revision
SET status = 'WITHDRAWN', status_version = status_version + 1
WHERE status = 'IN_REVIEW';
