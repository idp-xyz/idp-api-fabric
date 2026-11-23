-- A revision that is approved can be reviewed again: for a scope the first
-- approval did not name, or because that approval holds no longer, having
-- run out or resting on a check the policy and the tools have moved on from
-- (F03 §8.1, F04 §7.2). Such a review is supplementary. It asks for another
-- approval of the same content and moves nothing: the revision stays
-- approved while the review is open and however it ends.
--
-- Every request there is was made of a revision that was not approved, so
-- the column has that as its default, and a build before this one, which
-- names no such column, keeps recording requests of that kind.
ALTER TABLE policyreview.review_request
    ADD COLUMN supplementary boolean NOT NULL DEFAULT false,
    DROP CONSTRAINT review_request_once_key;

-- A request that puts a revision in review is the only one at the version
-- the status of the revision took then, as before. A supplementary request
-- records the version the status of the approved revision has, which no
-- review changes, so any number of them share it. That a revision is under
-- one review at a time is, for supplementary requests, for whoever records
-- them to see to behind a hold of the revision: these tables cannot tell an
-- open request from a concluded one within a constraint.
CREATE UNIQUE INDEX review_request_once_key
    ON policyreview.review_request (revision_id, revision_status_version)
    WHERE NOT supplementary;
