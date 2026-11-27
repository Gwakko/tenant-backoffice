//go:build integration

package postgres_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dset "github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/settings"
)

// settingsAudit records the audit entries of the settings use cases.
type settingsAudit struct {
	mu      sync.Mutex
	entries []port.AuditEntry
}

func (a *settingsAudit) Write(_ context.Context, _ port.Tx, e port.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *settingsAudit) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.entries)
}

type settingsCases struct {
	audit *settingsAudit
	repo  *postgres.SettingsRepo
	get   *uc.GetSettings
	set   *uc.SetSetting
	unset *uc.UnsetSetting
}

func (w *world) settingsCases() *settingsCases {
	a := &settingsAudit{}
	repo := postgres.NewSettingsRepo(slog.New(slog.NewTextHandler(io.Discard, nil)))
	d := uc.Deps{Runner: w.runner, Checker: w.checker, Settings: repo, Audit: a, Clock: realClock{}}
	return &settingsCases{audit: a, repo: repo, get: uc.NewGetSettings(d), set: uc.NewSetSetting(d), unset: uc.NewUnsetSetting(d)}
}

// seedSetting writes an override as the owner role, bypassing RLS and use cases.
func (w *world) seedSetting(nodeKey string, key dset.Key, jsonValue string) {
	w.t.Helper()
	_, err := w.db.Owner.Exec(context.Background(),
		`INSERT INTO node_settings (node_id, key, value, updated_at, updated_by) VALUES ($1, $2, $3::jsonb, now(), $4)`,
		uuid.UUID(w.id(nodeKey)), string(key), jsonValue, uuid.UUID(w.users["ownerRoot"]))
	require.NoError(w.t, err)
}

type storedSetting struct {
	value     string
	updatedBy uuid.UUID
	updatedAt time.Time
	found     bool
}

// storedSetting reads the row as the owner role.
func (w *world) storedSetting(nodeKey string, key dset.Key) storedSetting {
	w.t.Helper()
	var s storedSetting
	rows, err := w.db.Owner.Query(context.Background(),
		`SELECT value::text, updated_by, updated_at FROM node_settings WHERE node_id = $1 AND key = $2`,
		uuid.UUID(w.id(nodeKey)), string(key))
	require.NoError(w.t, err)
	defer rows.Close()
	if rows.Next() {
		s.found = true
		require.NoError(w.t, rows.Scan(&s.value, &s.updatedBy, &s.updatedAt))
	}
	require.NoError(w.t, rows.Err())
	return s
}

func (w *world) getSettings(c *settingsCases, who, nodeKey string) (map[dset.Key]uc.Item, error) {
	out, err := c.get.Execute(context.Background(), uc.GetSettingsInput{Actor: w.users[who], NodeID: w.id(nodeKey)})
	if err != nil {
		return nil, err
	}
	m := map[dset.Key]uc.Item{}
	for _, it := range out.Items {
		m[it.Key] = it
	}
	return m, nil
}

func (w *world) setSetting(c *settingsCases, who, nodeKey, key string, v uc.RawValue) (uc.SetSettingOutput, error) {
	return c.set.Execute(context.Background(), uc.SetSettingInput{
		Actor: w.users[who], RequestID: "t", NodeID: w.id(nodeKey), Key: key, Value: v,
	})
}

