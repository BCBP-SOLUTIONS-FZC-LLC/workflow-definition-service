-- Local development bootstrap. It runs once, as the container's bootstrap
-- superuser (POSTGRES_USER=postgres in docker-compose.yml), when the data
-- volume is first initialised. `make docker-reset` drops the volume and runs it
-- again. Production roles come from the platform's provisioning; this file is
-- never applied there.
--
-- Three roles mirror production so row-level security is exercised locally:
--   postgres                       bootstrap superuser, used by this script only
--   workflow_definition_migrator   runs migrations (MIGRATION_DATABASE_URL), owns
--                                  the schema objects, and is the BYPASSRLS system
--                                  pool role (SYSTEM_DATABASE_URL)
--   workflow_definition_app        the application pool (DATABASE_URL): DML only,
--                                  NOSUPERUSER NOBYPASSRLS, so every query is
--                                  subject to the FORCE ROW LEVEL SECURITY policies;
--                                  the service refuses to start if it can bypass them

DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'workflow_definition_migrator') THEN
    CREATE ROLE workflow_definition_migrator LOGIN PASSWORD 'devpassword';
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'workflow_definition_app') THEN
    CREATE ROLE workflow_definition_app LOGIN PASSWORD 'devpassword';
  END IF;
END $$;

ALTER ROLE workflow_definition_app NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
ALTER ROLE workflow_definition_migrator NOSUPERUSER BYPASSRLS NOCREATEDB NOCREATEROLE;

CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

GRANT CONNECT ON DATABASE workflow_definition TO workflow_definition_app, workflow_definition_migrator;
GRANT ALL PRIVILEGES ON DATABASE workflow_definition TO workflow_definition_migrator;
GRANT USAGE, CREATE ON SCHEMA public TO workflow_definition_migrator;
GRANT USAGE ON SCHEMA public TO workflow_definition_app;

-- The app gets DML on every table and sequence the migrator creates. FOR ROLE
-- matters: default privileges apply to objects created by the named role, and
-- without it they would cover only objects this bootstrap superuser creates.
ALTER DEFAULT PRIVILEGES FOR ROLE workflow_definition_migrator IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO workflow_definition_app;
ALTER DEFAULT PRIVILEGES FOR ROLE workflow_definition_migrator IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO workflow_definition_app;
