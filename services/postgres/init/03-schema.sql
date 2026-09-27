-- Create the witsaba schema and grant least-privilege access.
--
-- Schema ownership goes to pg-admin (the POSTGRES_USER). Table DDL is a
-- follow-up concern (versioned migrations), so this file does NOT create
-- any tables. The grants below assume tables will live in witsaba and be
-- owned by pg-admin. PostgreSQL default privileges handle the rest: any
-- table created by pg-admin in witsaba will inherit USAGE on the schema
-- and the GRANTs we set up here.
--
-- Permissions are scoped per role to match the design decision in
-- odd/tasks/postgres-storage.md:
--   pg-worker         -> INSERT, UPDATE, SELECT on all current and future
--                        tables in witsaba (DML on devices, no DDL).
--   pg-messaging-core -> SELECT only on all current and future tables in
--                        witsaba (read-only consumer).
--
-- We use ALTER DEFAULT PRIVILEGES so future tables created by pg-admin
-- automatically get the right GRANTs without re-running this script.

CREATE SCHEMA IF NOT EXISTS witsaba AUTHORIZATION "pg-admin";

GRANT USAGE ON SCHEMA witsaba TO "pg-worker";
GRANT USAGE ON SCHEMA witsaba TO "pg-messaging-core";

-- Future tables created by pg-admin in witsaba:
--   pg-worker gets DML (INSERT/UPDATE/DELETE/SELECT).
--   pg-messaging-core gets SELECT only.
ALTER DEFAULT PRIVILEGES FOR ROLE "pg-admin" IN SCHEMA witsaba
    GRANT INSERT, UPDATE, DELETE, SELECT ON TABLES TO "pg-worker";

ALTER DEFAULT PRIVILEGES FOR ROLE "pg-admin" IN SCHEMA witsaba
    GRANT SELECT ON TABLES TO "pg-messaging-core";

-- Future sequences owned by pg-admin in witsaba (for SERIAL/IDENTITY cols):
ALTER DEFAULT PRIVILEGES FOR ROLE "pg-admin" IN SCHEMA witsaba
    GRANT USAGE, SELECT ON SEQUENCES TO "pg-worker";

ALTER DEFAULT PRIVILEGES FOR ROLE "pg-admin" IN SCHEMA witsaba
    GRANT SELECT ON SEQUENCES TO "pg-messaging-core";
