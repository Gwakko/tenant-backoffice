package app

import (
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/authn"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/settings"
)

// UUIDv7 generates time-ordered ids (port.IDGen).
type UUIDv7 struct{}

// New implements port.IDGen.
func (UUIDv7) New() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// Handlers is the server's implementation of every operation. Each group of
// operations is its own type embedded here; the groups that do not exist yet
// stay behind Unimplemented, which sits one level deeper so the real groups
// win. Adding a group means one more embedded field and one more entry in
// operations.
type Handlers struct {
	httpapi.Unimplemented
	*httpapi.CoreHandlers
}

// Wiring is what the HTTP server needs from the database-backed layer.
type Wiring struct {
	Authenticator httpapi.Authenticator
	Handlers      gen.StrictServerInterface
	Implemented   map[string]bool
}

// Wire builds the use cases over the pool and the handlers over the use cases.
func Wire(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger, clock port.Clock, cursors *httpapi.CursorCodec) (*Wiring, error) {
	acfg := AuthnConfig(cfg)
	jwt, err := authn.NewJWT(acfg, clock)
	if err != nil {
		return nil, fmt.Errorf("access tokens: %w", err)
	}
	hasher, err := authn.NewHasher(acfg)
	if err != nil {
		return nil, fmt.Errorf("password hasher: %w", err)
	}
	ids := UUIDv7{}
	runner := postgres.NewTxRunner(pool, log)
	users := postgres.NewAuthUserRepo(pool, log)
	audit := postgres.NewAuditWriter(log, clock, ids)
	checker := postgres.NewChecker(log)

	ad := auth.Deps{
		Config: AuthConfig(cfg), Tx: runner, Users: users, Profile: users,
		Refresh: postgres.NewRefreshTokenRepo(pool, log), Codec: authn.NewRefreshCodec(),
		Issuer: jwt, Verify: jwt, Hasher: hasher, Clock: clock, IDs: ids, Log: log,
	}
	nd := node.Deps{
		Runner: runner, Checker: checker, Nodes: postgres.NewNodeRepo(log), Lock: postgres.NewTreeLock(),
		Audit: audit, Clock: clock, IDs: ids,
	}
	md := member.Deps{
		Runner: runner, Checker: checker, Nodes: nd.Nodes, Members: postgres.NewMemberRepo(log),
		Users: postgres.NewUserRepo(log), Hasher: hasher, Audit: audit, Clock: clock, IDs: ids,
	}
	sd := settings.Deps{Runner: runner, Checker: checker, Settings: postgres.NewSettingsRepo(log), Audit: audit, Clock: clock}

	authenticate, err := auth.NewAuthenticateUseCase(ad)
	if err != nil {
		return nil, err
	}
	core, err := coreHandlers(cfg, log, clock, cursors, ad, nd, md, sd)
	if err != nil {
		return nil, err
	}
	return &Wiring{
		Authenticator: authenticate,
		Handlers:      Handlers{CoreHandlers: core},
		Implemented:   httpapi.MergeOperations(httpapi.CoreOperations()),
	}, nil
}

func coreHandlers(cfg config.Config, log *slog.Logger, clock port.Clock, cursors *httpapi.CursorCodec,
	ad auth.Deps, nd node.Deps, md member.Deps, sd settings.Deps,
) (*httpapi.CoreHandlers, error) {
	d := httpapi.CoreDeps{
		Log: log, Clock: clock, Cursors: cursors, Cookie: httpapi.CookieConfig{Secure: cfg.Auth.CookieSecure},

		GetNode: node.NewGetNode(nd), ListChildren: node.NewListChildren(nd), CreateNode: node.NewCreateNode(nd),
		RenameNode: node.NewRenameNode(nd), MoveNode: node.NewMoveNode(nd), ListMoveTargets: node.NewListMoveTargets(nd),
		DeactivateNode: node.NewDeactivateNode(nd), ActivateNode: node.NewActivateNode(nd), DeleteNode: node.NewDeleteNode(nd),
		AccessRoots: node.NewListAccessRoots(nd), Permissions: node.NewGetPermissions(nd),

		ListMembers: member.NewListMembers(md), AddMember: member.NewAddMember(md),
		ChangeRole: member.NewChangeMemberRole(md), RemoveMember: member.NewRemoveMember(md),

		GetSettings: settings.NewGetSettings(sd), SetSetting: settings.NewSetSetting(sd), UnsetSetting: settings.NewUnsetSetting(sd),
	}
	var err error
	if d.Login, err = auth.NewLoginUseCase(ad); err != nil {
		return nil, err
	}
	if d.Refresh, err = auth.NewRefreshUseCase(ad); err != nil {
		return nil, err
	}
	if d.Logout, err = auth.NewLogoutUseCase(ad); err != nil {
		return nil, err
	}
	if d.ChangePassword, err = auth.NewChangePasswordUseCase(ad); err != nil {
		return nil, err
	}
	if d.Profile, err = auth.NewGetProfileUseCase(ad); err != nil {
		return nil, err
	}
	return httpapi.NewCoreHandlers(d)
}
