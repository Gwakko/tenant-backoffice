package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/authn"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/seed"
)

// The seed writes the past, so it cannot run as tb_app: operations and the
// audit log are append-only for that role, and the rows must carry old
// timestamps and a balance chain that the application would never build in
// one go. It runs as tb_owner (TB_DB_MIGRATE_URL), which owns the tables and
// is therefore not subject to the row-level security policies.
//
// Everything is written in one transaction with fixed ids and
// ON CONFLICT DO NOTHING, so a second run changes nothing. The final
// invariants are checked inside the same transaction; a violation rolls the
// whole seed back.
//
// No audit entries are written: the audit log records actions of
// authenticated actors through the API, and the seed has no actor. The
// ledger rows carry the demo user that "made" each operation.

// SeedData is the content of demo.json.
type SeedData struct {
	Comment    string          `json:"_comment"`
	Nodes      []SeedNode      `json:"nodes"`
	Users      []SeedUser      `json:"users"`
	Roles      []SeedRole      `json:"roles"`
	Settings   []SeedSetting   `json:"settings"`
	Operations []SeedOperation `json:"operations"`
	Transfers  []SeedTransfer  `json:"transfers"`
}

// SeedNode is a node of the tree; parents come before their children.
type SeedNode struct {
	ID     uuid.UUID  `json:"id"`
	Type   string     `json:"type"`
	Parent *uuid.UUID `json:"parent"`
	Name   string     `json:"name"`
}

// SeedUser is a demo user. Password is a throwaway demo value that is hashed
// before it is stored.
type SeedUser struct {
	ID       uuid.UUID `json:"id"`
	Email    string    `json:"email"`
	Password string    `json:"password"`
	Status   string    `json:"status"`
}

// SeedRole is a role assignment.
type SeedRole struct {
	User      uuid.UUID  `json:"user"`
	Node      uuid.UUID  `json:"node"`
	Role      string     `json:"role"`
	GrantedBy *uuid.UUID `json:"granted_by"`
}

// SeedSetting is one setting value on a node.
type SeedSetting struct {
	Node  uuid.UUID       `json:"node"`
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
	By    uuid.UUID       `json:"by"`
}

// SeedOperation is a credit or a debit. Day is an offset in days from the
// seed day (UTC, negative) and At is the time of day "HH:MM".
type SeedOperation struct {
	ID      uuid.UUID `json:"id"`
	Node    uuid.UUID `json:"node"`
	Kind    string    `json:"kind"`
	Amount  int64     `json:"amount"`
	Day     int       `json:"day"`
	At      string    `json:"at"`
	Actor   uuid.UUID `json:"actor"`
	Comment string    `json:"comment"`
}

// SeedTransfer is a pair of ledger rows: transfer_out on From, transfer_in on To.
type SeedTransfer struct {
	TransferID uuid.UUID `json:"transfer_id"`
	OutID      uuid.UUID `json:"out_id"`
	InID       uuid.UUID `json:"in_id"`
	From       uuid.UUID `json:"from"`
	To         uuid.UUID `json:"to"`
	Amount     int64     `json:"amount"`
	Day        int       `json:"day"`
	At         string    `json:"at"`
	Actor      uuid.UUID `json:"actor"`
	Comment    string    `json:"comment"`
}

// ErrSeedForeignData is returned when the database holds nodes, users or
// operations that are not part of the seed and force is off.
var ErrSeedForeignData = errors.New("seed: database holds data that is not part of the seed (use -force to seed anyway)")

const (
	maxSeedDays   = 365
	baseDays      = 70 // nodes, users and roles are created this many days before the seed day
	maxCommentLen = 200
)

// LoadSeedData decodes and validates a seed document. Unknown fields are an error.
func LoadSeedData(raw []byte) (SeedData, error) {
	var d SeedData
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return SeedData{}, fmt.Errorf("seed: decode: %w", err)
	}
	if err := d.Validate(); err != nil {
		return SeedData{}, err
	}
	return d, nil
}

