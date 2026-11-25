package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// floatViolations reports float32/float64 identifiers and float literals in
// domain and usecase packages (money is integer minor units).
func floatViolations(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, gf := range parseTree(t, root) {
		if !inDomainOrUsecase(gf.rel) {
			continue
		}
		ast.Inspect(gf.file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if x.Name == "float32" || x.Name == "float64" {
					out = append(out, fmt.Sprintf("%s: uses %s", gf.rel, x.Name))
				}
			case *ast.BasicLit:
				if x.Kind == token.FLOAT {
					out = append(out, fmt.Sprintf("%s: float literal %s", gf.rel, x.Value))
				}
			}
			return true
		})
	}
	return out
}

// Test-ID: T-ARCH-05
func TestNoFloatInDomainAndUseCase(t *testing.T) {
	if len(parseTree(t, "..")) == 0 {
		t.Fatal("no Go files scanned: the rule would pass vacuously")
	}
	if v := floatViolations(t, ".."); len(v) > 0 {
		t.Errorf("float in domain/usecase:\n%s", strings.Join(v, "\n"))
	}
}

// Test-ID: T-ARCH-05
func TestNoFloatDetectsViolations(t *testing.T) {
	got := floatViolations(t, filepath.Join("testdata", "float"))
	want := []string{
		"internal/domain/bad/bad.go: uses float64",
		"internal/domain/bad/bad.go: uses float32",
		"internal/domain/bad/bad.go: float literal 1.5",
		"internal/usecase/bad/bad.go: uses float64",
	}
	if len(got) != len(want) {
		t.Fatalf("want %d violations, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("violation %d: want %q, got %q", i, want[i], got[i])
		}
	}
}
