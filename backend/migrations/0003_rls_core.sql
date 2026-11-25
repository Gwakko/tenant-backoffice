-- +goose Up

-- Row-level security for the tenant tables of migrations 0001 and 0002.
--
-- Model: the API runs as tb_app (no BYPASSRLS, owns nothing) and sets the actor
-- per transaction with set_config('app.actor_id', <uuid>, true). Without an
-- actor tb_actor_id() is NULL, tb_actor_scopes() is empty and every policy
-- below rejects: reads return no rows, writes fail with SQLSTATE 42501.
--
-- RLS is ENABLEd, deliberately not FORCEd: the table owner tb_owner is exempt,
-- which migrations, seeding and the SECURITY DEFINER functions below rely on.
-- tb_app is subject to the policies on every statement.
--
-- Every SECURITY DEFINER function: owner tb_owner (the migration role), a fixed
-- search_path that lists pg_catalog first and pg_temp last, no dynamic SQL,
-- EXECUTE revoked from PUBLIC and granted to tb_app only.

-- Current actor of the transaction, NULL when none was set.
CREATE FUNCTION tb_actor_id() RETURNS uuid
  LANGUAGE sql STABLE
  SET search_path = pg_catalog, pg_temp
  AS $$ SELECT nullif(current_setting('app.actor_id', true), '')::uuid $$;

-- Paths of the nodes where the actor holds a role (active actor only). The
-- policies treat "path <@ scopes" as "node is in one of the actor's subtrees".
-- Reads role_assignments, nodes and users as the owner (bypassing their RLS) to
-- avoid policy recursion; it returns only paths of the actor's own assignments.
CREATE FUNCTION tb_actor_scopes() RETURNS ltree[]
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public, pg_temp
  AS $$
  SELECT coalesce(array_agg(n.path), '{}'::ltree[])
    FROM role_assignments ra
    JOIN nodes n ON n.id = ra.node_id
    JOIN users u ON u.id = ra.user_id AND u.status = 'active'
   WHERE ra.user_id = tb_actor_id() $$;

-- Authentication before an actor exists (login, token check). Returns the row
-- of exactly one user selected by email.
CREATE FUNCTION tb_auth_user_by_email(p_email text)
  RETURNS TABLE (id uuid, password_hash text, status text, token_version bigint)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public, pg_temp
  AS $$
  SELECT u.id, u.password_hash, u.status, u.token_version
    FROM users u
   WHERE u.email = lower(p_email) $$;

-- Same, selected by user id (the verified subject of an access token). Token
-- refresh needs no credential, so the password hash is deliberately not returned.
CREATE FUNCTION tb_auth_user_by_id(p_id uuid)
  RETURNS TABLE (id uuid, status text, token_version bigint)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public, pg_temp
  AS $$
  SELECT u.id, u.status, u.token_version
    FROM users u
   WHERE u.id = p_id $$;

-- Id of a user by email, also for users not yet visible through RLS (adding a
-- member who belongs to another branch). Only an actor that holds a role
-- somewhere gets an answer; otherwise NULL.
CREATE FUNCTION tb_user_id_by_email(p_email text) RETURNS uuid
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public, pg_temp
  AS $$
  SELECT u.id
    FROM users u
   WHERE u.email = lower(p_email)
     AND cardinality(tb_actor_scopes()) > 0 $$;

-- Changes the password hash of the acting user only: the row is picked by
-- tb_actor_id() and there is no identifier argument. Without an actor nothing
-- is updated. tb_app has no UPDATE on users.password_hash, so this function is
-- the only way to change a hash after creation.
CREATE FUNCTION tb_set_own_password(p_hash text) RETURNS void
  LANGUAGE sql VOLATILE SECURITY DEFINER
  SET search_path = pg_catalog, public, pg_temp
  AS $$
  UPDATE users SET password_hash = p_hash WHERE id = tb_actor_id() $$;

-- Effective activity of a node: its own status and that of every ancestor, also
-- those above the actor's access root (hidden by RLS). NULL when the node is
-- not visible to the actor, so the answer never confirms a foreign node.
CREATE FUNCTION tb_node_effective_active(p_node_id uuid) RETURNS boolean
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public, pg_temp
  AS $$
  SELECT (SELECT bool_and(a.status = 'active') FROM nodes a WHERE a.path @> t.path)
    FROM nodes t
   WHERE t.id = p_node_id
     AND t.path <@ tb_actor_scopes() $$;

