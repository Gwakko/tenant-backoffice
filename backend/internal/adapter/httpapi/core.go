package httpapi

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/settings"
)

// CookieConfig describes the refresh cookie. HttpOnly, SameSite=Strict and the
// path are not configurable: they are the protection (spec 02 section 7).
type CookieConfig struct {
	// Secure marks the cookie HTTPS-only. It is on by default; the config can
	// turn it off for local plain-HTTP work only.
	Secure bool
}

// CoreDeps are the collaborators of CoreHandlers: the use cases of
// authentication, the profile, nodes, members and settings.
type CoreDeps struct {
	Log     *slog.Logger
	Clock   port.Clock
	Cursors *CursorCodec
	Cookie  CookieConfig

	Login          *auth.LoginUseCase
	Refresh        *auth.RefreshUseCase
	Logout         *auth.LogoutUseCase
	ChangePassword *auth.ChangePasswordUseCase
	Profile        *auth.GetProfileUseCase

	GetNode         *node.GetNode
	ListChildren    *node.ListChildren
	CreateNode      *node.CreateNode
	RenameNode      *node.RenameNode
	MoveNode        *node.MoveNode
	ListMoveTargets *node.ListMoveTargets
	DeactivateNode  *node.DeactivateNode
	ActivateNode    *node.ActivateNode
	DeleteNode      *node.DeleteNode
	AccessRoots     *node.ListAccessRoots
	Permissions     *node.GetPermissions

	ListMembers  *member.ListMembers
	AddMember    *member.AddMember
	ChangeRole   *member.ChangeMemberRole
	RemoveMember *member.RemoveMember

	GetSettings  *settings.GetSettings
	SetSetting   *settings.SetSetting
	UnsetSetting *settings.UnsetSetting
}

// CoreHandlers implements the operations of auth, me, nodes, members and
// settings. The other operation groups are separate types that the composition
// root embeds next to it, so each can be added without touching this one.
type CoreHandlers struct {
	d CoreDeps
}

// CoreOperations are the operation ids CoreHandlers implements; they go into
// Options.Implemented.
func CoreOperations() map[string]bool {
	// The names are the operation ids as the generated strict server passes them.
	ids := []string{
		"Login", "Refresh", "Logout", "ChangePassword", "GetMe", "GetMyPermissions",
		"GetNode", "ListChildren", "CreateChild", "RenameNode", "MoveNode", "ListMoveTargets",
		"DeactivateNode", "ActivateNode", "DeleteNode",
		"ListMembers", "AddMember", "ChangeMemberRole", "RemoveMember",
		"ListSettings", "PutSetting", "DeleteSetting",
	}
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// MergeOperations joins sets of implemented operation ids.
func MergeOperations(sets ...map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, s := range sets {
		for k, v := range s {
			out[k] = v
		}
	}
	return out
}

// Unimplemented is the fallback for the operations no handler type implements
// yet: embedding it deeper than the real handler types lets those win, and the
// server answers 501 for operations that are not in Options.Implemented.
type Unimplemented struct{ gen.StrictServerInterface }

var errMissingDep = errors.New("httpapi: missing handler dependency")

// NewCoreHandlers builds the handlers; every collaborator is required.
func NewCoreHandlers(d CoreDeps) (*CoreHandlers, error) {
	ok := d.Log != nil && d.Clock != nil && d.Cursors != nil &&
		d.Login != nil && d.Refresh != nil && d.Logout != nil && d.ChangePassword != nil && d.Profile != nil &&
		d.GetNode != nil && d.ListChildren != nil && d.CreateNode != nil && d.RenameNode != nil &&
		d.MoveNode != nil && d.ListMoveTargets != nil && d.DeactivateNode != nil && d.ActivateNode != nil &&
		d.DeleteNode != nil && d.AccessRoots != nil && d.Permissions != nil &&
		d.ListMembers != nil && d.AddMember != nil && d.ChangeRole != nil && d.RemoveMember != nil &&
		d.GetSettings != nil && d.SetSetting != nil && d.UnsetSetting != nil
	if !ok {
		return nil, errMissingDep
	}
	return &CoreHandlers{d: d}, nil
}

// actorOf is the authenticated user of the request. It comes from the context
// that the authn middleware filled, never from the body or the parameters.
func actorOf(ctx context.Context) (user.ActorID, error) {
	a, ok := ActorFrom(ctx)
	if !ok {
		return user.ActorID{}, apperr.TokenMissingError()
	}
	return a, nil
}