// Test-ID: T-I-46
func TestSettingsChainAndHiddenSource(t *testing.T) {
	w := newWorld(t)
	c := w.settingsCases()
	w.seedSetting("root", dset.DisplayTimezone, `"Asia/Tokyo"`)
	w.seedSetting("A", dset.OperationsEnabled, `false`)
	w.seedSetting("A", dset.MaxOperationAmount, `"1000"`)
	w.seedSetting("A1", dset.OperationsEnabled, `true`)
	w.seedSetting("A1", dset.MaxOperationAmount, `"5000"`)

	// EC-143: "multi" is admin on A1 only; root and A are above the access root.
	got, err := w.getSettings(c, "multi", "A1u1")
	require.NoError(t, err)
	tz := got[dset.DisplayTimezone]
	assert.Equal(t, "Asia/Tokyo", tz.Effective)
	assert.Nil(t, tz.SourceNodeID, "the id of the root must not reach the actor")
	assert.Nil(t, tz.Own)
	// EC-140, EC-141: the ancestors' restrictions apply, their ids stay hidden.
	assert.Equal(t, "false", got[dset.OperationsEnabled].Effective)
	assert.Nil(t, got[dset.OperationsEnabled].SourceNodeID)
	assert.Equal(t, "1000", got[dset.MaxOperationAmount].Effective)
	assert.Nil(t, got[dset.MaxOperationAmount].SourceNodeID)

	// The repository itself never holds a hidden id: A and root are nil, A1 is known.
	w.do("multi", func(ctx context.Context, tx port.Tx) {
		chain, cerr := c.repo.Chain(ctx, tx, w.id("A1u1"))
		require.NoError(t, cerr)
		require.Len(t, chain, 5)
		for _, e := range chain {
			if e.Depth < 3 {
				assert.Nil(t, e.NodeID, "depth %d is above the access root", e.Depth)
			} else {
				require.NotNil(t, e.NodeID)
				assert.Equal(t, w.id("A1"), *e.NodeID)
			}
		}
	})

	// Seen from above, the sources are the real nodes.
	got, err = w.getSettings(c, "ownerRoot", "A1u1")
	require.NoError(t, err)
	assert.Equal(t, w.id("root"), *got[dset.DisplayTimezone].SourceNodeID)
	assert.Equal(t, w.id("A"), *got[dset.OperationsEnabled].SourceNodeID)
	assert.Equal(t, w.id("A"), *got[dset.MaxOperationAmount].SourceNodeID)

	// The admin of A sees A but not the root.
	got, err = w.getSettings(c, "adminA", "A1")
	require.NoError(t, err)
	assert.Nil(t, got[dset.DisplayTimezone].SourceNodeID)
	assert.Equal(t, w.id("A"), *got[dset.OperationsEnabled].SourceNodeID)
	require.NotNil(t, got[dset.OperationsEnabled].Own)
	assert.Equal(t, "true", *got[dset.OperationsEnabled].Own)

	// Foreign branch and ancestors: not_found; a viewer reads.
	_, err = w.getSettings(c, "adminB", "A1")
	requireErrCode(t, err, apperr.NotFound)
	_, err = w.getSettings(c, "multi", "A")
	requireErrCode(t, err, apperr.NotFound)
	_, err = w.getSettings(c, "viewerA1u1", "A1u1")
	require.NoError(t, err)
}

