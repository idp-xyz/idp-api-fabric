CREATE TABLE publication.target (
    target_id uuid PRIMARY KEY,
    product_id uuid NOT NULL,
    audience_config_id uuid NOT NULL,
    portal_key text NOT NULL CHECK (portal_key ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    audience text NOT NULL CHECK (audience IN ('internal', 'partner', 'public')),
    environment text NOT NULL CHECK (environment IN ('sandbox', 'production')),
    block_generation bigint NOT NULL DEFAULT 0 CHECK (block_generation >= 0),
    created_at timestamptz NOT NULL,
    UNIQUE (product_id, audience_config_id, portal_key),
    UNIQUE (product_id, target_id)
);

CREATE INDEX publication_target_list ON publication.target (product_id, created_at, target_id);

CREATE TABLE publication.slot (
    slot_id uuid PRIMARY KEY,
    product_id uuid NOT NULL,
    target_id uuid NOT NULL,
    api_asset_id uuid NOT NULL,
    active_release_id uuid,
    generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
    CHECK (generation > 0 OR active_release_id IS NULL),
    UNIQUE (target_id, api_asset_id),
    FOREIGN KEY (product_id, target_id) REFERENCES publication.target (product_id, target_id)
);

CREATE TABLE publication.operation (
    operation_id uuid PRIMARY KEY,
    slot_id uuid NOT NULL REFERENCES publication.slot (slot_id),
    generation bigint NOT NULL CHECK (generation > 0),
    key_digest text NOT NULL CHECK (key_digest ~ '^[0-9a-f]{64}$'),
    action text NOT NULL CHECK (action IN ('ACTIVATE', 'WITHDRAW', 'RESTORE')),
    previous_release_id uuid,
    release_id uuid,
    reason text NOT NULL CHECK (char_length(reason) <= 500),
    actor_issuer text NOT NULL,
    actor_subject text NOT NULL,
    occurred_at timestamptz NOT NULL,
    CHECK ((action = 'WITHDRAW') = (release_id IS NULL)),
    CHECK (action = 'ACTIVATE' OR reason <> ''),
    UNIQUE (slot_id, generation),
    UNIQUE (slot_id, key_digest)
);

CREATE INDEX publication_operation_release_history
    ON publication.operation (slot_id, release_id, generation DESC) WHERE release_id IS NOT NULL;

CREATE TABLE publication.access_block (
    block_id uuid PRIMARY KEY,
    product_id uuid NOT NULL,
    target_id uuid NOT NULL,
    api_asset_id uuid,
    release_id uuid,
    generation bigint NOT NULL CHECK (generation > 0),
    reason text NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 500),
    created_by_issuer text NOT NULL,
    created_by_subject text NOT NULL,
    created_at timestamptz NOT NULL,
    clear_generation bigint CHECK (clear_generation > generation),
    clear_reason text CHECK (char_length(clear_reason) BETWEEN 1 AND 500),
    cleared_by_issuer text,
    cleared_by_subject text,
    cleared_at timestamptz CHECK (cleared_at >= created_at),
    CHECK (release_id IS NULL OR api_asset_id IS NOT NULL),
    CHECK (num_nonnulls(clear_generation, clear_reason, cleared_by_issuer, cleared_by_subject, cleared_at) IN (0, 5)),
    UNIQUE (target_id, generation),
    FOREIGN KEY (product_id, target_id) REFERENCES publication.target (product_id, target_id)
);

CREATE UNIQUE INDEX publication_access_block_scope
    ON publication.access_block (target_id, api_asset_id, release_id) NULLS NOT DISTINCT
    WHERE cleared_at IS NULL;

-- A target can advance its block generation, a slot can switch its active
-- release, and a block can be cleared. Each transition preserves its identity.
CREATE FUNCTION publication.step_generation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'target' THEN
        IF NEW.block_generation <> OLD.block_generation + 1
           OR (to_jsonb(NEW) - 'block_generation') IS DISTINCT FROM (to_jsonb(OLD) - 'block_generation') THEN
            RAISE EXCEPTION 'a target advances its block generation by one';
        END IF;
    ELSIF NEW.generation <> OLD.generation + 1
          OR (to_jsonb(NEW) - ARRAY['generation', 'active_release_id'])
             IS DISTINCT FROM (to_jsonb(OLD) - ARRAY['generation', 'active_release_id']) THEN
        RAISE EXCEPTION 'a slot advances its generation by one';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER target_step_generation BEFORE UPDATE ON publication.target
    FOR EACH ROW EXECUTE FUNCTION publication.step_generation();
CREATE TRIGGER slot_step_generation BEFORE UPDATE ON publication.slot
    FOR EACH ROW EXECUTE FUNCTION publication.step_generation();

CREATE FUNCTION publication.clear_block_once() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.cleared_at IS NOT NULL OR NEW.cleared_at IS NULL
       OR (to_jsonb(NEW) - ARRAY['clear_generation', 'clear_reason', 'cleared_by_issuer', 'cleared_by_subject', 'cleared_at'])
          IS DISTINCT FROM (to_jsonb(OLD) - ARRAY['clear_generation', 'clear_reason', 'cleared_by_issuer', 'cleared_by_subject', 'cleared_at'])
       OR (OLD.created_by_issuer, OLD.created_by_subject) = (NEW.cleared_by_issuer, NEW.cleared_by_subject) THEN
        RAISE EXCEPTION 'an independent reviewer clears an access block once';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER access_block_clear_once BEFORE UPDATE ON publication.access_block
    FOR EACH ROW EXECUTE FUNCTION publication.clear_block_once();
