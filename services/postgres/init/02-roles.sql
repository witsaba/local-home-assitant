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
-- Idempotent: CREATE ROLE IF NOT EXISTS creates the role only if it does
-- not exist. For password rotation we rely on ALTER ROLE in 01-bootstrap.sh
-- after the volume is already initialized, not on this init script.

CREATE ROLE "pg-worker" WITH LOGIN PASSWORD :'pg_worker_password';

CREATE ROLE "pg-messaging-core" WITH LOGIN PASSWORD :'pg_messaging_core_password';
