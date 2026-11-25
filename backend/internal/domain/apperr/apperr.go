// Package apperr defines the domain error type shared by all layers. Codes are
// stable identifiers; mapping a code to a transport status happens only in the
// HTTP adapter.
package apperr

import "errors"

// Code is a stable machine-readable error code.
type Code string

// Codes used by the node domain.
const (
	ValidationFailed  Code = "validation_failed"
	InvalidID         Code = "invalid_id"
	InvalidNodeType   Code = "invalid_node_type"
	InvalidName       Code = "invalid_name"
	InvalidParentType Code = "invalid_parent_type"
	MoveCycle         Code = "move_cycle"
	RootImmutable     Code = "root_immutable"
	NodeInactive      Code = "node_inactive"
	NodeHasChildren   Code = "node_has_children"
	NodeHasBalance    Code = "node_has_balance"
	NodeHasHistory    Code = "node_has_history"
	NameTaken         Code = "name_taken"
	ChildrenLimit     Code = "children_limit"
)

// Codes used by the role and user domains.
const (
	InvalidEmail     Code = "invalid_email"
	InvalidPassword  Code = "invalid_password"
	InvalidRole      Code = "invalid_role"
	RoleNotGrantable Code = "role_not_grantable"
)

// Error is a domain error with a code, a human-readable message and optional
// per-field details.
type Error struct {
	Code    Code
	Message string
	Fields  map[string]string
}

// New builds an Error without field details.
func New(code Code, message string) Error {
	return Error{Code: code, Message: message}
}

// WithField returns a copy of e with one more field detail.
func (e Error) WithField(field, detail string) Error {
	fields := make(map[string]string, len(e.Fields)+1)
	for k, v := range e.Fields {
		fields[k] = v
	}
	fields[field] = detail
	e.Fields = fields
	return e
}

// Error implements the error interface.
func (e Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

// Is reports whether target is an Error with the same code, so that
// errors.Is(err, apperr.New(code, "")) matches by code only.
func (e Error) Is(target error) bool {
	t, ok := target.(Error)
	return ok && t.Code == e.Code
}

// CodeOf extracts the code of the first Error in err's chain.
func CodeOf(err error) (Code, bool) {
	var e Error
	if errors.As(err, &e) {
		return e.Code, true
	}
	return "", false
}
