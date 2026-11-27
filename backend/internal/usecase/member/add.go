package member

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// AddMemberInput is the request of UC-M1.
type AddMemberInput struct {
	Actor     user.ActorID
	RequestID string
	NodeID    dnode.ID
	Email     string
	Role      string
	// InitialPassword is used only when the email belongs to no user yet; for
	// an existing user it is ignored. It is never logged or echoed.
	InitialPassword string
}

// Validate checks the input without touching the store.
func (in AddMemberInput) Validate() error {
	if err := requireNodeID(in.NodeID); err != nil {
		return err
	}
	if _, err := user.ParseEmail(in.Email); err != nil {
		return validation("email", err)
	}
	if _, err := role.Parse(in.Role); err != nil {
		return validation("role", err)
	}
	if in.InitialPassword != "" {
		if err := user.ValidatePassword(in.InitialPassword); err != nil {
			return validation("initialPassword", err)
		}
	}
	return nil
}

// AddMemberOutput is the resulting assignment.
type AddMemberOutput struct{ Member Member }

// AddMember is UC-M1.
type AddMember struct{ d Deps }

// NewAddMember builds the use case.
func NewAddMember(d Deps) *AddMember { return &AddMember{d: d} }

// Execute gives the user a role on the node, creating the user when the email
// is new. The answer has the same shape for a new and an existing user.
func (uc *AddMember) Execute(ctx context.Context, in AddMemberInput) (AddMemberOutput, error) {
	if err := in.Validate(); err != nil {
		return AddMemberOutput{}, err
	}
	email, _ := user.ParseEmail(in.Email)
	target, _ := role.Parse(in.Role)
	d := uc.d

	// The hash is expensive, so it is computed outside the transaction, and
	// only when a password was sent. It is computed also for an existing user,
	// whose password is then ignored, so that the cost does not tell whether
	// the email is known.
	var hash user.PasswordHash
	if in.InitialPassword != "" {
		var err error
		if hash, err = d.Hasher.Hash(in.InitialPassword); err != nil {
			return AddMemberOutput{}, fmt.Errorf("member: hash initial password: %w", err)
		}
	}

	var out AddMemberOutput
	err := d.Runner.InTx(ctx, in.Actor, writeTx, func(ctx context.Context, tx port.Tx) error {
		actorRole, err := d.requireManage(ctx, tx, in.Actor, in.NodeID)
		if err != nil {
			return err
		}
		if !role.CanGrant(actorRole, target) {
			return notGrantable()
		}
		root, err := d.isRoot(ctx, tx, in.NodeID)
		if err != nil {
			return err
		}
		userID, created, err := d.resolveUser(ctx, tx, email, hash)
		if err != nil {
			return err
		}
		out.Member, err = d.assign(ctx, tx, in, root, actorRole, userID, email, created, target)
		return err
	})
	if err != nil {
		return AddMemberOutput{}, err
	}
	return out, nil
}

// resolveUser finds the user by email or creates it. Another transaction may
// create the same email at the same moment: the insert then reports false and
// the winner is looked up again (EC-109).
func (d Deps) resolveUser(ctx context.Context, tx port.Tx, email user.Email, hash user.PasswordHash) (id user.ID, created bool, err error) {
	id, found, err := d.Users.IDByEmail(ctx, tx, email)
	if err != nil || found {
		return id, false, err
	}
	if hash == "" {
		return user.ID{}, false, apperr.New(apperr.ValidationFailed, "initialPassword is required for a new user").
			WithField("initialPassword", "required")
	}
	// The id is generated here: a new user is invisible to its creator until
	// an assignment exists, so the row cannot be read back.
	newID := user.ID(d.IDs.New())
	inserted, err := d.Users.Insert(ctx, tx, port.NewUser{ID: newID, Email: email, Hash: hash, CreatedAt: d.Clock.Now()})
	if err != nil {
		return user.ID{}, false, err
	}
	if inserted {
		return newID, true, nil
	}
	id, found, err = d.Users.IDByEmail(ctx, tx, email)
	if err != nil {
		return user.ID{}, false, err
	}
	if !found {
		return user.ID{}, false, fmt.Errorf("member: user with a taken email is not visible")
	}
	return id, false, nil
}

