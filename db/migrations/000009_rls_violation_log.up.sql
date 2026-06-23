CREATE TABLE IF NOT EXISTS rls_violation_log (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID,
    user_id    UUID,
    table_name TEXT        NOT NULL,
    operation  TEXT        NOT NULL,
    client_ip  INET,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- No RLS: operational audit log (same class as processed_event, outbox_events).
-- Populated by DB-level triggers on RLS violations; not written by application code.
-- Cleaned by the external periodic job per LLD §7.1.2 (GAP-11).
