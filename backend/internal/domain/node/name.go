package node

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

const maxNameRunes = 100

// Name is a normalized node name.
type Name struct{ v string }

// ParseName validates and normalizes a raw name: UTF-8, NFC, trimmed, inner
// whitespace collapsed to one space, no control or format (Cf) characters,
// 1..100 runes.
func ParseName(raw string) (Name, error) {
	if !utf8.ValidString(raw) {
		return Name{}, invalidName("name is not valid UTF-8")
	}
	s := norm.NFC.String(raw)
	s = strings.Join(strings.Fields(s), " ")
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return Name{}, invalidName("name contains control or format characters")
		}
	}
	n := utf8.RuneCountInString(s)
	if n < 1 || n > maxNameRunes {
		return Name{}, invalidName("name length must be 1..100 characters")
	}
	return Name{v: s}, nil
}

func invalidName(msg string) error {
	return apperr.New(apperr.InvalidName, msg).WithField("name", string(apperr.InvalidName))
}

// String returns the normalized name.
func (n Name) String() string { return n.v }

// Key returns the case-folded uniqueness key of the name.
func (n Name) Key() string {
	return strings.ToLower(norm.NFC.String(cases.Fold().String(n.v)))
}

// Equal reports whether two names are identical after normalization.
func (n Name) Equal(o Name) bool { return n.v == o.v }
