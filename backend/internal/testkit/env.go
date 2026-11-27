package testkit

import (
	"os"
	"strings"
	"testing"
)

// ClearConfigEnv removes every TB_* variable from the environment for the duration of the
// test and restores them afterwards. Tests of configuration defaults call it first: the
// Makefile exports .env and keys/cursor.env into the test processes, so a developer who has
// run `make keys` would otherwise see a different configuration than the CI runner does.
func ClearConfigEnv(t testing.TB) {
	t.Helper()
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, "TB_") {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("clear %s: %v", name, err)
		}
		t.Cleanup(func() { _ = os.Setenv(name, value) })
	}
}
