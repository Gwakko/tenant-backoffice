package archtest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// requiredForbidden are the forbidigo patterns that make T-ARCH-02 true: the environment is read only
// by the config package, there is no stdout or std-log printing, and os.Exit stays in cmd and tools.
var requiredForbidden = []string{
	`^os\.(Getenv|LookupEnv)$`,
	`^fmt\.(Print|Printf|Println)$`,
	`^log\.(Print|Printf|Println|Fatal|Fatalf|Fatalln|Panic|Panicf|Panicln)$`,
	`^os\.Exit$`,
}

// envExclusionPaths are the only places allowed to call os.Getenv/os.LookupEnv.
var envExclusionPaths = map[string]bool{
	`internal/config/`:  true,
	`internal/testkit/`: true,
	`_test\.go`:         true,
}

var pathLine = regexp.MustCompile(`^\s*-\s*path:\s*(.+?)\s*$`)

// lintConfigViolations checks the golangci-lint configuration text against the T-ARCH-02 rules.
func lintConfigViolations(config string) []string {
	var out []string

	if !regexp.MustCompile(`(?m)^\s*-\s*forbidigo\s*$`).MatchString(config) {
		out = append(out, "forbidigo is not enabled")
	}
	for _, pattern := range requiredForbidden {
		if !strings.Contains(config, "pattern: "+pattern) {
			out = append(out, "missing forbidden pattern "+pattern)
		}
	}

	// Exclusions that switch forbidigo off for environment access must stay limited to the known paths.
	lines := strings.Split(config, "\n")
	for i, line := range lines {
		m := pathLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		block := strings.Join(lines[i:min(i+4, len(lines))], "\n")
		if !strings.Contains(block, "forbidigo") || !strings.Contains(block, "Getenv") {
			continue
		}
		path := strings.Trim(m[1], `'"`)
		if !envExclusionPaths[path] {
			out = append(out, "environment access is excluded from forbidigo for an unexpected path: "+path)
		}
	}
	return out
}

// Test-ID: T-ARCH-02
func TestLintConfigForbidsEnvironmentAndPrinting(t *testing.T) {
	root := filepath.Join("..", "..", ".golangci.yml")
	data, err := os.ReadFile(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	if v := lintConfigViolations(string(data)); len(v) > 0 {
		t.Fatalf("lint configuration weakens T-ARCH-02:\n  %s", strings.Join(v, "\n  "))
	}
}

// The rule must catch a weakened configuration: a removed pattern and a broad exclusion.
func TestLintConfigCheckDetectsWeakening(t *testing.T) {
	good := "linters:\n  enable:\n    - forbidigo\n"
	for _, p := range requiredForbidden {
		good += "# pattern: " + p + "\n"
	}
	if v := lintConfigViolations(good); len(v) != 0 {
		t.Fatalf("a complete configuration must pass: %v", v)
	}

	noPrint := strings.Replace(good, "pattern: ^fmt\\.(Print|Printf|Println)$", "pattern: gone", 1)
	if v := lintConfigViolations(noPrint); len(v) != 1 || !strings.Contains(v[0], "fmt") {
		t.Fatalf("a removed fmt.Print pattern must be reported once, got %v", v)
	}

	broad := good + "exclusions:\n  - path: internal/\n    linters: [forbidigo]\n    text: 'os\\.(Getenv|LookupEnv)'\n"
	if v := lintConfigViolations(broad); len(v) != 1 || !strings.Contains(v[0], "internal/") {
		t.Fatalf("a broad exclusion must be reported once, got %v", v)
	}

	off := strings.Replace(good, "    - forbidigo\n", "", 1)
	if v := lintConfigViolations(off); len(v) == 0 {
		t.Fatal("a disabled forbidigo linter must be reported")
	}
}
