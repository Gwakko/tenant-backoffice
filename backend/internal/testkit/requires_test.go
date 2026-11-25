package testkit_test

import (
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

// fatalRecorder stands in for *testing.T: Fatal records the message and stops
// the calling goroutine the way the real one does.
type fatalRecorder struct {
	testing.TB
	mu     sync.Mutex
	failed bool
	msg    string
}

func (f *fatalRecorder) Helper() {}

func (f *fatalRecorder) Fatal(args ...any) {
	f.mu.Lock()
	f.failed = true
	if len(args) > 0 {
		if s, ok := args[0].(string); ok {
			f.msg = s
		}
	}
	f.mu.Unlock()
	runtime.Goexit()
}

// Test-ID: T-CI-04
func TestRequiresAdminURL(t *testing.T) {
	t.Setenv("TB_TEST_DB_ADMIN_URL", "")

	for name, fn := range map[string]func(testing.TB) *testkit.DB{
		"NewDB":      testkit.NewDB,
		"NewEmptyDB": testkit.NewEmptyDB,
	} {
		t.Run(name, func(t *testing.T) {
			rec := &fatalRecorder{}
			done := make(chan struct{})
			go func() {
				defer close(done)
				fn(rec)
			}()
			<-done
			require.True(t, rec.failed, "must fail, not skip, without the admin URL")
			require.Contains(t, rec.msg, "TB_TEST_DB_ADMIN_URL")
		})
	}
}
