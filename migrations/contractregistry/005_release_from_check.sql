-- A release is prepared on a check of its revision and no longer under an
-- approval (CR-0001). It names the check as before, and the rules that did
-- not pass in that check and that whoever prepared the release waived, each
-- with its reason, as a JSON array of {"rule_id", "reason"} in the order of
-- the rules. Which rules those are is for Policy & Review to say, which
-- reads the check: the table holds what the release was prepared with.
--
-- A release prepared under an approval keeps the review it names and has no
-- waivers of its own: they are the approval's. A release prepared on a check
-- names no review. A build before this one names a review and no waivers,
-- and keeps recording releases on the migrated database.
--
-- The table stays append-only for the application role, which may add and
-- read releases as before: the step grants nothing.
ALTER TABLE contractregistry.contract_release
    ALTER COLUMN review_id DROP NOT NULL,
    ADD COLUMN waivers jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD CONSTRAINT contract_release_waivers_check CHECK (jsonb_typeof(waivers) = 'array'),
    ADD CONSTRAINT contract_release_approval_waivers_check CHECK (review_id IS NULL OR waivers = '[]'::jsonb);