REVOKE ALL ON FUNCTION tb_actor_id() FROM PUBLIC;
REVOKE ALL ON FUNCTION tb_actor_scopes() FROM PUBLIC;
REVOKE ALL ON FUNCTION tb_auth_user_by_email(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION tb_auth_user_by_id(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION tb_user_id_by_email(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION tb_set_own_password(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION tb_node_effective_active(uuid) FROM PUBLIC;

-- nodes: a row is visible and writable when its path lies in an actor subtree.
ALTER TABLE nodes ENABLE ROW LEVEL SECURITY;
CREATE POLICY nodes_select ON nodes FOR SELECT
  USING (path <@ (SELECT tb_actor_scopes()));
CREATE POLICY nodes_insert ON nodes FOR INSERT
  WITH CHECK (path <@ (SELECT tb_actor_scopes()));
-- The new row is also checked against nodes_select by PostgreSQL, so WITH CHECK
-- here is a second, explicit barrier against moving a node out of the branch.
CREATE POLICY nodes_update ON nodes FOR UPDATE
  USING (path <@ (SELECT tb_actor_scopes()))
  WITH CHECK (path <@ (SELECT tb_actor_scopes()));
CREATE POLICY nodes_delete ON nodes FOR DELETE
  USING (path <@ (SELECT tb_actor_scopes()));

-- role_assignments: visibility follows the node; an active actor also sees own
-- rows (an inactive actor has no scopes, so it sees none). Writes require a
-- visible node. The subquery on nodes is itself filtered by the nodes policy.
ALTER TABLE role_assignments ENABLE ROW LEVEL SECURITY;
CREATE POLICY role_assignments_select ON role_assignments FOR SELECT
  USING (node_id IN (SELECT id FROM nodes)
         OR (user_id = tb_actor_id() AND cardinality((SELECT tb_actor_scopes())) > 0));
CREATE POLICY role_assignments_insert ON role_assignments FOR INSERT
  WITH CHECK (node_id IN (SELECT id FROM nodes));
CREATE POLICY role_assignments_update ON role_assignments FOR UPDATE
  USING (node_id IN (SELECT id FROM nodes))
  WITH CHECK (node_id IN (SELECT id FROM nodes));
CREATE POLICY role_assignments_delete ON role_assignments FOR DELETE
  USING (node_id IN (SELECT id FROM nodes));

-- users: the actor and users that hold a visible assignment. A new user has no
-- assignment yet and stays invisible until one is added, so INSERT ... RETURNING
-- is rejected by the SELECT policy: callers generate the id and do not use
-- RETURNING. Creating users needs an actor that holds a role somewhere (the
-- spec's plain "true" would let a session without actor create users).
-- There is no DELETE policy and no DELETE grant.
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
CREATE POLICY users_select ON users FOR SELECT
  USING (id = tb_actor_id() OR id IN (SELECT user_id FROM role_assignments));
CREATE POLICY users_insert ON users FOR INSERT
  WITH CHECK (cardinality((SELECT tb_actor_scopes())) > 0);
CREATE POLICY users_update ON users FOR UPDATE
  USING (id = tb_actor_id()
         OR id IN (SELECT user_id FROM role_assignments
                    WHERE node_id IN (SELECT id FROM nodes)))
  WITH CHECK (id = tb_actor_id()
         OR id IN (SELECT user_id FROM role_assignments
                    WHERE node_id IN (SELECT id FROM nodes)));

-- Grants (skipped when the application role does not exist yet, as on a bare
-- server in the migration roundtrip; roles.sql and default privileges cover it).
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tb_app') THEN
    GRANT EXECUTE ON FUNCTION tb_actor_id() TO tb_app;
    GRANT EXECUTE ON FUNCTION tb_actor_scopes() TO tb_app;
    GRANT EXECUTE ON FUNCTION tb_auth_user_by_email(text) TO tb_app;
    GRANT EXECUTE ON FUNCTION tb_auth_user_by_id(uuid) TO tb_app;
    GRANT EXECUTE ON FUNCTION tb_user_id_by_email(text) TO tb_app;
    GRANT EXECUTE ON FUNCTION tb_set_own_password(text) TO tb_app;
    GRANT EXECUTE ON FUNCTION tb_node_effective_active(uuid) TO tb_app;

    GRANT SELECT, INSERT, UPDATE, DELETE ON nodes TO tb_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON role_assignments TO tb_app;

    -- users: the password hash is never readable through SQL (login reads it
    -- through tb_auth_user_by_email) and never updatable directly (own password
    -- through tb_set_own_password); only status and token_version are updatable;
    -- no DELETE.
    REVOKE ALL ON users FROM tb_app;
    GRANT SELECT (id, email, status, token_version, created_at) ON users TO tb_app;
    GRANT INSERT ON users TO tb_app;
    GRANT UPDATE (status, token_version) ON users TO tb_app;

    -- refresh_tokens: no tenant data, no RLS; only the state columns change.
    REVOKE ALL ON refresh_tokens FROM tb_app;
    GRANT SELECT, INSERT, DELETE ON refresh_tokens TO tb_app;
    GRANT UPDATE (used_at, revoked_at) ON refresh_tokens TO tb_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP POLICY users_update ON users;
DROP POLICY users_insert ON users;
DROP POLICY users_select ON users;
ALTER TABLE users DISABLE ROW LEVEL SECURITY;

DROP POLICY role_assignments_delete ON role_assignments;
DROP POLICY role_assignments_update ON role_assignments;
DROP POLICY role_assignments_insert ON role_assignments;
DROP POLICY role_assignments_select ON role_assignments;
ALTER TABLE role_assignments DISABLE ROW LEVEL SECURITY;

DROP POLICY nodes_delete ON nodes;
DROP POLICY nodes_update ON nodes;
DROP POLICY nodes_insert ON nodes;
DROP POLICY nodes_select ON nodes;
ALTER TABLE nodes DISABLE ROW LEVEL SECURITY;

DROP FUNCTION tb_node_effective_active(uuid);
DROP FUNCTION tb_set_own_password(text);
DROP FUNCTION tb_user_id_by_email(text);
DROP FUNCTION tb_auth_user_by_id(uuid);
DROP FUNCTION tb_auth_user_by_email(text);
DROP FUNCTION tb_actor_scopes();
DROP FUNCTION tb_actor_id();

-- Back to the privileges the default privileges of roles.sql give.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tb_app') THEN
    REVOKE ALL ON users, refresh_tokens FROM tb_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON users, refresh_tokens TO tb_app;
  END IF;
END $$;
-- +goose StatementEnd
