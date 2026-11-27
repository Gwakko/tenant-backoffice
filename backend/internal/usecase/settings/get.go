package settings

import (
	"context"

	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// GetSettingsInput is the request of UC-S1.
type GetSettingsInput struct {
	Actor  user.ActorID
	NodeID dnode.ID
}

// GetSettingsOutput lists every key, in the fixed order of settings.Keys.
type GetSettingsOutput struct{ Items []Item }

// GetSettings is UC-S1.
type GetSettings struct{ d Deps }

// NewGetSettings builds the use case.
func NewGetSettings(d Deps) *GetSettings { return &GetSettings{d: d} }

// Execute returns the own and effective value of every setting of the node.
func (uc *GetSettings) Execute(ctx context.Context, in GetSettingsInput) (GetSettingsOutput, error) {
	if err := requireID(in.NodeID); err != nil {
		return GetSettingsOutput{}, err
	}
	var out GetSettingsOutput
	err := uc.d.Runner.InTx(ctx, in.Actor, readTx, func(ctx context.Context, tx port.Tx) error {
		if err := uc.d.require(ctx, tx, in.Actor, in.NodeID, role.SettingsRead); err != nil {
			return err
		}
		items, err := uc.d.resolve(ctx, tx, in.NodeID)
		out.Items = items
		return err
	})
	if err != nil {
		return GetSettingsOutput{}, err
	}
	return out, nil
}
