package authn_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/authn"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

func TestHasher(t *testing.T) {
	h, err := authn.NewHasher(testConfig("", ""))
	require.NoError(t, err)

	hash, err := h.Hash("correct horse battery")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(hash), "$argon2id$v=19$m=16,t=1,p=1$"))
	require.NotContains(t, string(hash), "correct horse")

	ok, err := h.Verify(hash, "correct horse battery")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = h.Verify(hash, "correct horse batterY")
	require.NoError(t, err)
	require.False(t, ok)

	again, err := h.Hash("correct horse battery")
	require.NoError(t, err)
	require.NotEqual(t, hash, again, "every hash has its own salt")

	t.Run("parameters come from the stored hash", func(t *testing.T) {
		cfg := testConfig("", "")
		cfg.Argon2MemoryKiB, cfg.Argon2Time = 32, 2
		stronger, err := authn.NewHasher(cfg)
		require.NoError(t, err)
		ok, err := stronger.Verify(hash, "correct horse battery")
		require.NoError(t, err)
		require.True(t, ok)
	})
	t.Run("malformed hashes are errors, not matches", func(t *testing.T) {
		for _, bad := range []user.PasswordHash{
			"", "plain", "$argon2i$v=19$m=16,t=1,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
			"$argon2id$v=18$m=16,t=1,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
			"$argon2id$v=19$m=16,t=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
			"$argon2id$v=19$m=999999999,t=1,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
			"$argon2id$v=19$m=16,t=1,p=1$!!$aGFzaGhhc2hoYXNoaGFzaA",
		} {
			ok, err := h.Verify(bad, "x")
			require.Error(t, err, string(bad))
			require.False(t, ok)
		}
	})
	t.Run("invalid parameters are rejected at construction", func(t *testing.T) {
		cfg := testConfig("", "")
		cfg.Argon2Time = 0
		_, err := authn.NewHasher(cfg)
		require.Error(t, err)
	})
	t.Run("owasp minimum parameters are accepted", func(t *testing.T) {
		cfg := testConfig("", "")
		cfg.Argon2MemoryKiB, cfg.Argon2Time = 19456, 2
		strong, err := authn.NewHasher(cfg)
		require.NoError(t, err)
		hash, err := strong.Hash("a-long-enough-password")
		require.NoError(t, err)
		require.Contains(t, string(hash), "m=19456,t=2,p=1")
	})
}

func TestRefreshCodec(t *testing.T) {
	c := authn.NewRefreshCodec()
	token, hash, err := c.Generate()
	require.NoError(t, err)
	require.Len(t, token, authn.RefreshTokenLen)
	require.Len(t, hash, 32)

	got, err := c.Hash(token)
	require.NoError(t, err)
	require.Equal(t, hash, got)

	other, _, err := c.Generate()
	require.NoError(t, err)
	require.NotEqual(t, token, other)

	for _, bad := range []string{"", "short", token + "A", strings.Repeat("!", 43), token[:42] + "="} {
		_, err := c.Hash(bad)
		requireCode(t, err, apperr.RefreshInvalid)
	}
}
