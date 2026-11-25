// Command eccheck verifies that every edge case (EC) in the specification has
// automated tests, and that every test marker in the code is declared.
//
// It uses only the standard library. Run it from backend/:
//
//	go run ./tools/eccheck [-soft] [-markdown]
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	ecIDRe     = regexp.MustCompile(`^EC-\d{2,}$`)
	testIDRe   = regexp.MustCompile(`T-[A-Z]+-\d+`)
	goMarkerRe = regexp.MustCompile(`^\s*// Test-ID: (T-[A-Z]+-\d+)\s*$`)
	// First string argument of it(...), test(...), describe(...) and their
	// modifiers (it.each, test.skip, ...): one alternative per quote style.
	webCallRe  = regexp.MustCompile("\\b(?:it|test|describe)(?:\\.\\w+)*\\(\\s*(?:\"([^\"]*)\"|'([^']*)'|`([^`]*)`)")
	webTagRe   = regexp.MustCompile(`\[(T-[A-Z]+-\d+)\]`)
	declTestRe = regexp.MustCompile(`^\|\s*(T-[A-Z]+-\d+)\s*\|`)
)

// scenarioTests have no EC and do not take part in the EC matrix (spec 11, section 2).
// They are declared by the test catalogue (a line mentioning "Сценарн...").
var scenarioTests = []string{"T-I-50", "T-A-30"}

// unlinkedOK lists test groups that are legitimately not tied to any EC.
var unlinkedOK = []string{"T-CFG", "T-HEALTH", "T-ARCH", "T-CI"}

var skipDirs = map[string]bool{"node_modules": true, "testdata": true, "gen": true, ".git": true, "vendor": true}

// EC is one edge case parsed from the specification.
type EC struct {
	ID     string
	Tests  []string
	Stages []string
	Line   int
}

// Marker is a test marker found in code.
type Marker struct {
	ID   string
	File string
	Line int
}

// Status of one EC.
const (
	statusCovered = "covered"
	statusPartial = "partial"
	statusMissing = "missing"
)

// Report is the full analysis result.
type Report struct {
	ECs         []EC
	SpecErrors  []string
	Declared    map[string]bool // every declared T
	LinkedToEC  map[string]bool // T referenced by at least one EC
	Found       map[string][]Marker
	Undeclared  []Marker
	NoMarker    []string // declared T without any marker (strict only reports these)
	ECStatus    map[string]string
	Unlinked    []string // implemented T not linked to any EC
	UnlinkedBad []string // subset of Unlinked outside the allowed groups
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, errViolations) {
			fmt.Fprintln(os.Stderr, "eccheck:", err)
		}
		os.Exit(1)
	}
}

var errViolations = errors.New("violations found")

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("eccheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	spec := flags.String("spec", "../docs/spec/07_edge_cases.md", "path to the edge-case catalogue")
	testsSpec := flags.String("tests", "../docs/spec/08_testing.md", "path to the test catalogue (optional source of declared tests)")
	matrix := flags.String("matrix", "../docs/spec/11_coverage.md", "path to the coverage matrix (optional source of stages)")
	roots := flags.String("roots", ".,../web/src,../web/e2e", "comma-separated directories searched for markers")
	soft := flags.Bool("soft", false, "print the report and exit 0 (spec errors are still fatal)")
	markdown := flags.Bool("markdown", false, "print the coverage matrix as a Markdown table")
	if err := flags.Parse(args); err != nil {
		return err
	}

	ecs, specErrs, err := parseSpec(*spec)
	if err != nil {
		return err
	}
	stages := parseMatrix(*matrix)
	for i := range ecs {
		ecs[i].Stages = stages[ecs[i].ID]
	}
	declared := parseDeclared(*testsSpec)
	var markers []Marker
	for _, root := range strings.Split(*roots, ",") {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		m, err := scanRoot(root)
		if err != nil {
			return err
		}
		markers = append(markers, m...)
	}

	rep := analyze(ecs, specErrs, declared, markers)
	if *markdown {
		writeMarkdown(stdout, rep)
	} else {
		writeSummary(stdout, rep)
	}

	if len(rep.SpecErrors) > 0 {
		return fmt.Errorf("%d specification error(s)", len(rep.SpecErrors))
	}
	if *soft {
		return nil
	}
	if rep.strictViolations() > 0 {
		fmt.Fprintf(stderr, "eccheck: strict mode: %d violation(s)\n", rep.strictViolations())
		return errViolations
	}
	return nil
}

