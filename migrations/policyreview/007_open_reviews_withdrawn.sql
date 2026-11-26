-- No review is asked for or decided any more (CR-0001): a release is
-- prepared on a check. A review that is still open would never end, so each
-- one is concluded as withdrawn, as its requester would have withdrawn it,
-- supplementary reviews of approved revisions included, which leaves those
-- revisions approved. Contract Registry withdraws the revisions that were
-- in review in a step of its own (contractregistry/006_open_reviews_withdrawn).
--
-- The conclusion says why, and names no person: it is concluded by a
-- principal no token is issued for, the migration itself. It is never
-- earlier than the request, whatever the clocks said. What was concluded
-- before stays what it is.
INSERT INTO policyreview.review_conclusion (
    review_id, outcome, reason, concluded_at, concluded_by_issuer, concluded_by_subject
)
SELECT q.review_id, 'WITHDRAWN',
       'Withdrawn when reviews were retired by CR-0001: a release is prepared on a check and needs no approval.',
       greatest(now(), q.requested_at), 'urn:idp-api-fabric:system', 'migration/policyreview/007_open_reviews_withdrawn'
FROM policyreview.review_request q
WHERE NOT EXISTS (SELECT 1 FROM policyreview.review_conclusion c WHERE c.review_id = q.review_id);
