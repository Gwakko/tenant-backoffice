-- +goose Up

-- Trigger function that makes a table append-only. It also stops the table
-- owner (tb_owner is exempt from RLS), which grants alone cannot. The one
-- sanctioned way to remove such a table is the Down migration (DROP TABLE does
-- not fire row triggers). Shared by operations (here) and audit_log (0007).
-- +goose StatementBegin
CREATE FUNCTION tb_raise_immutable() RETURNS trigger
  LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp
  AS $$
BEGIN
  RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE = '55000';
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION tb_raise_immutable() FROM PUBLIC;

-- Money is bigint minor units of the one installation currency (no float, no
-- numeric, no currency column). Balance is never negative (INV-L2, EC-138).
CREATE TABLE balances (
  node_id    uuid        PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
  amount     bigint      NOT NULL DEFAULT 0 CHECK (amount >= 0),
  version    bigint      NOT NULL DEFAULT 0 CHECK (version >= 0),
  updated_at timestamptz NOT NULL
);

-- The ledger: append-only. A correction is a counter-operation.
CREATE TABLE operations (
  id                   uuid        PRIMARY KEY,
  node_id              uuid        NOT NULL REFERENCES nodes(id) ON DELETE RESTRICT,
  kind                 text        NOT NULL CHECK (kind IN ('credit', 'debit', 'transfer_in', 'transfer_out')),
  amount               bigint      NOT NULL CHECK (amount > 0),
  balance_after        bigint      NOT NULL CHECK (balance_after >= 0),
  transfer_id          uuid        NULL,
  counterparty_node_id uuid        NULL REFERENCES nodes(id) ON DELETE RESTRICT,
  comment              text        NOT NULL DEFAULT '' CHECK (char_length(comment) <= 200),
  actor_id             uuid        NOT NULL REFERENCES users(id),
  created_at           timestamptz NOT NULL,
  CHECK ((kind IN ('transfer_in', 'transfer_out')) = (transfer_id IS NOT NULL)),
  CHECK (counterparty_node_id IS DISTINCT FROM node_id)
);
CREATE INDEX operations_node_created ON operations (node_id, created_at DESC, id DESC);
CREATE INDEX operations_created ON operations (created_at DESC, id DESC);
CREATE INDEX operations_transfer ON operations (transfer_id) WHERE transfer_id IS NOT NULL;

CREATE TRIGGER operations_immutable BEFORE UPDATE OR DELETE ON operations
  FOR EACH ROW EXECUTE FUNCTION tb_raise_immutable();
CREATE TRIGGER operations_no_truncate BEFORE TRUNCATE ON operations
  FOR EACH STATEMENT EXECUTE FUNCTION tb_raise_immutable();

-- Row-level security: same model and same ENABLE-without-FORCE as 0003 (the
-- owner must stay exempt for migrations, seeding and SECURITY DEFINER
-- functions; tb_app is always subject to the policies). A row is visible and
-- writable when its node is visible through the nodes policy; without an actor
-- nothing is visible and every write is rejected with 42501.
ALTER TABLE balances ENABLE ROW LEVEL SECURITY;
CREATE POLICY balances_select ON balances FOR SELECT
  USING (node_id IN (SELECT id FROM nodes));
CREATE POLICY balances_insert ON balances FOR INSERT
  WITH CHECK (node_id IN (SELECT id FROM nodes));
CREATE POLICY balances_update ON balances FOR UPDATE
  USING (node_id IN (SELECT id FROM nodes))
  WITH CHECK (node_id IN (SELECT id FROM nodes));
-- No DELETE policy and no DELETE grant: a balance row goes away only with its
-- node (ON DELETE CASCADE runs with the owner's rights).

-- An operation is written for a visible node, as the acting user, and may name
-- only a visible counterparty: the policy check runs before the foreign key
-- check, so an invisible id cannot be probed through 23503 versus success.
-- There is no UPDATE or DELETE policy: the table is append-only.
ALTER TABLE operations ENABLE ROW LEVEL SECURITY;
CREATE POLICY operations_select ON operations FOR SELECT
  USING (node_id IN (SELECT id FROM nodes));
CREATE POLICY operations_insert ON operations FOR INSERT
  WITH CHECK (node_id IN (SELECT id FROM nodes)
              AND actor_id = (SELECT tb_actor_id())
              AND (counterparty_node_id IS NULL OR counterparty_node_id IN (SELECT id FROM nodes)));

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tb_app') THEN
    REVOKE ALL ON FUNCTION tb_raise_immutable() FROM tb_app;

    -- The balance of a node and its stamps change; the node never does.
    REVOKE ALL ON balances FROM tb_app;
    GRANT SELECT, INSERT ON balances TO tb_app;
    GRANT UPDATE (amount, version, updated_at) ON balances TO tb_app;

    REVOKE ALL ON operations FROM tb_app;
    GRANT SELECT, INSERT ON operations TO tb_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP POLICY operations_insert ON operations;
DROP POLICY operations_select ON operations;
DROP TABLE operations;
DROP POLICY balances_update ON balances;
DROP POLICY balances_insert ON balances;
DROP POLICY balances_select ON balances;
DROP TABLE balances;
DROP FUNCTION tb_raise_immutable();
