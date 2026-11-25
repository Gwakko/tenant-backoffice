package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureArgs points eccheck at one self-test fixture under testdata/.
func fixtureArgs(name string, extra ...string) []string {
	dir := filepath.Join("testdata", name)
	args := []string{
		"-spec", filepath.Join(dir, "spec.md"),
		"-tests", filepath.Join(dir, "tests.md"),
		"-matrix", filepath.Join(dir, "absent.md"),
		"-roots", filepath.Join(dir, "code"),
	}
	return append(args, extra...)
}

func runFixture(t *testing.T, name string, extra ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = run(fixtureArgs(name, extra...), &out, &errOut)
	return out.String(), errOut.String(), err
}

// Test-ID: T-CI-05
func TestStrictModePassesOnCorrectSet(t *testing.T) {
	out, _, err := runFixture(t, "ok")
	require.NoError(t, err)
	assert.Contains(t, out, "EC total:            2")
	assert.Contains(t, out, "covered:           2")
	assert.Contains(t, out, "T-CFG-01  allowed")
}

// Test-ID: T-CI-05
func TestStrictModeFailsOnUncoveredEC(t *testing.T) {
	out, stderr, err := runFixture(t, "uncovered")
	require.ErrorIs(t, err, errViolations)
	assert.Contains(t, stderr, "strict mode")
	assert.Contains(t, out, "covered:           1")
	assert.Contains(t, out, "partial:           1")
	assert.Contains(t, out, "planned (missing): 1")
}

// Test-ID: T-CI-05
func TestStrictModeFailsOnDeclaredTestWithoutMarker(t *testing.T) {
	out, _, err := runFixture(t, "notest")
	require.ErrorIs(t, err, errViolations)
	assert.Contains(t, out, "T planned:           1")
}

// Test-ID: T-CI-05
func TestUndeclaredMarkersAreReported(t *testing.T) {
	out, _, err := runFixture(t, "undeclared")
	require.ErrorIs(t, err, errViolations)
	assert.Contains(t, out, "ERROR T-U-99")
	assert.Contains(t, out, "ERROR T-W-77")
}

// Test-ID: T-CI-05
func TestSoftModeReportsWithoutFailing(t *testing.T) {
	for _, name := range []string{"uncovered", "notest", "undeclared"} {
		out, _, err := runFixture(t, name, "-soft")
		require.NoError(t, err, name)
		assert.Contains(t, out, "EC total:", name)
	}
}

// Test-ID: T-CI-05
func TestSpecErrorsAreFatalEvenInSoftMode(t *testing.T) {
	out, _, err := runFixture(t, "speckerr", "-soft")
	require.Error(t, err)
	require.False(t, errors.Is(err, errViolations))
	assert.Contains(t, out, "duplicate EC-01")
	assert.Contains(t, out, "EC-02 has no tests")
	assert.Contains(t, out, `bad EC id "EC-X"`)
}

// Test-ID: T-CI-05
func TestMarkdownReportHasStatusColumn(t *testing.T) {
	out, _, err := runFixture(t, "uncovered", "-soft", "-markdown")
	require.NoError(t, err)
	assert.Contains(t, out, "| EC | Tests | Stages | Status |")
	assert.Contains(t, out, "| EC-01 | T-U-01 | - | covered |")
	assert.Contains(t, out, "| EC-02 | T-U-02, T-W-01 | - | partial |")
	assert.Contains(t, out, "| EC-03 | T-I-01 | - | missing |")
}

// Test-ID: T-CI-05
func TestWebMarkerOnlyInFirstArgumentOfTestCall(t *testing.T) {
	m, err := scanWeb(filepath.Join("testdata", "ok", "code", "a.test.ts"))
	require.NoError(t, err)
	require.Len(t, m, 1)
	assert.Equal(t, "T-W-01", m[0].ID)
}

func TestMissingRootIsIgnored(t *testing.T) {
	m, err := scanRoot(filepath.Join("testdata", "no-such-dir"))
	require.NoError(t, err)
	assert.Empty(t, m)
}
