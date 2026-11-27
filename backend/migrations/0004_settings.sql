-- +goose Up

-- Explicitly set node settings. Only the override of a node is stored; the
-- effective value is resolved in the domain over the chain root -> node.
--
-- The value type is pinned per key so that a bad value cannot reach the
-- resolver even if a caller skips domain validation (the domain still checks
-- ranges and the IANA zone, which SQL cannot).
CREATE TABLE node_settings (
  node_id    uuid        NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  key        text        NOT NULL CHECK (key IN ('display_timezone', 'operations_enabled', 'max_operation_amount')),
  value      jsonb       NOT NULL,
  updated_at timestamptz NOT NULL,
  updated_by uuid        NOT NULL REFERENCES users(id),
  PRIMARY KEY (node_id, key),
  CHECK (CASE key
           WHEN 'display_timezone'     THEN jsonb_typeof(value) = 'string'
                                            AND char_length(value #>> '{}') BETWEEN 1 AND 64
           WHEN 'operations_enabled'   THEN jsonb_typeof(value) = 'boolean'
           WHEN 'max_operation_amount' THEN jsonb_typeof(value) = 'string'
                                            AND (value #>> '{}') ~ '^[1-9][0-9]{0,18}$'
         END)
);

-- Row-level security, same model as 0003: the actor is set per transaction,
-- visibility of a row follows the visibility of its node (the subquery on nodes
-- is itself filtered by the nodes policy), and without an actor every policy
-- rejects. RLS is ENABLEd and deliberately not FORCEd, exactly as in 0003: the
-- owner tb_owner stays exempt because migrations, seeding and the SECURITY
-- DEFINER function tb_settings_chain below must read rows hidden from tb_app.
-- tb_app is subject to the policies on every statement.
--
-- updated_by must be the acting user: an actor cannot attribute a change to
-- somebody else. (RLS still does not check role x action, see spec 02 5.2.)
ALTER TABLE node_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY node_settings_select ON node_settings FOR SELECT
  USING (node_id IN (SELECT id FROM nodes));
CREATE POLICY node_settings_insert ON node_settings FOR INSERT
  WITH CHECK (node_id IN (SELECT id FROM nodes) AND updated_by = (SELECT tb_actor_id()));
CREATE POLICY node_settings_update ON node_settings FOR UPDATE
  USING (node_id IN (SELECT id FROM nodes))
  WITH CHECK (node_id IN (SELECT id FROM nodes) AND updated_by = (SELECT tb_actor_id()));
CREATE POLICY node_settings_delete ON node_settings FOR DELETE
  USING (node_id IN (SELECT id FROM nodes));

-- The settings of the chain root -> node, for resolving an effective value.
-- Ancestors above the actor's access root are hidden from tb_app by RLS, but
-- the effective value depends on them (an ancestor may forbid operations or cap
-- the amount), so this function reads node_settings and nodes as the owner.
--
-- Nothing is returned unless the node itself is inside one of the actor's
-- subtrees (an unknown, foreign or invisible node and a missing actor all give
-- an empty result, indistinguishable from each other). For an ancestor the
-- actor cannot see, source_node_id is NULL: the id of a foreign node never
-- leaves the database; only the key, the value and the depth do. updated_by and
-- updated_at are not returned. No identifier of the actor is an argument.
CREATE FUNCTION tb_settings_chain(p_node_id uuid)
  RETURNS TABLE (source_node_id uuid, depth int, key text, value jsonb)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public, pg_temp
  AS $$
  WITH scope AS (SELECT tb_actor_scopes() AS paths),
       target AS (
         SELECT t.path
           FROM nodes t, scope
          WHERE t.id = p_node_id AND t.path <@ scope.paths)
  SELECT CASE WHEN a.path <@ (SELECT paths FROM scope) THEN a.id END,
         nlevel(a.path)::int, ns.key, ns.value
    FROM target t
    JOIN nodes a ON a.path @> t.path
    JOIN node_settings ns ON ns.node_id = a.id
   ORDER BY nlevel(a.path), ns.key $$;

REVOKE ALL ON FUNCTION tb_settings_chain(uuid) FROM PUBLIC;

-- Grants (skipped when tb_app does not exist yet, as in 0003). The default
-- privileges of roles.sql grant everything on new tables, so reset them first.
-- The row identity (node_id, key) never changes: only value and its stamps are
-- updatable.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tb_app') THEN
    GRANT EXECUTE ON FUNCTION tb_settings_chain(uuid) TO tb_app;

    REVOKE ALL ON node_settings FROM tb_app;
    GRANT SELECT, INSERT, DELETE ON node_settings TO tb_app;
    GRANT UPDATE (value, updated_at, updated_by) ON node_settings TO tb_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION tb_settings_chain(uuid);
DROP POLICY node_settings_delete ON node_settings;
DROP POLICY node_settings_update ON node_settings;
DROP POLICY node_settings_insert ON node_settings;
DROP POLICY node_settings_select ON node_settings;
DROP TABLE node_settings;
