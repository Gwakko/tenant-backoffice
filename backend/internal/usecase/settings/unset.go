package settings

import (
	"context"

	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	dset "github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// UnsetSettingInput is the request of UC-S3.
type UnsetSettingInput struct {
	Actor     user.ActorID
	RequestID string
	NodeID    dnode.ID
	Key       string
}

// Validate checks the input without touching the store.
func (in UnsetSettingInput) Validate() error {
	if err := requireID(in.NodeID); err != nil {
		return err
	}
	_, err := dset.ParseKey(in.Key)
	return err
}

// UnsetSettingOutput is the setting after the override is gone.
type UnsetSettingOutput struct{ Item Item }

// UnsetSetting is UC-S3.
type UnsetSetting struct{ d Deps }

// NewUnsetSetting builds the use case.
func NewUnsetSetting(d Deps) *UnsetSetting { return &UnsetSetting{d: d} }

// Execute removes the override of the key on the node, so the node inherits
// again. A missing override is no change: no audit entry.
func (uc *UnsetSetting) Execute(ctx context.Context, in UnsetSettingInput) (UnsetSettingOutput, error) {
	if err := in.Validate(); err != nil {
		return UnsetSettingOutput{}, err
	}
	key, _ := dset.ParseKey(in.Key)
	d := uc.d

	var out UnsetSettingOutput
	err := d.Runner.InTx(ctx, in.Actor, writeTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.NodeID, role.SettingsWrite); err != nil {
			return err
		}
		before, found, err := d.Settings.Unset(ctx, tx, in.NodeID, key)
		if err != nil {
			return err
		}
		if found {
			if err := d.Audit.Write(ctx, tx, port.AuditEntry{
				Action: "settings.unset", NodeID: in.NodeID, ActorID: in.Actor, RequestID: in.RequestID,
				Before: map[string]string{"key": string(key), "value": before},
				After:  map[string]string{"key": string(key)},
			}); err != nil {
				return err
			}
		}
		items, err := d.resolve(ctx, tx, in.NodeID)
		out.Item = pick(items, key)
		return err
	})
	if err != nil {
		return UnsetSettingOutput{}, err
	}
	return out, nil
}
