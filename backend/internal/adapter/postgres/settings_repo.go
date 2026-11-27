package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

const (
	resourceSetting = "setting"

	// tb_settings_chain returns source_node_id = NULL for an ancestor above the
	// actor's access root; the id is never read from anywhere else.
	selectSettingsChain = `SELECT source_node_id, depth, key, value FROM tb_settings_chain($1)`

	selectSettingForUpdate = `SELECT value FROM node_settings WHERE node_id = $1 AND key = $2 FOR UPDATE`

	insertSetting = `INSERT INTO node_settings (node_id, key, value, updated_at, updated_by)
VALUES ($1, $2, $3, $4, (SELECT tb_actor_id()))
ON CONFLICT (node_id, key) DO NOTHING`

	updateSetting = `UPDATE node_settings SET value = $3, updated_at = $4, updated_by = (SELECT tb_actor_id())
 WHERE node_id = $1 AND key = $2`

	deleteSetting = `DELETE FROM node_settings WHERE node_id = $1 AND key = $2 RETURNING value`
)

// SettingsRepo implements port.SettingsRepo.
type SettingsRepo struct{ log *slog.Logger }

var _ port.SettingsRepo = (*SettingsRepo)(nil)

// NewSettingsRepo builds the repository.
func NewSettingsRepo(log *slog.Logger) *SettingsRepo { return &SettingsRepo{log: log} }

// The value column holds a JSON boolean for operations_enabled and a JSON
// string for the other keys (a CHECK in 0004 pins this).
func encodeValue(key settings.Key, raw string) ([]byte, error) {
	if key == settings.OperationsEnabled {
		b, err := settings.ParseBool(raw)
		if err != nil {
			return nil, err
		}
		return json.Marshal(b)
	}
	return json.Marshal(raw)
}

func decodeValue(key settings.Key, data []byte) (string, error) {
	if key == settings.OperationsEnabled {
		var b bool
		if err := json.Unmarshal(data, &b); err != nil {
			return "", fmt.Errorf("postgres: setting %s is not a boolean: %w", key, err)
		}
		if b {
			return "true", nil
		}
		return "false", nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return "", fmt.Errorf("postgres: setting %s is not a string: %w", key, err)
	}
	return s, nil
}

// Chain implements port.SettingsRepo.
func (r *SettingsRepo) Chain(ctx context.Context, tx port.Tx, nodeID node.ID) ([]port.SettingEntry, error) {
	t, err := unwrap(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, selectSettingsChain, uuid.UUID(nodeID))
	if err != nil {
		return nil, mapError(r.log, "settings chain", resourceSetting, err)
	}
	defer rows.Close()
	var out []port.SettingEntry
	for rows.Next() {
		var (
			src   *uuid.UUID
			depth int32
			key   string
			value []byte
		)
		if err := rows.Scan(&src, &depth, &key, &value); err != nil {
			return nil, mapError(r.log, "scan settings chain", resourceSetting, err)
		}
		k, err := settings.ParseKey(key)
		if err != nil {
			return nil, fmt.Errorf("postgres: stored setting key %q: %w", key, err)
		}
		raw, err := decodeValue(k, value)
		if err != nil {
			return nil, err
		}
		e := port.SettingEntry{Depth: int(depth), Key: k, Value: raw}
		if src != nil {
			id := node.ID(*src)
			e.NodeID = &id
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(r.log, "read settings chain", resourceSetting, err)
	}
	return out, nil
}

// Set implements port.SettingsRepo. The row is read FOR UPDATE; a missing one
// is inserted with ON CONFLICT DO NOTHING, and if a concurrent insert won, the
// loop reads the winner's row (locked) and compares again.
func (r *SettingsRepo) Set(ctx context.Context, tx port.Tx, nodeID node.ID, key settings.Key, value string, at time.Time) (port.SettingWrite, error) {
	t, err := unwrap(tx)
	if err != nil {
		return port.SettingWrite{}, err
	}
	enc, err := encodeValue(key, value)
	if err != nil {
		return port.SettingWrite{}, err
	}
	for range 3 {
		var cur []byte
		err := t.QueryRow(ctx, selectSettingForUpdate, uuid.UUID(nodeID), string(key)).Scan(&cur)
		switch {
		case err == nil:
			before, derr := decodeValue(key, cur)
			if derr != nil {
				return port.SettingWrite{}, derr
			}
			w := port.SettingWrite{Before: before, HadBefore: true}
			if before == value {
				return w, nil
			}
			if _, err := t.Exec(ctx, updateSetting, uuid.UUID(nodeID), string(key), enc, at); err != nil {
				return port.SettingWrite{}, mapError(r.log, "update setting", resourceSetting, err)
			}
			w.Changed = true
			return w, nil
		case errors.Is(err, pgx.ErrNoRows):
			tag, err := t.Exec(ctx, insertSetting, uuid.UUID(nodeID), string(key), enc, at)
			if err != nil {
				return port.SettingWrite{}, mapError(r.log, "insert setting", resourceSetting, err)
			}
			if tag.RowsAffected() == 1 {
				return port.SettingWrite{Changed: true}, nil
			}
		default:
			return port.SettingWrite{}, mapError(r.log, "get setting", resourceSetting, err)
		}
	}
	return port.SettingWrite{}, fmt.Errorf("postgres: setting %s on %s kept changing under the transaction", key, nodeID)
}

// Unset implements port.SettingsRepo.
func (r *SettingsRepo) Unset(ctx context.Context, tx port.Tx, nodeID node.ID, key settings.Key) (string, bool, error) {
	t, err := unwrap(tx)
	if err != nil {
		return "", false, err
	}
	var cur []byte
	err = t.QueryRow(ctx, deleteSetting, uuid.UUID(nodeID), string(key)).Scan(&cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, mapError(r.log, "delete setting", resourceSetting, err)
	}
	before, err := decodeValue(key, cur)
	if err != nil {
		return "", false, err
	}
	return before, true, nil
}