// Validate checks the document without a database.
func (d SeedData) Validate() error {
	nodes, err := d.validateNodes()
	if err != nil {
		return err
	}
	users, err := d.validateUsers()
	if err != nil {
		return err
	}
	if err := d.validateRoles(nodes, users); err != nil {
		return err
	}
	if err := d.validateSettings(nodes, users); err != nil {
		return err
	}
	if err := d.validateLedger(nodes, users); err != nil {
		return err
	}
	// Building the chain proves that no balance goes negative.
	_, _, err = d.ledger(time.Time{})
	return err
}

func (d SeedData) validateNodes() (map[uuid.UUID]node.Type, error) {
	nodes := map[uuid.UUID]node.Type{}
	names := map[string]bool{}
	for _, n := range d.Nodes {
		if _, dup := nodes[n.ID]; dup || n.ID == uuid.Nil {
			return nil, fmt.Errorf("seed: node id %s is empty or duplicated", n.ID)
		}
		typ, err := node.ParseType(n.Type)
		if err != nil {
			return nil, fmt.Errorf("seed: node %s: %w", n.ID, err)
		}
		name, err := node.ParseName(n.Name)
		if err != nil || name.String() != n.Name {
			return nil, fmt.Errorf("seed: node %s: name %q is not normalized", n.ID, n.Name)
		}
		if n.Parent == nil {
			if typ != node.TypePlatform {
				return nil, fmt.Errorf("seed: node %s: only the platform has no parent", n.ID)
			}
		} else {
			pt, ok := nodes[*n.Parent]
			if !ok {
				return nil, fmt.Errorf("seed: node %s: parent %s is unknown or listed later", n.ID, n.Parent)
			}
			if !pt.CanHaveChild(typ) {
				return nil, fmt.Errorf("seed: node %s: %s cannot sit under %s", n.ID, typ, pt)
			}
		}
		key := fmt.Sprintf("%v/%s", n.Parent, name.Key())
		if names[key] {
			return nil, fmt.Errorf("seed: node %s: duplicate name %q under one parent", n.ID, n.Name)
		}
		names[key] = true
		nodes[n.ID] = typ
	}
	roots := 0
	for _, t := range nodes {
		if t == node.TypePlatform {
			roots++
		}
	}
	if roots != 1 {
		return nil, fmt.Errorf("seed: exactly one platform node is required, got %d", roots)
	}
	return nodes, nil
}

func (d SeedData) validateUsers() (map[uuid.UUID]bool, error) {
	users := map[uuid.UUID]bool{}
	emails := map[string]bool{}
	for _, u := range d.Users {
		if _, dup := users[u.ID]; dup || u.ID == uuid.Nil {
			return nil, fmt.Errorf("seed: user id %s is empty or duplicated", u.ID)
		}
		email, err := user.ParseEmail(u.Email)
		if err != nil || string(email) != u.Email || emails[u.Email] {
			return nil, fmt.Errorf("seed: user %s: email is invalid, not lower case or duplicated", u.ID)
		}
		if err := user.ValidatePassword(u.Password); err != nil {
			return nil, fmt.Errorf("seed: user %s: %w", u.ID, err)
		}
		if u.Status != "active" && u.Status != "inactive" {
			return nil, fmt.Errorf("seed: user %s: unknown status %q", u.ID, u.Status)
		}
		emails[u.Email] = true
		users[u.ID] = true
	}
	return users, nil
}

func (d SeedData) validateRoles(nodes map[uuid.UUID]node.Type, users map[uuid.UUID]bool) error {
	seen := map[[2]uuid.UUID]bool{}
	for _, r := range d.Roles {
		if _, err := role.Parse(r.Role); err != nil {
			return fmt.Errorf("seed: role of user %s: %w", r.User, err)
		}
		if !users[r.User] || !hasNode(nodes, r.Node) || (r.GrantedBy != nil && !users[*r.GrantedBy]) {
			return fmt.Errorf("seed: role of user %s on %s refers to an unknown user or node", r.User, r.Node)
		}
		k := [2]uuid.UUID{r.User, r.Node}
		if seen[k] {
			return fmt.Errorf("seed: user %s has two roles on node %s", r.User, r.Node)
		}
		seen[k] = true
	}
	return nil
}