// requireActive refuses an inactive user. It reads the user through the
// assignment, so it must run when the user already has a visible one.
func (d Deps) requireActive(ctx context.Context, tx port.Tx, id user.ID) error {
	st, err := d.Users.Status(ctx, tx, id)
	if err != nil {
		return err
	}
	if st != user.StatusActive {
		return apperr.New(apperr.UserInactive, "the user is inactive")
	}
	return nil
}

// assign creates, replaces or keeps the assignment of the user on the node.
// A repeat of the same role changes nothing and writes no audit entry (EC-40).
func (d Deps) assign(ctx context.Context, tx port.Tx, in AddMemberInput, root bool, actorRole role.Role, userID user.ID, email user.Email, created bool, target role.Role) (Member, error) {
	now := d.Clock.Now()
	next := role.Assignment{UserID: userID, NodeID: in.NodeID, Role: target, GrantedBy: in.Actor, GrantedAt: now}

	// Two attempts: the second one runs only when a concurrent request created
	// the same assignment between the read and the insert.
	for range 2 {
		cur, found, err := d.Members.GetForUpdate(ctx, tx, in.NodeID, userID)
		if err != nil {
			return Member{}, err
		}
		if found {
			return d.replace(ctx, tx, in, root, actorRole, cur, next, email)
		}
		inserted, err := d.Members.Insert(ctx, tx, next)
		if err != nil {
			return Member{}, err
		}
		if !inserted {
			continue
		}
		if !created {
			// The assignment makes the user visible; an inactive one is refused
			// and the whole transaction, assignment included, rolls back.
			if err := d.requireActive(ctx, tx, userID); err != nil {
				return Member{}, err
			}
		} else if err := d.audit(ctx, tx, port.AuditEntry{
			Action: "user.created", NodeID: in.NodeID, TargetID: uuid.UUID(userID), ActorID: in.Actor, RequestID: in.RequestID,
			After: map[string]string{"email": email.String()},
		}); err != nil {
			return Member{}, err
		}
		err = d.audit(ctx, tx, port.AuditEntry{
			Action: "member.granted", NodeID: in.NodeID, TargetID: uuid.UUID(userID), ActorID: in.Actor, RequestID: in.RequestID,
			After: map[string]string{"user_id": userID.String(), "role": string(target)},
		})
		return Member{UserID: userID, Email: email, Role: target, NodeID: in.NodeID, GrantedAt: now}, err
	}
	return Member{}, fmt.Errorf("member: assignment kept appearing and disappearing")
}

// replace handles an assignment that already exists on the node.
func (d Deps) replace(ctx context.Context, tx port.Tx, in AddMemberInput, root bool, actorRole role.Role, cur port.Member, next role.Assignment, email user.Email) (Member, error) {
	if err := d.requireActive(ctx, tx, cur.UserID); err != nil {
		return Member{}, err
	}
	if !role.CanRevoke(actorRole, cur.Role) || !role.CanGrant(actorRole, next.Role) {
		return Member{}, notGrantable()
	}
	if cur.Role == next.Role {
		return Member{UserID: cur.UserID, Email: email, Role: cur.Role, NodeID: cur.NodeID, GrantedAt: cur.GrantedAt}, nil
	}
	if err := d.guardLastRootOwner(ctx, tx, root, in.NodeID, cur.UserID, cur.Role, next.Role); err != nil {
		return Member{}, err
	}
	if err := d.Members.Update(ctx, tx, next); err != nil {
		return Member{}, err
	}
	err := d.audit(ctx, tx, port.AuditEntry{
		Action: "member.changed", NodeID: in.NodeID, TargetID: uuid.UUID(cur.UserID), ActorID: in.Actor, RequestID: in.RequestID,
		Before: map[string]string{"role": string(cur.Role)},
		After:  map[string]string{"user_id": cur.UserID.String(), "role": string(next.Role)},
	})
	return Member{UserID: cur.UserID, Email: email, Role: next.Role, NodeID: next.NodeID, GrantedAt: next.GrantedAt}, err
}
