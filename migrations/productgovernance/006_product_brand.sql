-- Product Governance owns how a product presents itself to the developers it
-- documents its APIs for: a name, a logo and a colour (FR-005). The brand is
-- part of the product, and changing it is a change of the product: it counts
-- in the version of the product, and the product says who changed it last.
--
-- Until now a process registered a product and read it. It now changes its
-- brand too, and nothing else of it. The trigger below holds it to that, so
-- the UPDATE the application role is granted on the table lets it do no more.
-- No statement removes a product, and the role still holds no DELETE.

ALTER TABLE productgovernance.product
    -- The name the product presents itself under. NULL in a row a build
    -- before this one wrote, which is read as the display name: what a
    -- product is registered with for it.
    ADD COLUMN brand_name          text,
    -- Where the logo is fetched from, and the colour of the product. NULL
    -- for as long as the product has none.
    ADD COLUMN brand_logo_url      text,
    ADD COLUMN brand_primary_color text,
    -- Who changed the product last. NULL in a row a build before this one
    -- wrote, which is read as whoever registered the product.
    ADD COLUMN updated_by_issuer   text,
    ADD COLUMN updated_by_subject  text,

    -- The model holds a brand to its rules. The table holds it to what a
    -- reader of the row relies on: a name that is one, an https URL within
    -- its bound, and a colour of six hexadecimal digits.
    ADD CONSTRAINT product_brand_name_bounded
        CHECK (brand_name IS NULL OR char_length(brand_name) BETWEEN 1 AND 120),
    ADD CONSTRAINT product_brand_logo_https
        CHECK (brand_logo_url IS NULL OR (brand_logo_url LIKE 'https://_%' AND char_length(brand_logo_url) <= 2048)),
    ADD CONSTRAINT product_brand_color_hex
        CHECK (brand_primary_color IS NULL OR brand_primary_color ~ '^#[0-9A-Fa-f]{6}$'),
    -- Whoever changed the product is a principal: an issuer and a subject,
    -- or neither.
    ADD CONSTRAINT product_updated_by_whole
        CHECK ((updated_by_issuer IS NULL) = (updated_by_subject IS NULL)),
    ADD CONSTRAINT product_updated_since_created CHECK (updated_at >= created_at);

-- The application role needs UPDATE to change the brand of a product, and
-- UPDATE alone would let it rewrite one: rename it, give it another key, hand
-- it to another organization, or suspend it. The table decides instead, for
-- every writer. What a product was registered as never changes. A change of
-- a product is a new brand under a name, with who changed it and when, at
-- its next version, and nothing else about it moves with that.
--
-- The status is the one exception. Suspending a product, and resuming it, is
-- no statement of any build yet: an operator sets the status by hand
-- (INV-01). That stays possible for the role that owns the table, which is
-- never the one a process connects with, and it changes the status and
-- nothing with it. The build that brings the statement replaces this
-- function.
CREATE FUNCTION productgovernance.product_only_changes_its_brand() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.product_id, NEW.product_key, NEW.display_name,
        NEW.owner_identity_provider, NEW.owner_organization,
        NEW.created_at, NEW.created_by_issuer, NEW.created_by_subject)
       IS DISTINCT FROM
       (OLD.product_id, OLD.product_key, OLD.display_name,
        OLD.owner_identity_provider, OLD.owner_organization,
        OLD.created_at, OLD.created_by_issuer, OLD.created_by_subject)
    THEN
        RAISE EXCEPTION 'what a product was registered as does not change'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    IF NEW.status IS DISTINCT FROM OLD.status THEN
        IF NOT pg_has_role(current_user, (SELECT relowner FROM pg_class WHERE oid = TG_RELID), 'USAGE')
           OR (NEW.aggregate_version, NEW.updated_at, NEW.updated_by_issuer, NEW.updated_by_subject,
               NEW.brand_name, NEW.brand_logo_url, NEW.brand_primary_color)
              IS DISTINCT FROM
              (OLD.aggregate_version, OLD.updated_at, OLD.updated_by_issuer, OLD.updated_by_subject,
               OLD.brand_name, OLD.brand_logo_url, OLD.brand_primary_color)
        THEN
            RAISE EXCEPTION 'the status of a product is set by hand, by the role that owns the table, and nothing with it'
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.aggregate_version <> OLD.aggregate_version + 1
       OR NEW.brand_name IS NULL OR NEW.updated_by_issuer IS NULL
       OR (NEW.brand_name, NEW.brand_logo_url, NEW.brand_primary_color)
          IS NOT DISTINCT FROM
          (OLD.brand_name, OLD.brand_logo_url, OLD.brand_primary_color)
       OR NEW.updated_at < OLD.updated_at
    THEN
        RAISE EXCEPTION 'a change of a product is a new brand under a name, by somebody, at its next version, no earlier than the last change'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER product_only_changes_its_brand
    BEFORE UPDATE ON productgovernance.product
    FOR EACH ROW EXECUTE FUNCTION productgovernance.product_only_changes_its_brand();
