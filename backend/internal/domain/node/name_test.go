package node_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

// Test-ID: T-U-11
func TestNameWhitespace(t *testing.T) {
	n, err := node.ParseName("  Main   office\t")
	require.NoError(t, err)
	require.Equal(t, "Main office", n.String()) // EC-21

	n, err = node.ParseName("a\n\u00a0\u2003b")
	require.NoError(t, err)
	require.Equal(t, "a b", n.String())

	for _, s := range []string{"", "   ", "\t\n"} {
		_, err := node.ParseName(s)
		requireCode(t, err, apperr.InvalidName)
	}
}

// Test-ID: T-U-12
func TestNameUnicode(t *testing.T) {
	// EC-22: composed and decomposed forms are the same name.
	composed, err := node.ParseName("Caf\u00e9")
	require.NoError(t, err)
	decomposed, err := node.ParseName("Cafe\u0301")
	require.NoError(t, err)
	require.Equal(t, composed.String(), decomposed.String())
	require.Equal(t, composed.Key(), decomposed.Key())
	require.True(t, composed.Equal(decomposed))

	// Format (Cf) and control characters are rejected.
	for _, s := range []string{
		"a\u200bb",       // zero-width space
		"a\u202eb",       // right-to-left override
		"a\xef\xbb\xbfb", // BOM
		"a\x00b",         // NUL
		"a\u0007b",       // BEL
		"a\u200d",        // zero-width joiner
		"\u2066x\u2069",  // bidi isolates
	} {
		_, err := node.ParseName(s)
		requireCode(t, err, apperr.InvalidName)
	}

	// Invalid UTF-8.
	_, err = node.ParseName("a\xffb")
	requireCode(t, err, apperr.InvalidName)

	// Length is counted in runes, not bytes.
	_, err = node.ParseName(strings.Repeat("\u00e9", 100)) // 200 bytes
	require.NoError(t, err)
	_, err = node.ParseName(strings.Repeat("\u00e9", 101))
	requireCode(t, err, apperr.InvalidName)
	_, err = node.ParseName(strings.Repeat("\U0001F600", 100))
	require.NoError(t, err)
	_, err = node.ParseName(strings.Repeat("a", 101))
	requireCode(t, err, apperr.InvalidName)
}

func TestNameKeyFoldsCase(t *testing.T) {
	a := mustName(t, "Stra\u00dfe Main")
	b := mustName(t, "STRASSE main")
	require.Equal(t, a.Key(), b.Key())
	require.NotEqual(t, a.String(), b.String())
}

func TestPathHelpers(t *testing.T) {
	a, b := newID(), newID()
	p := node.Path{a}
	q := p.Append(b)
	require.Equal(t, node.Path{a}, p)
	require.Equal(t, node.Path{a, b}, q)
	require.True(t, q.Contains(b))
	require.False(t, p.Contains(b))
	require.Equal(t, node.Path{a}, q.Parent())
	require.Nil(t, p.Parent())
}
