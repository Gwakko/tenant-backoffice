package settings

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	dset "github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Kind is the JSON type of a setting value as the handler saw it.
type Kind int

// Value kinds. The zero value is KindOther: anything that is neither a string
// nor a boolean (a number, null, an object), which no key accepts.
const (
	KindOther Kind = iota
	KindString
	KindBool
)

// RawValue is a setting value before validation: the JSON type and the
// payload of that type.
type RawValue struct {
	Kind Kind
	Str  string
	Bool bool
}

// kindOf is the JSON type the value of key must have.
func kindOf(key dset.Key) Kind {
	if key == dset.OperationsEnabled {
		return KindBool
	}
	return KindString
}

// raw returns the string form of the value after checking its JSON type
// against the key.
func (v RawValue) raw(key dset.Key) (string, error) {
	if v.Kind != kindOf(key) {
		return "", apperr.New(apperr.InvalidSettingValue, "the value has the wrong type for this setting").
			WithField("value", string(apperr.InvalidSettingValue))
	}
	if v.Kind == KindBool {
		return boolString(v.Bool), nil
	}
	return v.Str, nil
}

// SetSettingInput is the request of UC-S2.
type SetSettingInput struct {
	Actor     user.ActorID
	RequestID string
	NodeID    dnode.ID
	Key       string
	Value     RawValue
}

// Validate checks the input without touching the store.
func (in SetSettingInput) Validate() error {
	if err := requireID(in.NodeID); err != nil {
		return err
	}
	key, err := dset.ParseKey(in.Key)
	if err != nil {
		return err
	}
	_, err = in.Value.raw(key)
	return err
}

// SetSettingOutput is the setting after the change.
type SetSettingOutput struct{ Item Item }

// SetSetting is UC-S2.
type SetSetting struct{ d Deps }

// NewSetSetting builds the use case.
func NewSetSetting(d Deps) *SetSetting { return &SetSetting{d: d} }

// Execute sets the override of the key on the node. Setting the value the node
// already has writes nothing and leaves no audit entry. The write is accepted
// even if an ancestor restricts the key more: the effective value then stays
// that of the ancestor (a descendant cannot relax a restriction).
func (uc *SetSetting) Execute(ctx context.Context, in SetSettingInput) (SetSettingOutput, error) {
	if err := in.Validate(); err != nil {
		return SetSettingOutput{}, err
	}
	key, _ := dset.ParseKey(in.Key)
	rawValue, _ := in.Value.raw(key)
	d := uc.d

	var out SetSettingOutput
	err := d.Runner.InTx(ctx, in.Actor, writeTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.NodeID, role.SettingsWrite); err != nil {
			return err
		}
		next, err := dset.NodeSettings{NodeID: in.NodeID}.Set(key, rawValue)
		if err != nil {
			return err
		}
		canonical, _ := next.Value(key)
		w, err := d.Settings.Set(ctx, tx, in.NodeID, key, canonical, d.Clock.Now())
		if err != nil {
			return err
		}
		if w.Changed {
			before := map[string]string{"key": string(key)}
			if w.HadBefore {
				before["value"] = w.Before
			}
			if err := d.Audit.Write(ctx, tx, port.AuditEntry{
				Action: "settings.set", NodeID: in.NodeID, ActorID: in.Actor, RequestID: in.RequestID,
				Before: before, After: map[string]string{"key": string(key), "value": canonical},
			}); err != nil {
				return err
			}
		}
		items, err := d.resolve(ctx, tx, in.NodeID)
		out.Item = pick(items, key)
		return err
	})
	if err != nil {
		return SetSettingOutput{}, err
	}
	return out, nil
}
