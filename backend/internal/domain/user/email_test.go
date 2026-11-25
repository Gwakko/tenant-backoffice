package user_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// Test-ID: T-U-34
func TestParseEmail(t *testing.T) {
	long := strings.Repeat("a", 242) + "@example.com" // 254 bytes
	valid := []struct{ in, want string }{
		{"  User@Example.COM ", "user@example.com"},
		{"a@b", "a@b"},
		{"USER+tag@Sub.Example.org", "user+tag@sub.example.org"},
		{long, long},
	}
	for _, tc := range valid {
		got, err := user.ParseEmail(tc.in)
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, got.String())
	}

	invalid := map[string]string{
		"empty":         "",
		"blank":         "   ",
		"no at":         "userexample.com",
		"two ats":       "a@@b",
		"two ats split": "a@b@c",
		"empty local":   "@example.com",
		"empty domain":  "user@",
		"only at":       "@",
		"too short":     "a@",
		"inner space":   "us er@example.com",
		"tab inside":    "user@exa\tmple.com",
		"too long":      long + "x",
		"invalid utf8":  "a\xff@b",
	}
	for name, in := range invalid {
		_, err := user.ParseEmail(in)
		require.Error(t, err, name)
		require.True(t, errors.Is(err, apperr.New(apperr.InvalidEmail, "")), name)
	}
}
