package archtest

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"strings"
	"testing"
)

func inDomainOrUsecase(rel string) bool {
	return strings.HasPrefix(rel, "internal/domain/") || strings.HasPrefix(rel, "internal/usecase/")
}

// tagViolations reports struct fields with tags in domain and usecase packages.
func tagViolations(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, gf := range parseTree(t, root) {
		if !inDomainOrUsecase(gf.rel) {
			continue
		}
		ast.Inspect(gf.file, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, field := range st.Fields.List {
				if field.Tag != nil {
					out = append(out, fmt.Sprintf("%s: struct field has tag %s", gf.rel, field.Tag.Value))
				}
			}
			return true
		})
	}
	return out
}

// Test-ID: T-ARCH-03
func TestNoTagsInDomainAndUseCase(t *testing.T) {
	if len(parseTree(t, "..")) == 0 {
		t.Fatal("no Go files scanned: the rule would pass vacuously")
	}
	if v := tagViolations(t, ".."); len(v) > 0 {
		t.Errorf("tags in domain/usecase:\n%s", strings.Join(v, "\n"))
	}
}

// Test-ID: T-ARCH-03
func TestNoTagsDetectsViolations(t *testing.T) {
	got := tagViolations(t, filepath.Join("testdata", "tags"))
	want := []string{
		"internal/domain/bad/bad.go",
		"internal/domain/bad/bad.go", // anonymous struct
		"internal/usecase/bad/bad.go",
	}
	if len(got) != len(want) {
		t.Fatalf("want %d violations, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], w+":") {
			t.Errorf("violation %d: want file %s, got %s", i, w, got[i])
		}
	}
}
