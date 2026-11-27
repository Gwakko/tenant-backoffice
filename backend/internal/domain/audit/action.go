// Package audit is the model of the immutable audit trail: the closed set of
// actions, the field lists describing a change, and the Entry record. The
// domain knows nothing about JSON or storage.
package audit

import "github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"

// Action is what happened.
type Action string

// Audit actions.
const (
	NodeCreated        Action = "node.created"
	NodeRenamed        Action = "node.renamed"
	NodeMoved          Action = "node.moved"
	NodeChildMovedOut  Action = "node.child_moved_out"
	NodeChildMovedIn   Action = "node.child_moved_in"
	NodeDeactivated    Action = "node.deactivated"
	NodeActivated      Action = "node.activated"
	NodeDeleted        Action = "node.deleted"
	MemberGranted      Action = "member.granted"
	MemberChanged      Action = "member.changed"
	MemberRevoked      Action = "member.revoked"
	UserCreated        Action = "user.created"
	SettingsSet        Action = "settings.set"
	SettingsUnset      Action = "settings.unset"
	BalanceCredited    Action = "balance.credited"
	BalanceDebited     Action = "balance.debited"
	BalanceTransferred Action = "balance.transferred"
	AuthPasswordChange Action = "auth.password_changed"
)

// Actions lists every action.
func Actions() []Action {
	return []Action{
		NodeCreated, NodeRenamed, NodeMoved, NodeChildMovedOut, NodeChildMovedIn,
		NodeDeactivated, NodeActivated, NodeDeleted,
		MemberGranted, MemberChanged, MemberRevoked, UserCreated,
		SettingsSet, SettingsUnset,
		BalanceCredited, BalanceDebited, BalanceTransferred,
		AuthPasswordChange,
	}
}

// ParseAction validates a raw action.
func ParseAction(s string) (Action, error) {
	a := Action(s)
	if !a.Valid() {
		return "", apperr.New(apperr.ValidationFailed, "unknown audit action")
	}
	return a, nil
}

// TargetType is the kind of object an action concerns.
type TargetType string

// Target types.
const (
	TargetNode    TargetType = "node"
	TargetMember  TargetType = "member"
	TargetUser    TargetType = "user"
	TargetSetting TargetType = "setting"
	TargetBalance TargetType = "balance"
)

// TargetType returns the kind of object the action concerns, or "" for an
// unknown action.
func (a Action) TargetType() TargetType {
	switch a {
	case NodeCreated, NodeRenamed, NodeMoved, NodeChildMovedOut, NodeChildMovedIn,
		NodeDeactivated, NodeActivated, NodeDeleted:
		return TargetNode
	case MemberGranted, MemberChanged, MemberRevoked:
		return TargetMember
	case UserCreated, AuthPasswordChange:
		return TargetUser
	case SettingsSet, SettingsUnset:
		return TargetSetting
	case BalanceCredited, BalanceDebited, BalanceTransferred:
		return TargetBalance
	default:
		return ""
	}
}

// Valid reports whether a is a known action.
func (a Action) Valid() bool { return a.TargetType() != "" }