// parseSpec reads EC rows: "| EC-NN | ... | T-X-NN, T-Y-NN |".
func parseSpec(path string) ([]EC, []string, error) {
	f, err := os.Open(path) //nolint:gosec // path is a CLI flag of a developer tool
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()

	var ecs []EC
	var errs []string
	seen := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if !strings.HasPrefix(line, "| EC-") {
			continue
		}
		cols := splitRow(line)
		if len(cols) < 2 {
			errs = append(errs, fmt.Sprintf("%s:%d: malformed row", path, n))
			continue
		}
		id := cols[0]
		if !ecIDRe.MatchString(id) {
			errs = append(errs, fmt.Sprintf("%s:%d: bad EC id %q", path, n, id))
			continue
		}
		if first, dup := seen[id]; dup {
			errs = append(errs, fmt.Sprintf("%s:%d: duplicate %s (first at line %d)", path, n, id, first))
			continue
		}
		seen[id] = n
		tests := uniqueSorted(testIDRe.FindAllString(cols[len(cols)-1], -1))
		if len(tests) == 0 {
			errs = append(errs, fmt.Sprintf("%s:%d: %s has no tests", path, n, id))
		}
		ecs = append(ecs, EC{ID: id, Tests: tests, Line: n})
	}
	return ecs, errs, sc.Err()
}

// splitRow splits a Markdown table row into trimmed cells.
func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// parseMatrix reads "| EC-NN | tests | stages |" rows of the coverage matrix.
// A missing file is not an error: stages are informational.
func parseMatrix(path string) map[string][]string {
	out := map[string][]string{}
	f, err := os.Open(path) //nolint:gosec // path is a CLI flag of a developer tool
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "| EC-") {
			continue
		}
		cols := splitRow(line)
		if len(cols) != 3 {
			continue
		}
		for _, s := range strings.Split(cols[2], ",") {
			if s = strings.TrimSpace(s); s != "" {
				out[cols[0]] = append(out[cols[0]], s)
			}
		}
	}
	return out
}

// parseDeclared reads "| T-X-NN | description |" rows of the test catalogue.
// A missing file is not an error.
func parseDeclared(path string) map[string]bool {
	out := map[string]bool{}
	f, err := os.Open(path) //nolint:gosec // path is a CLI flag of a developer tool
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := declTestRe.FindStringSubmatch(line); m != nil {
			out[m[1]] = true
		}
		if strings.Contains(line, "Сценарн") { // scenario tests are described in prose
			for _, t := range testIDRe.FindAllString(line, -1) {
				out[t] = true
			}
		}
	}
	return out
}

// scanRoot walks root and collects markers. The root itself is always entered
// (self-test fixtures live under testdata); skipped directories apply below it.
func scanRoot(root string) ([]Marker, error) {
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // e.g. web/ does not exist yet
		}
		return nil, err
	}
	var out []Marker
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		switch {
		case strings.HasSuffix(name, "_test.go"):
			m, err := scanGo(path)
			if err != nil {
				return err
			}
			out = append(out, m...)
		case strings.HasSuffix(name, ".test.ts"), strings.HasSuffix(name, ".test.tsx"), strings.HasSuffix(name, ".spec.ts"):
			m, err := scanWeb(path)
			if err != nil {
				return err
			}
			out = append(out, m...)
		}
		return nil
	})
	return out, err
}

func scanLines(path string, fn func(line string, n int)) error {
	f, err := os.Open(path) //nolint:gosec // walked from a CLI-provided root
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; sc.Scan(); n++ {
		fn(sc.Text(), n)
	}
	return sc.Err()
}

func scanGo(path string) ([]Marker, error) {
	var out []Marker
	err := scanLines(path, func(line string, n int) {
		if m := goMarkerRe.FindStringSubmatch(line); m != nil {
			out = append(out, Marker{ID: m[1], File: path, Line: n})
		}
	})
	return out, err
}

func scanWeb(path string) ([]Marker, error) {
	var out []Marker
	err := scanLines(path, func(line string, n int) {
		for _, call := range webCallRe.FindAllStringSubmatch(line, -1) {
			title := call[1] + call[2] + call[3]
			for _, t := range webTagRe.FindAllStringSubmatch(title, -1) {
				out = append(out, Marker{ID: t[1], File: path, Line: n})
			}
		}
	})
	return out, err
}

func analyze(ecs []EC, specErrs []string, declaredExtra map[string]bool, markers []Marker) *Report {
	r := &Report{
		ECs:        ecs,
		SpecErrors: specErrs,
		Declared:   map[string]bool{},
		LinkedToEC: map[string]bool{},
		Found:      map[string][]Marker{},
		ECStatus:   map[string]string{},
	}
	for _, ec := range ecs {
		for _, t := range ec.Tests {
			r.Declared[t] = true
			r.LinkedToEC[t] = true
		}
	}
	for t := range declaredExtra {
		r.Declared[t] = true
	}
	for _, m := range markers {
		if !r.Declared[m.ID] {
			r.Undeclared = append(r.Undeclared, m)
			continue
		}
		r.Found[m.ID] = append(r.Found[m.ID], m)
	}
	for _, ec := range ecs {
		found := 0
		for _, t := range ec.Tests {
			if len(r.Found[t]) > 0 {
				found++
			}
		}
		switch {
		case len(ec.Tests) > 0 && found == len(ec.Tests):
			r.ECStatus[ec.ID] = statusCovered
		case found > 0:
			r.ECStatus[ec.ID] = statusPartial
		default:
			r.ECStatus[ec.ID] = statusMissing
		}
	}
	for _, t := range sortedKeys(r.Declared) {
		if len(r.Found[t]) == 0 {
			r.NoMarker = append(r.NoMarker, t)
		}
	}
	for _, t := range sortedFoundKeys(r.Found) {
		if r.LinkedToEC[t] {
			continue
		}
		r.Unlinked = append(r.Unlinked, t)
		if !isUnlinkedOK(t) {
			r.UnlinkedBad = append(r.UnlinkedBad, t)
		}
	}
	return r
}

