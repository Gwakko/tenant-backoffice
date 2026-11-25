-- +goose Up
CREATE EXTENSION IF NOT EXISTS ltree;

CREATE FUNCTION tb_label(id uuid) RETURNS text
  LANGUAGE sql IMMUTABLE STRICT AS $$ SELECT replace(id::text, '-', '') $$;

CREATE FUNCTION tb_type_level(t text) RETURNS int
  LANGUAGE sql IMMUTABLE STRICT AS $$
  SELECT CASE t WHEN 'platform' THEN 1 WHEN 'partner' THEN 2
                WHEN 'company' THEN 3 WHEN 'unit' THEN 4 END $$;

CREATE TABLE nodes (
  id          uuid PRIMARY KEY,
  type        text NOT NULL CHECK (type IN ('platform','partner','company','unit')),
  parent_id   uuid NULL REFERENCES nodes(id) ON DELETE RESTRICT,
  path        ltree NOT NULL UNIQUE,
  name        text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
  name_key    text NOT NULL,
  status      text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
  created_at  timestamptz NOT NULL,
  updated_at  timestamptz NOT NULL,
  CHECK (nlevel(path) = tb_type_level(type)),
  CHECK ((type = 'platform') = (parent_id IS NULL)),
  CHECK (subpath(path, -1)::text = tb_label(id))
);
CREATE UNIQUE INDEX nodes_single_platform ON nodes ((true)) WHERE type = 'platform';
CREATE UNIQUE INDEX nodes_parent_name_key ON nodes (parent_id, name_key);
CREATE INDEX nodes_path_gist ON nodes USING GIST (path);
CREATE INDEX nodes_parent_id ON nodes (parent_id, name_key, id);

-- Second line of defence behind the domain: the parent must be one level up and
-- the path must equal the parent path plus the own label.
-- When an UPDATE keeps parent_id (subtree move rewrites descendant paths), the
-- parent row is read from the statement snapshot with its old path, so the
-- path comparison is skipped; tb_tree_violations() covers that case.
-- +goose StatementBegin
CREATE FUNCTION tb_nodes_parent_type_check() RETURNS trigger
  LANGUAGE plpgsql AS $$
DECLARE
  p nodes%ROWTYPE;
BEGIN
  IF NEW.parent_id IS NULL THEN
    RETURN NEW;
  END IF;
  SELECT * INTO p FROM nodes WHERE id = NEW.parent_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'node % has unknown parent %', NEW.id, NEW.parent_id
      USING ERRCODE = 'check_violation';
  END IF;
  IF tb_type_level(p.type) <> tb_type_level(NEW.type) - 1 THEN
    RAISE EXCEPTION 'node type % is not allowed under parent type %', NEW.type, p.type
      USING ERRCODE = 'check_violation';
  END IF;
  IF TG_OP = 'INSERT' OR NEW.parent_id IS DISTINCT FROM OLD.parent_id THEN
    IF subpath(NEW.path, 0, -1) <> p.path THEN
      RAISE EXCEPTION 'node % path % does not extend parent path %', NEW.id, NEW.path, p.path
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER nodes_parent_type_check
  BEFORE INSERT OR UPDATE OF parent_id, path ON nodes
  FOR EACH ROW EXECUTE FUNCTION tb_nodes_parent_type_check();

-- Test helper: rows whose path or level disagrees with their parent or type.
-- +goose StatementBegin
CREATE FUNCTION tb_tree_violations() RETURNS TABLE(node_id uuid, problem text)
  LANGUAGE sql STABLE AS $$
  SELECT n.id, 'level_mismatch'
    FROM nodes n
   WHERE nlevel(n.path) <> tb_type_level(n.type)
  UNION ALL
  SELECT n.id, 'path_mismatch'
    FROM nodes n
    JOIN nodes p ON p.id = n.parent_id
   WHERE n.path <> p.path || subpath(n.path, -1)
  UNION ALL
  SELECT n.id, 'parent_type_mismatch'
    FROM nodes n
    JOIN nodes p ON p.id = n.parent_id
   WHERE tb_type_level(p.type) <> tb_type_level(n.type) - 1
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION tb_tree_violations();
DROP TABLE nodes;
DROP FUNCTION tb_nodes_parent_type_check();
DROP FUNCTION tb_type_level(text);
DROP FUNCTION tb_label(uuid);
DROP EXTENSION ltree;
