// Package settings is the model of per-node settings: the fixed set of typed
// keys, validation of raw values, and the pure resolution of effective values
// along the chain of ancestors.
package settings

import (
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

// Key names a setting. The set is fixed.
type Key string

// Setting keys.
const (
	DisplayTimezone    Key = "display_timezone"
	OperationsEnabled  Key = "operations_enabled"
	MaxOperationAmount Key = "max_operation_amount"
)

// Keys lists every key.
func Keys() []Key { return []Key{DisplayTimezone, OperationsEnabled, MaxOperationAmount} }

// ParseKey validates a raw key.
func ParseKey(s string) (Key, error) {
	k := Key(s)
	switch k {
	case DisplayTimezone, OperationsEnabled, MaxOperationAmount:
		return k, nil
	default:
		return "", apperr.New(apperr.UnknownSetting, "unknown setting")
	}
}

// Defaults are the effective values at the root when nothing is set.
const (
	DefaultTimezone          = "UTC"
	DefaultOperationsEnabled = true
	DefaultMaxOperation      = money.Amount(100000000)
)

func invalidValue(msg string) error {
	return apperr.New(apperr.InvalidSettingValue, msg).WithField("value", string(apperr.InvalidSettingValue))
}

// ValidateTimezone accepts a non-empty IANA zone name known to the runtime;
// "Local" is rejected because it depends on the host.
func ValidateTimezone(s string) (string, error) {
	if s == "" || s == "Local" {
		return "", invalidValue("timezone must be an IANA zone name")
	}
	if _, err := time.LoadLocation(s); err != nil {
		return "", invalidValue("timezone is not a known IANA zone")
	}
	return s, nil
}

// ParseBool accepts exactly "true" or "false".
func ParseBool(s string) (bool, error) {
	switch s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, invalidValue("value must be true or false")
	}
}

// ParseMaxOperation accepts an amount string (see money.ParseAmount) in
// 1..MaxInt64.
func ParseMaxOperation(s string) (money.Amount, error) {
	a, err := money.ParseAmount(s)
	if err != nil || a < 1 {
		return 0, invalidValue("limit must be an amount of at least 1")
	}
	return a, nil
}

// NodeSettings holds the values explicitly set on one node; nil means "not set,
// inherit".
type NodeSettings struct {
	NodeID             node.ID
	DisplayTimezone    *string
	OperationsEnabled  *bool
	MaxOperationAmount *money.Amount
}

// Set returns a copy of s with the key set from a raw string, after validation.
func (s NodeSettings) Set(key Key, raw string) (NodeSettings, error) {
	switch key {
	case DisplayTimezone:
		v, err := ValidateTimezone(raw)
		if err != nil {
			return s, err
		}
		s.DisplayTimezone = &v
	case OperationsEnabled:
		v, err := ParseBool(raw)
		if err != nil {
			return s, err
		}
		s.OperationsEnabled = &v
	case MaxOperationAmount:
		v, err := ParseMaxOperation(raw)
		if err != nil {
			return s, err
		}
		s.MaxOperationAmount = &v
	default:
		return s, apperr.New(apperr.UnknownSetting, "unknown setting")
	}
	return s, nil
}

// Unset returns a copy of s with the override of key removed.
func (s NodeSettings) Unset(key Key) (NodeSettings, error) {
	switch key {
	case DisplayTimezone:
		s.DisplayTimezone = nil
	case OperationsEnabled:
		s.OperationsEnabled = nil
	case MaxOperationAmount:
		s.MaxOperationAmount = nil
	default:
		return s, apperr.New(apperr.UnknownSetting, "unknown setting")
	}
	return s, nil
}

// Value returns the raw string form of an explicit value, or false if the key
// is not set on this node.
func (s NodeSettings) Value(key Key) (string, bool) {
	switch key {
	case DisplayTimezone:
		if s.DisplayTimezone != nil {
			return *s.DisplayTimezone, true
		}
	case OperationsEnabled:
		if s.OperationsEnabled != nil {
			if *s.OperationsEnabled {
				return "true", true
			}
			return "false", true
		}
	case MaxOperationAmount:
		if s.MaxOperationAmount != nil {
			return s.MaxOperationAmount.String(), true
		}
	}
	return "", false
}
