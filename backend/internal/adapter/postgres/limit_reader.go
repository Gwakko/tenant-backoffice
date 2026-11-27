package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// LimitReader implements port.LimitSource on tb_settings_chain, which also
// reports settings of ancestors the actor cannot see (their ids come back as
// NULL). Values are parsed and resolved by domain/settings.
type LimitReader struct{ log *slog.Logger }

var _ port.LimitSource = (*LimitReader)(nil)

// NewLimitReader builds the reader; log receives rejected statements.
func NewLimitReader(log *slog.Logger) *LimitReader { return &LimitReader{log: log} }

// EffectiveLimits implements port.LimitSource. A node with no settings in its
// chain (or one the actor cannot see; the caller has checked visibility) gets
// the defaults.
func (r *LimitReader) EffectiveLimits(ctx context.Context, tx port.Tx, id node.ID) (port.Limits, error) {
	t, err := unwrap(tx)
	if err != nil {
		return port.Limits{}, err
	}
	rows, err := t.Query(ctx, `SELECT source_node_id, depth, key, value FROM tb_settings_chain($1)`, uuid.UUID(id))
	if err != nil {
		return port.Limits{}, mapError(r.log, "settings chain", "node", err)
	}
	defer rows.Close()
	var chain []settings.NodeSettings
	lastDepth := -1
	for rows.Next() {
		var src *uuid.UUID
		var depth int
		var key string
		var raw []byte
		if err := rows.Scan(&src, &depth, &key, &raw); err != nil {
			return port.Limits{}, mapError(r.log, "settings chain", "node", err)
		}
		if depth != lastDepth {
			chain = append(chain, settings.NodeSettings{})
			lastDepth = depth
		}
		cur := &chain[len(chain)-1]
		if src != nil {
			cur.NodeID = node.ID(*src)
		}
		text, err := settingText(raw)
		if err != nil {
			return port.Limits{}, fmt.Errorf("postgres: setting %s: %w", key, err)
		}
		k, err := settings.ParseKey(key)
		if err != nil {
			return port.Limits{}, fmt.Errorf("postgres: setting %s: %w", key, err)
		}
		next, err := cur.Set(k, text)
		if err != nil {
			return port.Limits{}, fmt.Errorf("postgres: stored setting %s is invalid: %w", key, err)
		}
		*cur = next
	}
	if err := rows.Err(); err != nil {
		return port.Limits{}, mapError(r.log, "settings chain", "node", err)
	}
	eff := settings.Resolve(chain)
	return port.Limits{OperationsEnabled: eff.OperationsEnabled.Value, MaxOperationAmount: eff.MaxOperationAmount.Value}, nil
}

// settingText turns a jsonb value (boolean or string) into the raw text the
// domain parses.
func settingText(raw []byte) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	default:
		return "", fmt.Errorf("unexpected json type %T", v)
	}
}
