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

func isStdlib(p string) bool {
	first, _, _ := strings.Cut(p, "/")
	return !strings.Contains(first, ".")
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
				allowed := isStdlib(imp) ||
					hasPathPrefix(imp, "golang.org/x/text") ||
					imp == "github.com/google/uuid" ||
					hasPathPrefix(imp, internal+"domain")
				if !allowed || imp == "context" {
					add(rel, "domain must not import %s", imp)
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