func (d SeedData) validateSettings(nodes map[uuid.UUID]node.Type, users map[uuid.UUID]bool) error {
	seen := map[string]bool{}
	for _, s := range d.Settings {
		key, err := settings.ParseKey(s.Key)
		if err != nil {
			return fmt.Errorf("seed: setting %q: %w", s.Key, err)
		}
		if !hasNode(nodes, s.Node) || !users[s.By] || seen[s.Node.String()+string(key)] {
			return fmt.Errorf("seed: setting %q on %s: unknown node or user, or duplicated", s.Key, s.Node)
		}
		seen[s.Node.String()+string(key)] = true
		var typed any
		if err := json.Unmarshal(s.Value, &typed); err != nil {
			return fmt.Errorf("seed: setting %q on %s: %w", s.Key, s.Node, err)
		}
		if err := validateSettingValue(key, typed); err != nil {
			return fmt.Errorf("seed: setting %q on %s: %w", s.Key, s.Node, err)
		}
	}
	return nil
}

func validateSettingValue(key settings.Key, v any) error {
	switch key {
	case settings.OperationsEnabled:
		if _, ok := v.(bool); !ok {
			return errors.New("value must be a boolean")
		}
	case settings.DisplayTimezone:
		s, ok := v.(string)
		if !ok {
			return errors.New("value must be a string")
		}
		if _, err := settings.ValidateTimezone(s); err != nil {
			return err
		}
	case settings.MaxOperationAmount:
		s, ok := v.(string)
		if !ok {
			return errors.New("value must be a string")
		}
		if _, err := settings.ParseMaxOperation(s); err != nil {
			return err
		}
	}
	return nil
}

func hasNode(nodes map[uuid.UUID]node.Type, id uuid.UUID) bool {
	_, ok := nodes[id]
	return ok
}

func (d SeedData) validateLedger(nodes map[uuid.UUID]node.Type, users map[uuid.UUID]bool) error {
	ids := map[uuid.UUID]bool{}
	use := func(id uuid.UUID) error {
		if id == uuid.Nil || ids[id] {
			return fmt.Errorf("seed: operation id %s is empty or duplicated", id)
		}
		ids[id] = true
		return nil
	}
	check := func(id uuid.UUID, amount int64, day int, at, comment string, actor uuid.UUID) error {
		if _, err := clockOffset(day, at); err != nil {
			return fmt.Errorf("seed: operation %s: %w", id, err)
		}
		if amount <= 0 || len(comment) > maxCommentLen || !users[actor] {
			return fmt.Errorf("seed: operation %s: amount must be positive, comment at most %d bytes, actor known", id, maxCommentLen)
		}
		return nil
	}
	for _, o := range d.Operations {
		if err := use(o.ID); err != nil {
			return err
		}
		if (o.Kind != string(balance.Credit) && o.Kind != string(balance.Debit)) || !hasNode(nodes, o.Node) {
			return fmt.Errorf("seed: operation %s: kind must be credit or debit on a known node", o.ID)
		}
		if err := check(o.ID, o.Amount, o.Day, o.At, o.Comment, o.Actor); err != nil {
			return err
		}
	}
	for _, t := range d.Transfers {
		for _, id := range []uuid.UUID{t.TransferID, t.OutID, t.InID} {
			if err := use(id); err != nil {
				return err
			}
		}
		if t.From == t.To || !hasNode(nodes, t.From) || !hasNode(nodes, t.To) {
			return fmt.Errorf("seed: transfer %s: nodes must be two different known nodes", t.TransferID)
		}
		if err := check(t.TransferID, t.Amount, t.Day, t.At, t.Comment, t.Actor); err != nil {
			return err
		}
	}
	return nil
}

