package authn_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/authn"
)

// fakeClock is a settable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// writeKey generates a key pair and writes <kid>.pem into dir: the private key
// (PKCS#8), or only the public key when publicOnly is set.
func writeKey(t *testing.T, dir, kid string, publicOnly bool) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	var block *pem.Block
	if publicOnly {
		der, err := x509.MarshalPKIXPublicKey(pub)
		require.NoError(t, err)
		block = &pem.Block{Type: "PUBLIC KEY", Bytes: der}
	} else {
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		require.NoError(t, err)
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, kid+".pem"), pem.EncodeToMemory(block), 0o600))
	return priv
}

// testConfig is a valid configuration over a temporary key directory. Keys
// are generated in the test and never stored in the repository. The argon2
// parameters are small, to keep the tests fast.
func testConfig(dir, activeKID string) authn.Config {
	return authn.Config{
		KeysDir:         dir,
		ActiveKID:       activeKID,
		Issuer:          "tenant-backoffice",
		Audience:        "tenant-backoffice-api",
		AccessTTL:       10 * time.Minute,
		Leeway:          30 * time.Second,
		Argon2MemoryKiB: 16,
		Argon2Time:      1,
		Argon2Threads:   1,
		Argon2KeyLen:    32,
		Argon2SaltLen:   16,
	}
}

func removeKey(dir, kid string) error { return os.Remove(filepath.Join(dir, kid+".pem")) }

// writePublicOf writes the public half of priv as <kid>.pem.
func writePublicOf(t *testing.T, dir, kid string, priv ed25519.PrivateKey) {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(priv.Public())
	require.NoError(t, err)
	data := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	require.NoError(t, os.WriteFile(filepath.Join(dir, kid+".pem"), data, 0o600))
}