// Test-ID: T-I-77
func TestSettingsWriteThroughUseCases(t *testing.T) {
	w := newWorld(t)
	c := w.settingsCases()
	w.seedSetting("A", dset.OperationsEnabled, `false`)
	w.seedSetting("A", dset.MaxOperationAmount, `"1000"`)

	// Types in the column follow the key; the actor is stamped from the session.
	out, err := w.setSetting(c, "adminA", "A1", "display_timezone", uc.RawValue{Kind: uc.KindString, Str: "Europe/Kyiv"})
	require.NoError(t, err)
	assert.Equal(t, "Europe/Kyiv", out.Item.Effective)
	assert.Equal(t, w.id("A1"), *out.Item.SourceNodeID)
	s := w.storedSetting("A1", dset.DisplayTimezone)
	assert.Equal(t, `"Europe/Kyiv"`, s.value)
	assert.Equal(t, uuid.UUID(w.users["adminA"]), s.updatedBy)
	assert.Equal(t, 1, c.audit.count())

	// EC-140, EC-141: a descendant cannot relax; the row is stored all the same.
	out, err = w.setSetting(c, "adminA", "A1", "operations_enabled", uc.RawValue{Kind: uc.KindBool, Bool: true})
	require.NoError(t, err)
	assert.Equal(t, "false", out.Item.Effective)
	assert.Equal(t, w.id("A"), *out.Item.SourceNodeID)
	assert.Equal(t, `true`, w.storedSetting("A1", dset.OperationsEnabled).value)
	out, err = w.setSetting(c, "adminA", "A1", "max_operation_amount", uc.RawValue{Kind: uc.KindString, Str: "5000"})
	require.NoError(t, err)
	assert.Equal(t, "1000", out.Item.Effective)
	assert.Equal(t, `"5000"`, w.storedSetting("A1", dset.MaxOperationAmount).value)
	assert.Equal(t, 3, c.audit.count())

	// EC-101: the same value again is no write (updated_at stays) and no audit.
	before := w.storedSetting("A1", dset.DisplayTimezone)
	_, err = w.setSetting(c, "adminA", "A1", "display_timezone", uc.RawValue{Kind: uc.KindString, Str: "Europe/Kyiv"})
	require.NoError(t, err)
	assert.Equal(t, before, w.storedSetting("A1", dset.DisplayTimezone))
	assert.Equal(t, 3, c.audit.count())

	// Invalid input and missing rights change nothing.
	_, err = w.setSetting(c, "adminA", "A1", "display_timezone", uc.RawValue{Kind: uc.KindString, Str: "Mars/Base"})
	requireErrCode(t, err, apperr.InvalidSettingValue)
	_, err = w.setSetting(c, "adminA", "A1", "foo", uc.RawValue{Kind: uc.KindString, Str: "x"})
	requireErrCode(t, err, apperr.UnknownSetting)
	_, err = w.setSetting(c, "viewerA1u1", "A1u1", "operations_enabled", uc.RawValue{Kind: uc.KindBool})
	requireErrCode(t, err, apperr.Forbidden)
	_, err = w.setSetting(c, "adminB", "A1", "operations_enabled", uc.RawValue{Kind: uc.KindBool})
	requireErrCode(t, err, apperr.NotFound)
	assert.False(t, w.storedSetting("A1u1", dset.OperationsEnabled).found)
	assert.Equal(t, 3, c.audit.count())

	// EC-142: unset falls back to the ancestor; a second unset is a no-op.
	uout, err := c.unset.Execute(context.Background(), uc.UnsetSettingInput{
		Actor: w.users["adminA"], RequestID: "t", NodeID: w.id("A1"), Key: "max_operation_amount",
	})
	require.NoError(t, err)
	assert.Nil(t, uout.Item.Own)
	assert.Equal(t, "1000", uout.Item.Effective)
	assert.Equal(t, w.id("A"), *uout.Item.SourceNodeID)
	assert.False(t, w.storedSetting("A1", dset.MaxOperationAmount).found)
	assert.Equal(t, 4, c.audit.count())
	_, err = c.unset.Execute(context.Background(), uc.UnsetSettingInput{
		Actor: w.users["adminA"], RequestID: "t", NodeID: w.id("A1"), Key: "max_operation_amount",
	})
	require.NoError(t, err)
	assert.Equal(t, 4, c.audit.count())
}

// Test-ID: T-I-78
// Two concurrent writers of the same new value: one inserts, the other sees the
// row and finds it equal, so exactly one audit entry is written.
func TestSettingsConcurrentSameValue(t *testing.T) {
	w := newWorld(t)
	c := w.settingsCases()
	const n = 8
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			<-start
			_, err := w.setSetting(c, "adminA", "A1", "display_timezone", uc.RawValue{Kind: uc.KindString, Str: "Asia/Tokyo"})
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, 1, c.audit.count())
	assert.Equal(t, `"Asia/Tokyo"`, w.storedSetting("A1", dset.DisplayTimezone).value)
}
