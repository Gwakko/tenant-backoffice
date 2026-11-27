package report_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/report"
)

func TestNewSum(t *testing.T) {
	for _, ok := range []string{"0", "7", "-7", "9223372036854775807", "27670116110564327421", "-27670116110564327424"} {
		s, err := report.NewSum(ok)
		require.NoError(t, err, ok)
		require.Equal(t, ok, s.String())
	}
	for _, bad := range []string{"", "-", "-0", "-0x", "01", "+1", "1.0", " 1", "1e3", "--1", "1 ", "NaN"} {
		_, err := report.NewSum(bad)
		require.Error(t, err, bad)
	}
}

func TestParseDate(t *testing.T) {
	d, err := report.ParseDate("2024-02-29")
	require.NoError(t, err)
	require.Equal(t, "2024-02-29", d.Time().Format("2006-01-02"))
	for _, bad := range []string{"", "2025-02-29", "2026-2-1", "2026-01-32", "20260101", "2026-01-01T00:00:00Z", " 2026-01-01", "0000-01-01", "2026/01/01"} {
		_, err := report.ParseDate(bad)
		require.ErrorIs(t, err, apperr.InvalidPeriodError(), bad)
	}
}

func TestNewRangeBoundaries(t *testing.T) {
	ok := [][2]string{
		{"2024-01-01", "2025-01-01"}, // 366 days: the longest period
		{"2025-01-01", "2026-01-02"}, // 366 days across a non-leap year
		{"2026-06-01", "2026-06-02"}, // one day
	}
	for _, c := range ok {
		r, err := report.NewRange(c[0], c[1])
		require.NoError(t, err, c)
		require.Equal(t, report.Date(c[1]), r.To)
	}
	bad := [][2]string{
		{"2024-01-01", "2025-01-02"}, // 367 days
		{"2025-01-01", "2026-01-03"},
		{"2026-06-01", "2026-06-01"}, // empty
		{"2026-06-02", "2026-06-01"}, // reversed
		{"2026-06-01", "bad"},
		{"bad", "2026-06-02"},
	}
	for _, c := range bad {
		_, err := report.NewRange(c[0], c[1])
		require.ErrorIs(t, err, apperr.InvalidPeriodError(), c)
	}
}

func TestParseTZ(t *testing.T) {
	for _, ok := range []string{"UTC", "Europe/Kyiv", "America/New_York", "Asia/Kolkata"} {
		_, err := report.ParseTZ(ok)
		require.NoError(t, err, ok)
	}
	for _, bad := range []string{"", "Local", "Foo/Bar", "../etc/passwd", strings.Repeat("a", 65), "utc+3"} {
		_, err := report.ParseTZ(bad)
		require.ErrorIs(t, err, apperr.InvalidPeriodError(), bad)
	}
}

func TestParseGranularity(t *testing.T) {
	g, err := report.ParseGranularity("")
	require.NoError(t, err)
	require.Equal(t, report.Day, g)
	for _, ok := range []report.Granularity{report.Day, report.Week, report.Month} {
		g, err := report.ParseGranularity(string(ok))
		require.NoError(t, err)
		require.Equal(t, ok, g)
	}
	for _, bad := range []string{"year", "Day", "hour", "days"} {
		_, err := report.ParseGranularity(bad)
		require.ErrorIs(t, err, apperr.InvalidPeriodError(), bad)
	}
}

func TestParseKinds(t *testing.T) {
	k, err := report.ParseKinds([]string{"credit", "credit", "transfer_in"})
	require.NoError(t, err)
	require.Len(t, k, 2)
	k, err = report.ParseKinds(nil)
	require.NoError(t, err)
	require.Empty(t, k)
	_, err = report.ParseKinds([]string{"credit", "refund"})
	require.ErrorIs(t, err, apperr.New(apperr.ValidationFailed, ""))
}
