-- Platform Operations owns the audit trail (F03 §3.1, INV-13).
--
-- A row is the minimal fact of one attempt to act: who acted, what was
-- attempted on which resource, when, how it ended and under which rule
-- (F05 §14). A successful write records its row in the transaction of the
-- write itself, so neither exists without the other. A refusal records its
-- row on its own.
--
-- The table is append-only for a Fabric process: the application role may
-- insert and nothing else. It holds no token, no request body and no name of
-- a private object.
CREATE TABLE platformoperations.audit_record (
    audit_id       uuid        NOT NULL,
    recorded_at    timestamptz NOT NULL,
    -- The actor is a principal: a subject as one issuer names it (F05 §2.2).
    actor_issuer   text        NOT NULL,
    actor_subject  text        NOT NULL,
    -- The smallest unit of what was attempted, such as product.register.
    action         text        NOT NULL,
    outcome        text        NOT NULL,
    -- The rule that allowed or refused the action, by its stable identifier
    -- in the access matrix of F05, such as AC-08 or DENY-03.
    rule_id        text        NOT NULL,
    -- The product the action is scoped to. NULL is the platform scope: the
    -- action concerns no single product, as a product registration that was
    -- refused.
    product_id     uuid,
    resource_type  text        NOT NULL,
    -- NULL when the attempt left no resource behind.
    resource_id    uuid,
    -- Ties the row to the request, its log lines and its events.
    correlation_id text        NOT NULL,

    CONSTRAINT audit_record_pkey PRIMARY KEY (audit_id),
    CONSTRAINT audit_record_outcome_known CHECK (outcome IN ('SUCCEEDED', 'DENIED'))
);
