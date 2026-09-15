-- The outbox_events / outbox_dead_letters tables are created by
-- platform-events outbox.ApplySchema (run before these domain migrations).
-- The RLS policies are a service-specific decision and live here.
ALTER TABLE outbox_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON outbox_events
    USING (tenant_id = current_setting('app.tenant_id', true));

ALTER TABLE outbox_dead_letters ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON outbox_dead_letters
    USING (tenant_id = current_setting('app.tenant_id', true));
