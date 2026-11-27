-- +goose Up

-- The audit journal: append-only. node_id has no foreign key on purpose: a
-- deleted node must not break or erase the journal (its entries just stop being
-- visible, because visibility goes through nodes).
CREATE TABLE audit_log (
  id          uuid        PRIMARY KEY,
  occurred_at timestamptz NOT NULL,
  actor_id    uuid        NOT NULL,
  node_id     uuid        NOT NULL,
  action      text        NOT NULL CHECK (char_length(action) BETWEEN 1 AND 64),
  target_type text        NOT NULL CHECK (char_length(target_type) BETWEEN 1 AND 64),
  target_id   uuid        NOT NULL,
  before      jsonb       NULL,
  after       jsonb       NULL,
  request_id  text        NOT NULL
);
CREATE INDEX audit_node_time ON audit_log (node_id, occurred_at DESC, id DESC);
CREATE INDEX audit_time ON audit_log (occurred_at DESC, id DESC);

-- Two independent barriers against rewriting history: tb_app has neither the
-- UPDATE nor the DELETE nor the TRUNCATE grant (see below), and this trigger
-- stops everybody else, the owner included (SQLSTATE 55000). Removing the
-- table is the job of the Down migration only (DROP TABLE fires no row trigger).
CREATE TRIGGER audit_log_immutable BEFORE UPDATE OR DELETE ON audit_log
  FOR EACH ROW EXECUTE FUNCTION tb_raise_immutable();
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON audit_log
  FOR EACH STATEMENT EXECUTE FUNCTION tb_raise_immutable();

-- Row-level security: same model as 0003, ENABLE without FORCE (the owner is
-- exempt for migrations and seeding; the trigger above covers the owner). An
-- entry is visible when its node is visible, and may be written only for a
-- visible node and only as the acting user. There is no UPDATE or DELETE policy.
ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
CREATE POLICY audit_log_select ON audit_log FOR SELECT
  USING (node_id IN (SELECT id FROM nodes));
CREATE POLICY audit_log_insert ON audit_log FOR INSERT
  WITH CHECK (node_id IN (SELECT id FROM nodes) AND actor_id = (SELECT tb_actor_id()));

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tb_app') THEN
    REVOKE ALL ON audit_log FROM tb_app;
    GRANT SELECT, INSERT ON audit_log TO tb_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP POLICY audit_log_insert ON audit_log;
DROP POLICY audit_log_select ON audit_log;
DROP TABLE audit_log;
