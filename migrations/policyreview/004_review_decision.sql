-- A review ends in one of three ways now: whoever asked for it withdraws it,
-- or a reviewer approves or rejects the revision. An approval holds until a
-- time and may waive rules that did not pass, each for a reason and until a
-- time of its own; a rejection says why. Like everything else of a review,
-- a decision and its waivers are written once: the tables are append-only
-- for the application role.
ALTER TABLE policyreview.review_conclusion
    ADD COLUMN valid_until timestamptz,
    DROP CONSTRAINT review_conclusion_outcome_check,
    ADD CONSTRAINT review_conclusion_outcome_check
        CHECK (outcome IN ('WITHDRAWN', 'APPROVED', 'REJECTED')),
    -- An approval holds until a time after it was given, and nothing else
    -- holds for a time.
    ADD CONSTRAINT review_conclusion_validity_check
        CHECK ((outcome = 'APPROVED') = (valid_until IS NOT NULL)
               AND (valid_until IS NULL OR valid_until > concluded_at)),
    ADD CONSTRAINT review_conclusion_rejection_check
        CHECK (outcome <> 'REJECTED' OR reason IS NOT NULL);

-- The rules an approval lets stand although they did not pass. The rule is
-- named by the identifier it has in the policy of the check the review rests
-- on; whether that rule can be waived at all is for the model to say, which
-- reads the policy.
CREATE TABLE policyreview.review_waiver (
    review_id            uuid NOT NULL REFERENCES policyreview.review_conclusion (review_id),
    rule_id              text NOT NULL CHECK (char_length(rule_id) BETWEEN 1 AND 60),
    reason               text NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 1000),
    -- What makes up for the rule that does not hold, when something does.
    compensating_control text CHECK (char_length(compensating_control) BETWEEN 1 AND 1000),
    expires_at           timestamptz NOT NULL,
    PRIMARY KEY (review_id, rule_id)
);
