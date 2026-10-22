-- Purge bookkeeping for departed tenants (LLD §7.4.6). No RLS and no foreign
-- key: like connector_rest_alias it is operational state read across tenants
-- by the purge sweep, and it outlives the tenant's own rows. Credentials and
-- the design-time artifacts have separate periods and separate completion marks.
CREATE TABLE tenant_retention (
    tenant_id              UUID        PRIMARY KEY,
    offboarded_at          TIMESTAMPTZ NOT NULL,
    credentials_purge_after TIMESTAMPTZ NOT NULL,
    artifacts_purge_after  TIMESTAMPTZ NOT NULL,
    credentials_purged_at  TIMESTAMPTZ,
    purged_at              TIMESTAMPTZ,
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tenant_retention_credentials_due ON tenant_retention (credentials_purge_after) WHERE credentials_purged_at IS NULL;
CREATE INDEX idx_tenant_retention_artifacts_due ON tenant_retention (artifacts_purge_after) WHERE purged_at IS NULL;
