// Package user is the domain model of a backoffice user: identity, normalized
// email, password rules and the password hasher port.
package user

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

// Email length bounds, in bytes.
const (
	minEmailLen = 3
	maxEmailLen = 254
)

// Email is a normalized (trimmed, lower-case) email address. Create it only
// through ParseEmail.
type Email string

// ParseEmail normalizes and validates a raw email: trim, lower-case, length
// 3..254, exactly one "@", non-empty local and domain parts, no whitespace.
// Full RFC 5322 validation is intentionally not performed.
func ParseEmail(raw string) (Email, error) {
	if !utf8.ValidString(raw) {
		return "", apperr.New(apperr.InvalidEmail, "email must be valid UTF-8")
	}
	s := strings.ToLower(strings.TrimSpace(raw))
	if len(s) < minEmailLen || len(s) > maxEmailLen {
		return "", apperr.New(apperr.InvalidEmail, "email length must be 3..254 bytes")
	}
	if strings.IndexFunc(s, unicode.IsSpace) >= 0 {
		return "", apperr.New(apperr.InvalidEmail, "email must not contain whitespace")
	}
	if strings.Count(s, "@") != 1 {
		return "", apperr.New(apperr.InvalidEmail, "email must contain exactly one @")
	}
	local, domain, _ := strings.Cut(s, "@")
	if local == "" || domain == "" {
		return "", apperr.New(apperr.InvalidEmail, "email local and domain parts must not be empty")
	}
	return Email(s), nil
}

// String returns the normalized address.
func (e Email) String() string { return string(e) }
