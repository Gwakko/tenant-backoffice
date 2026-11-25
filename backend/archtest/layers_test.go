package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/Gwakko/tenant-backoffice/backend"

// goFile is a parsed non-test Go file together with its path relative to the scan root.
type goFile struct {
	rel  string // slash-separated, relative to the scan root
	file *ast.File
}

// parseTree parses every non-test .go file below root. Directories named
// testdata, vendor or starting with "." or "_" are skipped, like the go tool does.
func parseTree(t *testing.T, root string) []goFile {
	t.Helper()
	var out []goFile
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			return perr
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		out = append(out, goFile{rel: filepath.ToSlash(rel), file: f})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out
}

func importsOf(f *ast.File) []string {
	var out []string
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err == nil {
			out = append(out, p)
		}
	}
	return out
}

// domainAllowed is the single allow-list of imports for internal/domain/**
// (non-test files). Anything not listed here is a T-ARCH-01 violation.
// To extend it, add the package here AND to the "domain" rule in
// .golangci.yml (depguard), and explain why in the comment next to it.
// Entries marked "reserved" are pure and expected by planned domain code
// (money, formatting) but not imported yet.
var domainAllowed = []string{
	// Standard library: pure packages only, no I/O and no infrastructure.
	"errors",       // sentinel and wrapped errors
	"fmt",          // error formatting (reserved: Errorf wrapping)
	"strings",      // name normalization and validation
	"strconv",      // number parsing and formatting (reserved)
	"unicode",      // character classes in name validation
	"unicode/utf8", // rune-length limits
	"time",         // time values passed as arguments (never time.Now in domain)
	"sort",         // deterministic ordering (reserved)
	"slices",       // slice helpers (reserved)
	"maps",         // map helpers (reserved)
	"math",         // overflow bounds for money.Amount (reserved)
	"cmp",          // ordering helpers (reserved)
	// External.
	"github.com/google/uuid",         // identifier type
	"golang.org/x/text/cases",        // case folding of names
	"golang.org/x/text/unicode/norm", // Unicode normalization of names
	// Internal: domain sub-packages may import each other.
	modulePath + "/internal/domain",
}

// domainImportAllowed reports whether imp is on the domain allow-list.
// Module and x/text entries match as path prefixes, the rest exactly.
func domainImportAllowed(imp string) bool {
	for _, a := range domainAllowed {
		if imp == a || (strings.Contains(a, ".") && hasPathPrefix(imp, a)) {
			return true
		}
	}
	return false
}

func hasPathPrefix(p, prefix string) bool {
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

// adapterOf returns the top-level adapter name for a path like
// "internal/adapter/postgres/x.go" ("postgres"), or "" if not an adapter file.
func adapterOf(rel string) string {
	const prefix = "internal/adapter/"
	if !strings.HasPrefix(rel, prefix) {
		return ""
	}
	name, _, _ := strings.Cut(strings.TrimPrefix(rel, prefix), "/")
	return name
}

// layered reports whether rel belongs to a package with its own import case in
// layerViolations or is allowed to import adapters (app, cmd).
func layered(rel string) bool {
	for _, p := range []string{"internal/domain/", "internal/usecase/", "internal/adapter/", "internal/app/", "cmd/"} {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

// layerViolations checks the layering rules of spec 00 section 5 against the
// tree rooted at root and returns "file: message" strings.
func layerViolations(t *testing.T, root string) []string {
	t.Helper()
	internal := modulePath + "/internal/"
	adapterPkg := internal + "adapter"
	var out []string
	add := func(rel, format string, args ...string) {
		msg := format
		for _, a := range args {
			msg = strings.Replace(msg, "%s", a, 1)
		}
		out = append(out, rel+": "+msg)
	}

	for _, gf := range parseTree(t, root) {
		rel := gf.rel
		for _, imp := range importsOf(gf.file) {
			inModule := hasPathPrefix(imp, modulePath)
			switch {
			case strings.HasPrefix(rel, "internal/domain/"):
				if !domainImportAllowed(imp) {
					add(rel, "domain must not import %s (not on the domain allow-list)", imp)
				}
			case strings.HasPrefix(rel, "internal/usecase/"):
				switch {
				case hasPathPrefix(imp, adapterPkg):
					add(rel, "usecase must not import adapter %s", imp)
				case hasPathPrefix(imp, internal+"app"):
					add(rel, "usecase must not import app %s", imp)
				case hasPathPrefix(imp, "github.com/jackc/pgx"):
					add(rel, "usecase must not import pgx %s", imp)
				case imp == "net/http":
					add(rel, "usecase must not import %s", imp)
				}
			case strings.HasPrefix(rel, "internal/adapter/"):
				if hasPathPrefix(imp, adapterPkg) {
					self := adapterPkg + "/" + adapterOf(rel)
					sameAdapter := hasPathPrefix(imp, self)
					httpapiGen := adapterOf(rel) == "httpapi" && imp == adapterPkg+"/httpapi/gen"
					if !sameAdapter && !httpapiGen {
						add(rel, "adapter must not import another adapter %s", imp)
					}
				}
			case strings.HasPrefix(rel, "cmd/"):
				if inModule && !hasPathPrefix(imp, internal+"app") && !hasPathPrefix(imp, internal+"config") {
					add(rel, "cmd may import only app and config, got %s", imp)
				}
			}
			// Only app and cmd may import adapters. domain and usecase are
			// covered by their own cases above; this catches every other package.
			if hasPathPrefix(imp, adapterPkg) && !layered(rel) {
				add(rel, "only app and cmd may import adapters, got %s", imp)
			}
		}
	}
	return out
}

// Test-ID: T-ARCH-01
func TestLayerImports(t *testing.T) {
	root := ".."
	files := parseTree(t, root)
	if len(files) == 0 {
		t.Fatal("no Go files scanned: the rule would pass vacuously")
	}
	if v := layerViolations(t, root); len(v) > 0 {
		t.Errorf("layer violations:\n%s", strings.Join(v, "\n"))
	}
}

// Test-ID: T-ARCH-01
func TestLayerImportsDetectsViolations(t *testing.T) {
	got := layerViolations(t, filepath.Join("testdata", "layers"))
	perFile := map[string]int{}
	for _, v := range got {
		file, _, _ := strings.Cut(v, ": ")
		perFile[file]++
	}
	want := map[string]int{
		"internal/domain/bad/bad.go":      3, // pgx, uuid-like foreign module, context
		"internal/domain/bad/sql.go":      1, // database/sql
		"internal/domain/bad/http.go":     1, // net/http
		"internal/domain/bad/os.go":       1, // os
		"internal/domain/bad/json.go":     1, // encoding/json
		"internal/domain/bad/adapter.go":  1, // adapter
		"internal/domain/bad/mixed.go":    2, // log/slog and crypto/sha256 flagged, strings fine
		"internal/usecase/bad/bad.go":     4, // adapter, app, pgx, net/http
		"internal/adapter/a/bad.go":       1, // another adapter
		"internal/adapter/httpapi/bad.go": 1, // httpapi -> postgres
		"internal/config/bad.go":          1, // config -> adapter
		"cmd/bad/main.go":                 1, // cmd -> usecase
	}
	for file, n := range want {
		if perFile[file] != n {
			t.Errorf("%s: want %d violations, got %d (all: %v)", file, n, perFile[file], got)
		}
	}
	// Anything flagged outside the expected set means a good fixture was rejected.
	for file := range perFile {
		if _, ok := want[file]; !ok {
			t.Errorf("unexpected violations in %s: %v", file, got)
		}
	}
}
