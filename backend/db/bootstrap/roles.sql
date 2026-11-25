-- Database roles and the application database. Run as a superuser:
--   psql -v ON_ERROR_STOP=1 -v owner_password=... -v app_password=... -v db_name=... -f roles.sql
-- Idempotent: safe to run repeatedly; passwords are reset on each run.

SELECT format('CREATE ROLE tb_owner LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE PASSWORD %L', :'owner_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tb_owner') \gexec

SELECT format('CREATE ROLE tb_app LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE PASSWORD %L', :'app_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tb_app') \gexec

SELECT format('ALTER ROLE tb_owner LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE PASSWORD %L', :'owner_password') \gexec
SELECT format('ALTER ROLE tb_app LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE PASSWORD %L', :'app_password') \gexec

SELECT format('CREATE DATABASE %I OWNER tb_owner', :'db_name')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'db_name') \gexec

SELECT format('ALTER DATABASE %I OWNER TO tb_owner', :'db_name') \gexec
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', :'db_name') \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO tb_app', :'db_name') \gexec

\connect :db_name

-- Tables created by tb_owner (migrations) are usable by tb_app; RLS narrows rows.
GRANT USAGE ON SCHEMA public TO tb_app;
ALTER DEFAULT PRIVILEGES FOR ROLE tb_owner IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO tb_app;
ALTER DEFAULT PRIVILEGES FOR ROLE tb_owner IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO tb_app;
ALTER DEFAULT PRIVILEGES FOR ROLE tb_owner IN SCHEMA public
    GRANT EXECUTE ON FUNCTIONS TO tb_app;
