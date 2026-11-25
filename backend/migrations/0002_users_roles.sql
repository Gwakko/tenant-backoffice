-- +goose Up
CREATE TABLE users (
  id             uuid PRIMARY KEY,
  email          text NOT NULL CHECK (email = lower(email)),
  password_hash  text NOT NULL,
  status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
  token_version  bigint NOT NULL DEFAULT 0,
  created_at     timestamptz NOT NULL
);
CREATE UNIQUE INDEX users_email ON users (email);

CREATE TABLE role_assignments (
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  node_id     uuid NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  role        text NOT NULL CHECK (role IN ('viewer','analyst','admin','owner')),
  granted_by  uuid NULL REFERENCES users(id),
  granted_at  timestamptz NOT NULL,
  PRIMARY KEY (user_id, node_id)
);
CREATE INDEX role_assignments_node ON role_assignments (node_id);

CREATE TABLE refresh_tokens (
  id          uuid PRIMARY KEY,
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  family_id   uuid NOT NULL,
  token_hash  bytea NOT NULL UNIQUE,           -- sha256 of the token
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz NULL,
  revoked_at  timestamptz NULL,
  created_at  timestamptz NOT NULL
);
CREATE INDEX refresh_tokens_family ON refresh_tokens (family_id);

-- +goose Down
DROP TABLE refresh_tokens;
DROP TABLE role_assignments;
DROP TABLE users;
