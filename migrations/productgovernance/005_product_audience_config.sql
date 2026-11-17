-- Product Governance owns which audiences a product documents its APIs for,
-- in which environments, and where each of them enters the documentation
-- (FR-005; ProductAudienceConfig, F03 §5.4).
--
-- A configuration is in force from the moment it is added until it is
-- removed. A removed row is kept, as the record of what the product offered
-- and when (F03 §13). A product starts with none: what it documents for
-- nobody is published to nobody (DC-003).
--
-- A process inserts a configuration, reads it, changes where it is entered
-- and removes it. Those two are the changes a row sees, and the trigger
-- below holds every writer to them. Removing a configuration ends it and
-- keeps its row, so no statement deletes one, and the application role holds
-- no DELETE on the table.

CREATE TABLE productgovernance.product_audience_config (
    audience_config_id      uuid        NOT NULL,
    -- No statement reads a configuration without naming its product.
    product_id              uuid        NOT NULL,
    -- Who the product documents its APIs for, and where they run. What each
    -- of them allows is a rule of the platform (F05 §5.1), not of a product.
    audience                text        NOT NULL,
    environment             text        NOT NULL,
    -- Where the audience enters the documentation of the environment. NULL
    -- for as long as nobody said where.
    docs_url                text,
    -- Counts the changes of the row, for optimistic concurrency and for the
    -- order of its events: 1 when it is added, one more with every change
    -- of its entry and with its removal.
    audience_config_version bigint      NOT NULL,
    created_at              timestamptz NOT NULL,
    created_by_issuer       text        NOT NULL,
    created_by_subject      text        NOT NULL,
    -- The last change of the entry, or the creation until there is one.
    updated_at              timestamptz NOT NULL,
    updated_by_issuer       text        NOT NULL,
    updated_by_subject      text        NOT NULL,
    -- NULL for as long as the configuration is in force.
    removed_at              timestamptz,
    removed_by_issuer       text,
    removed_by_subject      text,

    CONSTRAINT product_audience_config_pkey PRIMARY KEY (audience_config_id),
    CONSTRAINT product_audience_config_product_fkey
        FOREIGN KEY (product_id) REFERENCES productgovernance.product (product_id),
    CONSTRAINT product_audience_config_audience_known
        CHECK (audience IN ('public', 'partner', 'internal')),
    CONSTRAINT product_audience_config_environment_known
        CHECK (environment IN ('sandbox', 'production')),
    -- The model holds an entry to a pattern. The table holds it to what a
    -- reader of the row relies on: an https URL, within its bound.
    CONSTRAINT product_audience_config_docs_url_https
        CHECK (docs_url IS NULL OR (docs_url LIKE 'https://_%' AND char_length(docs_url) <= 2048)),
    CONSTRAINT product_audience_config_version_positive CHECK (audience_config_version >= 1),
    CONSTRAINT product_audience_config_updated_since_created CHECK (updated_at >= created_at),
    -- A removal says when and by whom, or there is none.
    CONSTRAINT product_audience_config_removal_whole CHECK (
        (removed_at IS NULL AND removed_by_issuer IS NULL AND removed_by_subject IS NULL)
        OR (removed_at IS NOT NULL AND removed_by_issuer IS NOT NULL AND removed_by_subject IS NOT NULL)),
    CONSTRAINT product_audience_config_interval CHECK (removed_at IS NULL OR removed_at >= updated_at)
);

-- A product documents its APIs for an audience in an environment at most
-- once at a time: there is one configuration in force (F03 §5.4). Once it is
-- removed the pair can be configured again, as a new configuration.
CREATE UNIQUE INDEX product_audience_config_in_force_unique
    ON productgovernance.product_audience_config (product_id, audience, environment)
    WHERE removed_at IS NULL;

-- The configurations in force of one product, in the order of their list
-- (see 004). A removed row is in no list.
CREATE INDEX product_audience_config_list_order
    ON productgovernance.product_audience_config (product_id, created_at, audience_config_id)
    WHERE removed_at IS NULL;

-- The application role needs UPDATE to change the entry of a configuration
-- and to remove one, and UPDATE alone would let it rewrite one: move it to
-- another product, open it to another audience, or bring a removed one back.
-- The table decides instead, for every writer. A configuration in force gets
-- either a new entry, with who changed it and when, or its removal, and in
-- both cases its next version; nothing else about it moves. A removed row
-- never changes again.
CREATE FUNCTION productgovernance.product_audience_config_only_changes_entry_or_ends() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.removed_at IS NOT NULL THEN
        RAISE EXCEPTION 'a removed audience configuration is a record and does not change'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NEW.audience_config_version <> OLD.audience_config_version + 1
       OR (NEW.audience_config_id, NEW.product_id, NEW.audience, NEW.environment,
           NEW.created_at, NEW.created_by_issuer, NEW.created_by_subject)
          IS DISTINCT FROM
          (OLD.audience_config_id, OLD.product_id, OLD.audience, OLD.environment,
           OLD.created_at, OLD.created_by_issuer, OLD.created_by_subject)
    THEN
        RAISE EXCEPTION 'an audience configuration changes its entry or ends, one version at a time, and nothing else'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NEW.removed_at IS NULL THEN
        IF NEW.docs_url IS NOT DISTINCT FROM OLD.docs_url OR NEW.updated_at < OLD.updated_at THEN
            RAISE EXCEPTION 'a change of an audience configuration is a new entry, no earlier than the last change'
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
    ELSIF (NEW.docs_url, NEW.updated_at, NEW.updated_by_issuer, NEW.updated_by_subject)
          IS DISTINCT FROM
          (OLD.docs_url, OLD.updated_at, OLD.updated_by_issuer, OLD.updated_by_subject)
    THEN
        RAISE EXCEPTION 'the removal of an audience configuration changes nothing else about it'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER product_audience_config_only_changes_entry_or_ends
    BEFORE UPDATE ON productgovernance.product_audience_config
    FOR EACH ROW EXECUTE FUNCTION productgovernance.product_audience_config_only_changes_entry_or_ends();