// clockOffset parses "HH:MM" and the day offset into a duration from the
// start of the seed day.
func clockOffset(day int, at string) (time.Duration, error) {
	if day >= 0 || day < -maxSeedDays {
		return 0, fmt.Errorf("day %d is outside -%d..-1", day, maxSeedDays)
	}
	t, err := time.Parse("15:04", at)
	if err != nil {
		return 0, fmt.Errorf("time %q is not HH:MM", at)
	}
	return time.Duration(day)*24*time.Hour + time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

// SeedOptions tunes one run.
type SeedOptions struct {
	// Force seeds although the database holds data that is not part of the seed.
	Force bool
	// Now fixes the seed day; the zero value means the current time.
	Now time.Time
}

// SeedReport counts the rows a run inserted (0 on a repeated run).
type SeedReport struct {
	Nodes, Users, Roles, Settings, Operations, Balances int
}

// leg is one ledger row.
type leg struct {
	id           uuid.UUID
	node         uuid.UUID
	kind         balance.Kind
	amount       int64
	transferID   *uuid.UUID
	counterparty *uuid.UUID
	comment      string
	actor        uuid.UUID
	at           time.Time
	seq          int
}

func (l leg) delta() int64 {
	if l.kind == balance.Credit || l.kind == balance.TransferIn {
		return l.amount
	}
	return -l.amount
}

type chainRow struct {
	leg
	after int64
}

type nodeTotal struct {
	amount  int64
	version int64
	last    time.Time
}

// ledger expands operations and transfers into legs, orders them in time and
// computes the balance chain. It fails when a balance would go negative.
func (d SeedData) ledger(anchor time.Time) ([]chainRow, map[uuid.UUID]*nodeTotal, error) {
	var legs []leg
	for _, o := range d.Operations {
		off, err := clockOffset(o.Day, o.At)
		if err != nil {
			return nil, nil, err
		}
		legs = append(legs, leg{
			id: o.ID, node: o.Node, kind: balance.Kind(o.Kind), amount: o.Amount,
			comment: o.Comment, actor: o.Actor, at: anchor.Add(off), seq: len(legs),
		})
	}
	for i := range d.Transfers {
		t := &d.Transfers[i]
		off, err := clockOffset(t.Day, t.At)
		if err != nil {
			return nil, nil, err
		}
		at := anchor.Add(off)
		legs = append(legs,
			leg{
				id: t.OutID, node: t.From, kind: balance.TransferOut, amount: t.Amount, transferID: &t.TransferID,
				counterparty: &t.To, comment: t.Comment, actor: t.Actor, at: at, seq: len(legs),
			},
			leg{
				id: t.InID, node: t.To, kind: balance.TransferIn, amount: t.Amount, transferID: &t.TransferID,
				counterparty: &t.From, comment: t.Comment, actor: t.Actor, at: at, seq: len(legs) + 1,
			})
	}
	sort.SliceStable(legs, func(i, j int) bool {
		if !legs[i].at.Equal(legs[j].at) {
			return legs[i].at.Before(legs[j].at)
		}
		return legs[i].seq < legs[j].seq
	})
	totals := map[uuid.UUID]*nodeTotal{}
	rows := make([]chainRow, 0, len(legs))
	for _, l := range legs {
		t := totals[l.node]
		if t == nil {
			t = &nodeTotal{}
			totals[l.node] = t
		}
		t.amount += l.delta()
		t.version++
		t.last = l.at
		if t.amount < 0 {
			return nil, nil, fmt.Errorf("seed: operation %s would make the balance of node %s negative", l.id, l.node)
		}
		rows = append(rows, chainRow{leg: l, after: t.amount})
	}
	return rows, totals, nil
}

// Seed writes the data set and verifies the invariants, all in one
// transaction. pool must connect as the table owner (tb_owner).
func Seed(ctx context.Context, pool *pgxpool.Pool, data SeedData, hasher user.Hasher, opts SeedOptions) (SeedReport, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	anchor := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	chain, totals, err := data.ledger(anchor)
	if err != nil {
		return SeedReport{}, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return SeedReport{}, fmt.Errorf("seed: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if !opts.Force {
		if err := checkNoForeignData(ctx, tx, data); err != nil {
			return SeedReport{}, err
		}
	}
	var rep SeedReport
	created := anchor.Add(-baseDays*24*time.Hour + 8*time.Hour)
	if rep.Nodes, err = seedNodes(ctx, tx, data.Nodes, created); err != nil {
		return SeedReport{}, err
	}
	if rep.Users, err = seedUsers(ctx, tx, data.Users, hasher, created); err != nil {
		return SeedReport{}, err
	}
	if rep.Roles, err = seedRoles(ctx, tx, data.Roles, created.Add(time.Hour)); err != nil {
		return SeedReport{}, err
	}
	if rep.Settings, err = seedSettings(ctx, tx, data.Settings, created.Add(2*time.Hour)); err != nil {
		return SeedReport{}, err
	}
	if rep.Operations, err = seedOperations(ctx, tx, chain); err != nil {
		return SeedReport{}, err
	}
	if rep.Balances, err = seedBalances(ctx, tx, totals); err != nil {
		return SeedReport{}, err
	}
	if err := CheckSeedInvariants(ctx, tx); err != nil {
		return SeedReport{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SeedReport{}, fmt.Errorf("seed: commit: %w", err)
	}
	return rep, nil
}

func checkNoForeignData(ctx context.Context, tx pgx.Tx, d SeedData) error {
	var nodeIDs, userIDs, opIDs []uuid.UUID
	for _, n := range d.Nodes {
		nodeIDs = append(nodeIDs, n.ID)
	}
	for _, u := range d.Users {
		userIDs = append(userIDs, u.ID)
	}
	for _, o := range d.Operations {
		opIDs = append(opIDs, o.ID)
	}
	for _, t := range d.Transfers {
		opIDs = append(opIDs, t.OutID, t.InID)
	}
	var foreign int
	err := tx.QueryRow(ctx, `
SELECT (SELECT count(*) FROM nodes WHERE NOT (id = ANY($1)))
     + (SELECT count(*) FROM users WHERE NOT (id = ANY($2)))
     + (SELECT count(*) FROM operations WHERE NOT (id = ANY($3)))`, nodeIDs, userIDs, opIDs).Scan(&foreign)
	if err != nil {
		return fmt.Errorf("seed: check existing data: %w", err)
	}
	if foreign > 0 {
		return fmt.Errorf("%w: %d rows", ErrSeedForeignData, foreign)
	}
	return nil
}

func seedNodes(ctx context.Context, tx pgx.Tx, nodes []SeedNode, created time.Time) (int, error) {
	paths := map[uuid.UUID]string{}
	n := 0
	for _, s := range nodes {
		label := strings.ReplaceAll(s.ID.String(), "-", "")
		path := label
		if s.Parent != nil {
			path = paths[*s.Parent] + "." + label
		}
		paths[s.ID] = path
		name, err := node.ParseName(s.Name)
		if err != nil {
			return 0, fmt.Errorf("seed: node %s: %w", s.ID, err)
		}
		tag, err := tx.Exec(ctx, `
INSERT INTO nodes (id, type, parent_id, path, name, name_key, status, created_at, updated_at)
VALUES ($1, $2, $3, $4::ltree, $5, $6, 'active', $7, $7)
ON CONFLICT (id) DO NOTHING`, s.ID, s.Type, s.Parent, path, name.String(), name.Key(), created)
		if err != nil {
			return 0, fmt.Errorf("seed: insert node %s: %w", s.ID, err)
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}

func seedUsers(ctx context.Context, tx pgx.Tx, users []SeedUser, hasher user.Hasher, created time.Time) (int, error) {
	n := 0
	for _, u := range users {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, u.ID).Scan(&exists); err != nil {
			return 0, fmt.Errorf("seed: look up user %s: %w", u.ID, err)
		}
		if exists {
			continue // keep the stored hash; a new random salt would change the row
		}
		hash, err := hasher.Hash(u.Password)
		if err != nil {
			return 0, fmt.Errorf("seed: hash password of user %s: %w", u.ID, err)
		}
		tag, err := tx.Exec(ctx, `
INSERT INTO users (id, email, password_hash, status, token_version, created_at)
VALUES ($1, $2, $3, $4, 0, $5)
ON CONFLICT (id) DO NOTHING`, u.ID, u.Email, string(hash), u.Status, created)
		if err != nil {
			return 0, fmt.Errorf("seed: insert user %s: %w", u.ID, err)
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}

func seedRoles(ctx context.Context, tx pgx.Tx, roles []SeedRole, at time.Time) (int, error) {
	n := 0
	for _, r := range roles {
		tag, err := tx.Exec(ctx, `
INSERT INTO role_assignments (user_id, node_id, role, granted_by, granted_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, node_id) DO NOTHING`, r.User, r.Node, r.Role, r.GrantedBy, at)
		if err != nil {
			return 0, fmt.Errorf("seed: insert role of user %s: %w", r.User, err)
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}

func seedSettings(ctx context.Context, tx pgx.Tx, list []SeedSetting, at time.Time) (int, error) {
	n := 0
	for _, s := range list {
		tag, err := tx.Exec(ctx, `
INSERT INTO node_settings (node_id, key, value, updated_at, updated_by)
VALUES ($1, $2, $3::jsonb, $4, $5)
ON CONFLICT (node_id, key) DO NOTHING`, s.Node, s.Key, string(s.Value), at, s.By)
		if err != nil {
			return 0, fmt.Errorf("seed: insert setting %q on %s: %w", s.Key, s.Node, err)
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}

func seedOperations(ctx context.Context, tx pgx.Tx, chain []chainRow) (int, error) {
	n := 0
	for _, r := range chain {
		tag, err := tx.Exec(ctx, `
INSERT INTO operations (id, node_id, kind, amount, balance_after, transfer_id, counterparty_node_id, comment, actor_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (id) DO NOTHING`,
			r.id, r.node, string(r.kind), r.amount, r.after, r.transferID, r.counterparty, r.comment, r.actor, r.at)
		if err != nil {
			return 0, fmt.Errorf("seed: insert operation %s: %w", r.id, err)
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}

// seedBalances creates the balance row of every node that has operations. An
// existing row is left alone: it was written together with its operations by
// an earlier run (or by the application), never recomputed.
func seedBalances(ctx context.Context, tx pgx.Tx, totals map[uuid.UUID]*nodeTotal) (int, error) {
	ids := make([]uuid.UUID, 0, len(totals))
	for id := range totals {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	n := 0
	for _, id := range ids {
		t := totals[id]
		tag, err := tx.Exec(ctx, `
INSERT INTO balances (node_id, amount, version, updated_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (node_id) DO NOTHING`, id, t.amount, t.version, t.last)
		if err != nil {
			return 0, fmt.Errorf("seed: insert balance of node %s: %w", id, err)
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}

// SeedQuerier is what CheckSeedInvariants needs; pgx.Tx and *pgxpool.Pool fit.
type SeedQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// seedInvariants are queries that count offending rows; each must give 0.
var seedInvariants = []struct{ name, query string }{
	{"tree is consistent (tb_tree_violations)", `SELECT count(*) FROM tb_tree_violations()`},
	{"the platform node has an active owner", `
SELECT CASE WHEN EXISTS (
  SELECT 1 FROM nodes n
    JOIN role_assignments r ON r.node_id = n.id AND r.role = 'owner'
    JOIN users u ON u.id = r.user_id AND u.status = 'active'
   WHERE n.type = 'platform') THEN 0 ELSE 1 END`},
	{"every balance equals the sum of its operations", `
WITH j AS (
  SELECT node_id, sum(CASE WHEN kind IN ('credit', 'transfer_in') THEN amount ELSE -amount END) AS s, count(*) AS c
    FROM operations GROUP BY node_id)
SELECT count(*) FROM balances b FULL JOIN j ON j.node_id = b.node_id
 WHERE coalesce(b.amount, 0) <> coalesce(j.s, 0) OR coalesce(b.version, 0) <> coalesce(j.c, 0)`},
	{"balance_after follows the operations in time", `
SELECT count(*) FROM (
  SELECT balance_after,
         sum(CASE WHEN kind IN ('credit', 'transfer_in') THEN amount ELSE -amount END)
           OVER (PARTITION BY node_id ORDER BY created_at, id) AS run
    FROM operations) x
 WHERE balance_after <> run`},
	{"every transfer is one out leg and one in leg of equal amount and time", `
SELECT count(*) FROM (
  SELECT transfer_id FROM operations WHERE transfer_id IS NOT NULL GROUP BY transfer_id
  HAVING count(*) <> 2
      OR count(*) FILTER (WHERE kind = 'transfer_out') <> 1
      OR count(*) FILTER (WHERE kind = 'transfer_in') <> 1
      OR min(amount) <> max(amount) OR min(created_at) <> max(created_at)) x`},
	{"transfer legs name each other as counterparty", `
SELECT count(*) FROM operations o
 WHERE o.transfer_id IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM operations p
                    WHERE p.transfer_id = o.transfer_id AND p.id <> o.id
                      AND p.node_id = o.counterparty_node_id AND p.counterparty_node_id = o.node_id)`},
}

// CheckSeedInvariants verifies the invariants the seed promises: a consistent
// tree, an active owner of the platform node, balances equal to the journal,
// a balance chain that follows time and paired transfer legs. The error names
// every violated invariant.
func CheckSeedInvariants(ctx context.Context, q SeedQuerier) error {
	var bad []string
	for _, inv := range seedInvariants {
		var n int
		if err := q.QueryRow(ctx, inv.query).Scan(&n); err != nil {
			return fmt.Errorf("seed: invariant %q: %w", inv.name, err)
		}
		if n != 0 {
			bad = append(bad, fmt.Sprintf("%s (%d violations)", inv.name, n))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("seed: invariants violated: %s", strings.Join(bad, "; "))
	}
	return nil
}

// RunSeed is the entry point of cmd/seed. Flags: -force seeds although the
// database holds data that is not part of the seed.
func RunSeed(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	force := fs.Bool("force", false, "seed even if the database holds data that is not part of the seed")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("usage: seed [-force]: %w", err)
	}
	mig, authCfg, err := config.LoadSeed()
	if err != nil {
		return err
	}
	if mig.MigrateURL == "" {
		return errors.New("TB_DB_MIGRATE_URL is required")
	}
	hasher, err := authn.NewHasher(AuthnConfig(config.Config{Auth: authCfg}))
	if err != nil {
		return err
	}
	data, err := LoadSeedData(seed.Demo)
	if err != nil {
		return err
	}
	pool, err := ConnectDB(ctx, postgres.Config{
		URL: mig.MigrateURL, MaxConns: 2, StatementTimeout: time.Minute, IdleInTxTimeout: time.Minute,
		ConnectTimeout: mig.ConnectTimeout,
	})
	if err != nil {
		return err
	}
	defer pool.Close()
	rep, err := Seed(ctx, pool, data, hasher, SeedOptions{Force: *force})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "seed ok: inserted nodes=%d users=%d roles=%d settings=%d operations=%d balances=%d\n",
		rep.Nodes, rep.Users, rep.Roles, rep.Settings, rep.Operations, rep.Balances)
	return nil
}
