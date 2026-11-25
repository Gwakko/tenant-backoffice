package user_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// Test-ID: T-U-35
func TestPasswordLength(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"11 bytes", strings.Repeat("a", 11), false},
		{"12 bytes", strings.Repeat("a", 12), true},
		{"128 bytes", strings.Repeat("a", 128), true},
		{"129 bytes", strings.Repeat("a", 129), false},
		{"empty", "", false},
		{"6 two-byte runes = 12 bytes", strings.Repeat("é", 6), true},
		{"5 two-byte runes + 1 ascii = 11 bytes", strings.Repeat("é", 5) + "a", false},
		{"64 two-byte runes = 128 bytes", strings.Repeat("é", 64), true},
		{"65 two-byte runes = 130 bytes", strings.Repeat("é", 65), false},
		{"invalid utf8", strings.Repeat("a", 11) + "\xff", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := user.ValidatePassword(tc.in)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.True(t, errors.Is(err, apperr.New(apperr.InvalidPassword, "")))
		})
	}
}