func isUnlinkedOK(t string) bool {
	for _, g := range scenarioTests {
		if t == g {
			return true
		}
	}
	for _, p := range unlinkedOK {
		if strings.HasPrefix(t, p+"-") {
			return true
		}
	}
	return false
}

// strictViolations counts what makes strict mode fail.
func (r *Report) strictViolations() int {
	n := len(r.Undeclared) + len(r.NoMarker)
	for _, ec := range r.ECs {
		if r.ECStatus[ec.ID] != statusCovered {
			n++
		}
	}
	return n
}

func (r *Report) countStatus(s string) int {
	n := 0
	for _, ec := range r.ECs {
		if r.ECStatus[ec.ID] == s {
			n++
		}
	}
	return n
}

func writeSummary(w io.Writer, r *Report) {
	implemented := 0
	for t := range r.Declared {
		if len(r.Found[t]) > 0 {
			implemented++
		}
	}
	fmt.Fprintf(w, "EC total:            %d\n", len(r.ECs))
	fmt.Fprintf(w, "  covered:           %d\n", r.countStatus(statusCovered))
	fmt.Fprintf(w, "  partial:           %d\n", r.countStatus(statusPartial))
	fmt.Fprintf(w, "  planned (missing): %d\n", r.countStatus(statusMissing))
	fmt.Fprintf(w, "  violations:        %d\n", len(r.Undeclared)+len(r.SpecErrors))
	fmt.Fprintf(w, "T declared:          %d (referenced by EC: %d)\n", len(r.Declared), len(r.LinkedToEC))
	fmt.Fprintf(w, "T implemented:       %d\n", implemented)
	fmt.Fprintf(w, "T planned:           %d\n", len(r.NoMarker))

	fmt.Fprintf(w, "\nNot yet covered EC by stage (an EC counts under each of its stages, spec 11):\n")
	byStage := map[string]int{}
	for _, ec := range r.ECs {
		if r.ECStatus[ec.ID] == statusCovered {
			continue
		}
		if len(ec.Stages) == 0 {
			byStage["unknown"]++
		}
		for _, st := range ec.Stages {
			byStage[st]++
		}
	}
	for _, k := range sortedIntKeys(byStage) {
		fmt.Fprintf(w, "  %-8s %d\n", k, byStage[k])
	}

	fmt.Fprintf(w, "\nViolations (markers without a declaration in the spec): %d\n", len(r.Undeclared))
	for _, m := range r.Undeclared {
		fmt.Fprintf(w, "  ERROR %s at %s:%d\n", m.ID, m.File, m.Line)
	}
	for _, e := range r.SpecErrors {
		fmt.Fprintf(w, "  SPEC ERROR %s\n", e)
	}

	fmt.Fprintf(w, "\nImplemented tests not linked to any EC: %d\n", len(r.Unlinked))
	for _, t := range r.Unlinked {
		note := "allowed (infrastructure or scenario test)"
		if !isUnlinkedOK(t) {
			note = "WARNING: not linked to any EC, possible typo"
		}
		fmt.Fprintf(w, "  %s  %s\n", t, note)
	}

	fmt.Fprintf(w, "\nEC status:\n")
	var cov, part []string
	for _, ec := range r.ECs {
		switch r.ECStatus[ec.ID] {
		case statusCovered:
			cov = append(cov, ec.ID)
		case statusPartial:
			part = append(part, ec.ID)
		}
	}
	fmt.Fprintf(w, "  covered: %s\n", joinOrNone(cov))
	fmt.Fprintf(w, "  partial: %s\n", joinOrNone(part))
}

func writeMarkdown(w io.Writer, r *Report) {
	fmt.Fprintln(w, "| EC | Tests | Stages | Status |")
	fmt.Fprintln(w, "|---|---|---|---|")
	for _, ec := range r.ECs {
		stages := strings.Join(ec.Stages, ", ")
		if stages == "" {
			stages = "-"
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s |\n", ec.ID, strings.Join(ec.Tests, ", "), stages, r.ECStatus[ec.ID])
	}
}

func joinOrNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedFoundKeys(m map[string][]Marker) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedIntKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
