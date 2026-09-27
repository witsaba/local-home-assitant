-- Create the two service roles used by the witsaba Go services.
--
-- pg-worker            : used by services/workers. Will get INSERT/UPDATE/SELECT
--                        on witsaba.devices (granted in 03-schema.sql).
-- pg-messaging-core    : used by services/messaging-core. Will get SELECT
--                        on witsaba.devices (granted in 03-schema.sql).
--
-- Both roles are NOLOGIN-equivalent for now: LOGIN is enabled because the
-- services connect with these credentials, but they have no CREATEDB,
-- no CREATEROLE, and no SUPERUSER. Schema migrations are run as pg-admin
-- (the POSTGRES_USER created by the image entrypoint).
--
-- Idempotent: if a role already exists, we update its password instead
-- of failing. This makes re-running the bootstrap against a partially
-- initialized volume safe.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pg-worker') THEN
        CREATE ROLE "pg-worker" WITH LOGIN PASSWORD :'pg_worker_password';
    ELSE
        ALTER ROLE "pg-worker" WITH LOGIN PASSWORD :'pg_worker_password';
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pg-messaging-core') THEN
        CREATE ROLE "pg-messaging-core" WITH LOGIN PASSWORD :'pg_messaging_core_password';
    ELSE
        ALTER ROLE "pg-messaging-core" WITH LOGIN PASSWORD :'pg_messaging_core_password';
    END IF;
END
$$;
