-- An HTTP asset takes OpenAPI and an event asset takes AsyncAPI. The
-- version alone does not say which, as both have a 3.1.0, so a revision
-- names its specification. Every revision recorded before is OpenAPI, and
-- the default says so for them.
--
-- The default stays: a build before this one records OpenAPI revisions only
-- and names no specification, and keeps recording them on the migrated
-- database. The application role may only add and read revisions, so the
-- specification of one never changes. The step grants nothing.
ALTER TABLE contractregistry.contract_revision
    ADD COLUMN specification text NOT NULL DEFAULT 'OPENAPI'
        CONSTRAINT contract_revision_specification_check CHECK (specification IN ('OPENAPI', 'ASYNCAPI'));

CREATE FUNCTION contractregistry.contract_revision_specification_is_fixed() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.specification IS DISTINCT FROM OLD.specification THEN
        RAISE EXCEPTION 'the specification of a contract revision is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER contract_revision_specification_is_fixed
    BEFORE UPDATE OF specification ON contractregistry.contract_revision
    FOR EACH ROW EXECUTE FUNCTION contractregistry.contract_revision_specification_is_fixed();
