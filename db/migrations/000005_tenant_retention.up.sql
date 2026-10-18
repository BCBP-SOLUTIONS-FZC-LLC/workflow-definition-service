-- Purge bookkeeping for departed tenants (LLD §7.4.6). No RLS and no foreign
-- key: like connector_rest_alias it is operational state read across tenants
-- by the purge sweep, and it outlives the tenant's own rows.
CREATE TABLE tenant_retention (
    tenant_id             UUID        PRIMARY KEY,
    offboarded_at         TIMESTAMPTZ NOT NULL,
    artifacts_purge_after TIMESTAMPTZ NOT NULL,
    purged_at             TIMESTAMPTZ,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tenant_retention_due ON tenant_retention (artifacts_purge_after) WHERE purged_at IS NULL;
