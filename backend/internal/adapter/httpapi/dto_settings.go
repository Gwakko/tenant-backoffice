package httpapi

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	dset "github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/settings"
)

// rawValue reads the JSON type of a setting value. Only a string and a boolean
// can be valid; anything else is passed on as KindOther so that the use case
// answers invalid_setting_value, the same as for a wrong type of a known kind.
func rawValue(v gen.SettingValue) settings.RawValue {
	raw, err := v.MarshalJSON()
	if err != nil {
		return settings.RawValue{}
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return settings.RawValue{}
	}
	// Decide by the first byte: json.Unmarshal would also read null as false.
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return settings.RawValue{Kind: settings.KindString, Str: s}
		}
	case 't', 'f':
		var b bool
		if json.Unmarshal(raw, &b) == nil {
			return settings.RawValue{Kind: settings.KindBool, Bool: b}
		}
	}
	return settings.RawValue{}
}

// settingValue builds the typed JSON value of key from its stored text:
// operations_enabled is a boolean, the others are strings.
func settingValue(key dset.Key, raw string) (gen.SettingValue, error) {
	var out gen.SettingValue
	if key == dset.OperationsEnabled {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return out, err
		}
		return out, out.FromSettingValue1(b)
	}
	return out, out.FromSettingValue0(raw)
}

func settingItemDTO(it settings.Item) (gen.SettingItem, error) {
	eff, err := settingValue(it.Key, it.Effective)
	if err != nil {
		return gen.SettingItem{}, err
	}
	out := gen.SettingItem{Key: gen.SettingKey(it.Key), Effective: eff}
	if it.Own != nil {
		v, err := settingValue(it.Key, *it.Own)
		if err != nil {
			return gen.SettingItem{}, err
		}
		raw, err := v.MarshalJSON()
		if err != nil {
			return gen.SettingItem{}, err
		}
		var own gen.SettingItem_Own
		if err := own.UnmarshalJSON(raw); err != nil {
			return gen.SettingItem{}, err
		}
		out.Own = &own
	}
	if it.SourceNodeID != nil {
		s := uid(*it.SourceNodeID)
		out.SourceNodeId = &s
	}
	return out, nil
}
