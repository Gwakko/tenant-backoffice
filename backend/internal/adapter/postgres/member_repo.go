package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

const (
	resourceMember = "member"

	selectAssignmentForUpdate = `SELECT u.email, ra.role, ra.granted_at
  FROM role_assignments ra JOIN users u ON u.id = ra.user_id
 WHERE ra.node_id = $1 AND ra.user_id = $2
   FOR UPDATE OF ra`

	insertAssignment = `INSERT INTO role_assignments (user_id, node_id, role, granted_by, granted_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, node_id) DO NOTHING`

	updateAssignment = `UPDATE role_assignments SET role = $3, granted_by = $4, granted_at = $5
 WHERE user_id = $1 AND node_id = $2`

	deleteAssignment = `DELETE FROM role_assignments WHERE node_id = $1 AND user_id = $2`

	// Rows are locked in user id order, so two transactions that lock the same
	// owners never wait on each other in opposite order. After a concurrent
	// delete commits, READ COMMITTED re-checks the row and skips it.
	lockActiveOwners = `SELECT ra.user_id
  FROM role_assignments ra JOIN users u ON u.id = ra.user_id
 WHERE ra.node_id = $1 AND ra.role = 'owner' AND u.status = 'active'
 ORDER BY ra.user_id
   FOR UPDATE OF ra`

	// $2 = include assignments on ancestors of the node. nodes is filtered by
	// row-level security, so ancestors above the actor's access root drop out.
	listMembers = `SELECT u.id, u.email, ra.role, ra.node_id, ra.granted_at
  FROM role_assignments ra
  JOIN nodes n ON n.id = ra.node_id
  JOIN users u ON u.id = ra.user_id
 WHERE (ra.node_id = $1 OR ($2 AND n.path @> (SELECT path FROM nodes WHERE id = $1)))
   AND ($3::text IS NULL OR (u.email, u.id, ra.node_id) > ($3::text, $4::uuid, $5::uuid))
 ORDER BY u.email, u.id, ra.node_id
 LIMIT $6`
)

// MemberRepo implements port.MemberRepo.
type MemberRepo struct{ log *slog.Logger }

var _ port.MemberRepo = (*MemberRepo)(nil)

// NewMemberRepo builds the repository.
func NewMemberRepo(log *slog.Logger) *MemberRepo { return &MemberRepo{log: log} }

// GetForUpdate implements port.MemberRepo.
func (r *MemberRepo) GetForUpdate(ctx context.Context, tx port.Tx, nodeID node.ID, userID user.ID) (port.Member, bool, error) {
	t, err := unwrap(tx)
	if err != nil {
		return port.Member{}, false, err
	}
	m := port.Member{UserID: userID, NodeID: nodeID}
	var email, rl string
	err = t.QueryRow(ctx, selectAssignmentForUpdate, uuid.UUID(nodeID), uuid.UUID(userID)).Scan(&email, &rl, &m.GrantedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.Member{}, false, nil
	}
	if err != nil {
		return port.Member{}, false, mapError(r.log, "get assignment", resourceMember, err)
	}
	m.Email, m.Role, m.GrantedAt = user.Email(email), role.Role(rl), m.GrantedAt.UTC()
	if m.Role.Rank() == 0 {
		return port.Member{}, false, fmt.Errorf("postgres: assignment has unknown role %q", rl)
	}
	return m, true, nil
}

// Insert implements port.MemberRepo.
func (r *MemberRepo) Insert(ctx context.Context, tx port.Tx, a role.Assignment) (bool, error) {
	t, err := unwrap(tx)
	if err != nil {
		return false, err
	}
	tag, err := t.Exec(ctx, insertAssignment,
		uuid.UUID(a.UserID), uuid.UUID(a.NodeID), string(a.Role), uuid.UUID(a.GrantedBy), a.GrantedAt)
	if err != nil {
		return false, mapWriteError(r.log, "insert assignment", resourceMember, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Update implements port.MemberRepo.
func (r *MemberRepo) Update(ctx context.Context, tx port.Tx, a role.Assignment) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	tag, err := t.Exec(ctx, updateAssignment,
		uuid.UUID(a.UserID), uuid.UUID(a.NodeID), string(a.Role), uuid.UUID(a.GrantedBy), a.GrantedAt)
	if err != nil {
		return mapWriteError(r.log, "update assignment", resourceMember, err)
	}
	if tag.RowsAffected() != 1 {
		return apperr.NotFoundError(resourceMember)
	}
	return nil
}

// Delete implements port.MemberRepo.
func (r *MemberRepo) Delete(ctx context.Context, tx port.Tx, nodeID node.ID, userID user.ID) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	tag, err := t.Exec(ctx, deleteAssignment, uuid.UUID(nodeID), uuid.UUID(userID))
	if err != nil {
		return mapWriteError(r.log, "delete assignment", resourceMember, err)
	}
	if tag.RowsAffected() != 1 {
		return apperr.NotFoundError(resourceMember)
	}
	return nil
}

// LockActiveOwners implements port.MemberRepo.
func (r *MemberRepo) LockActiveOwners(ctx context.Context, tx port.Tx, nodeID node.ID) ([]user.ID, error) {
	t, err := unwrap(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, lockActiveOwners, uuid.UUID(nodeID))
	if err != nil {
		return nil, mapError(r.log, "lock owners", resourceMember, err)
	}
	defer rows.Close()
	var out []user.ID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, mapError(r.log, "scan owner", resourceMember, err)
		}
		out = append(out, user.ID(id))
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(r.log, "lock owners", resourceMember, err)
	}
	return out, nil
}

// List implements port.MemberRepo.
func (r *MemberRepo) List(ctx context.Context, tx port.Tx, nodeID node.ID, inherited bool, after *port.MemberCursor, limit int) ([]port.Member, error) {
	t, err := unwrap(tx)
	if err != nil {
		return nil, err
	}
	var email *string
	var uidArg, nidArg uuid.UUID
	if after != nil {
		email, uidArg, nidArg = &after.Email, uuid.UUID(after.UserID), uuid.UUID(after.NodeID)
	}
	rows, err := t.Query(ctx, listMembers, uuid.UUID(nodeID), inherited, email, uidArg, nidArg, limit)
	if err != nil {
		return nil, mapError(r.log, "list members", resourceMember, err)
	}
	defer rows.Close()
	var out []port.Member
	for rows.Next() {
		var m port.Member
		var id, nid uuid.UUID
		var em, rl string
		if err := rows.Scan(&id, &em, &rl, &nid, &m.GrantedAt); err != nil {
			return nil, mapError(r.log, "scan member", resourceMember, err)
		}
		m.UserID, m.Email, m.Role, m.NodeID, m.GrantedAt = user.ID(id), user.Email(em), role.Role(rl), node.ID(nid), m.GrantedAt.UTC()
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(r.log, "list members", resourceMember, err)
	}
	return out, nil
}
