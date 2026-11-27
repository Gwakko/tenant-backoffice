-- +goose Up

-- Idempotency keys of balance operations. A key belongs to the acting user: the
-- primary key is (user_id, key), so two users may use the same key text for
-- independent operations and one user's key can never collide with, or be
-- looked up through, another's.
--
-- Concurrency: the unique index serializes duplicates. A second transaction
-- that inserts the same (user_id, key) waits for the first to finish and then
-- sees the conflict (INSERT ... ON CONFLICT DO NOTHING), so a parallel duplicate
-- cannot create a second operation.
--
-- request_hash is sha256 over the request (method, route, params, canonical
-- body): the same key with another request is detected by the caller.
CREATE TABLE idempotency_keys (
  user_id      uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key          text        NOT NULL CHECK (char_length(key) BETWEEN 1 AND 128 AND key ~ '^[A-Za-z0-9_:.-]+$'),
  request_hash bytea       NOT NULL CHECK (octet_length(request_hash) = 32),
  operation_id uuid        NULL,   -- credit, debit
  transfer_id  uuid        NULL,   -- transfer
  created_at   timestamptz NOT NULL,
  expires_at   timestamptz NOT NULL,
  PRIMARY KEY (user_id, key),
  CHECK ((operation_id IS NULL) <> (transfer_id IS NULL)),
  CHECK (expires_at > created_at)
);
CREATE INDEX idempotency_keys_expires ON idempotency_keys (expires_at);

-- Row-level security: a row belongs to its user only, and the actor must be an
-- active user with a role somewhere (the same condition as users_insert in
-- 0003), so a session without an actor, an unknown actor and an inactive one
-- see nothing and write nothing. The scope is by actor, not by node: the
-- stored ids (operation_id, transfer_id) are the actor's own earlier results.
-- ENABLE without FORCE as in 0003 (the owner purges expired keys).
ALTER TABLE idempotency_keys ENABLE ROW LEVEL SECURITY;
CREATE POLICY idempotency_keys_select ON idempotency_keys FOR SELECT
  USING (user_id = (SELECT tb_actor_id()) AND cardinality((SELECT tb_actor_scopes())) > 0);
CREATE POLICY idempotency_keys_insert ON idempotency_keys FOR INSERT
  WITH CHECK (user_id = (SELECT tb_actor_id()) AND cardinality((SELECT tb_actor_scopes())) > 0);
CREATE POLICY idempotency_keys_delete ON idempotency_keys FOR DELETE
  USING (user_id = (SELECT tb_actor_id()) AND cardinality((SELECT tb_actor_scopes())) > 0);
-- No UPDATE policy and no UPDATE grant: a stored key is immutable, so its
-- request_hash cannot be rewritten to make a different request replay it. An
-- expired key is deleted and inserted again.

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tb_app') THEN
    REVOKE ALL ON idempotency_keys FROM tb_app;
    GRANT SELECT, INSERT, DELETE ON idempotency_keys TO tb_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP POLICY idempotency_keys_delete ON idempotency_keys;
DROP POLICY idempotency_keys_insert ON idempotency_keys;
DROP POLICY idempotency_keys_select ON idempotency_keys;
DROP TABLE idempotency_keys;
