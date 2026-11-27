//go:build integration

package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	drep "github.com/Gwakko/tenant-backoffice/backend/internal/domain/report"
)

func utc(s string) time.Time {
	t, err := time.Parse("2006-01-02T15:04:05.999999Z", s)
	if err != nil {
		panic(err)
	}
	return t
}

func countsOf(series []drep.PeriodTotals) map[string]int64 {
	m := map[string]int64{}
	for _, s := range series {
		m[s.PeriodStart.String()] = s.Count
	}
	return m
}

// Test-ID: T-I-32
// EC-88: the day boundary is the local midnight of the zone; the window is
// half-open [from 00:00, to 00:00) in that zone.
func TestDayBoundaryInTimezone(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	c := w.reportCases()

	// 22:30 UTC on 2026-06-10 is 01:30 on the 11th in Kyiv (UTC+3 in summer).
	l.credit("A1u1", 10, utc("2026-06-10T22:30:00Z"))
	p := period{from: "2026-06-09", to: "2026-06-13", gran: "day"}
	p.tz = "Europe/Kyiv"
	require.Equal(t, map[string]int64{"2026-06-09": 0, "2026-06-10": 0, "2026-06-11": 1, "2026-06-12": 0},
		countsOf(c.requireSummary(l, "ownerRoot", "A1", p).Series))
	p.tz = "UTC"
	require.Equal(t, map[string]int64{"2026-06-09": 0, "2026-06-10": 1, "2026-06-11": 0, "2026-06-12": 0},
		countsOf(c.requireSummary(l, "ownerRoot", "A1", p).Series))

	// The very edges: local midnight itself belongs to the new day.
	l.credit("A1u1", 1, utc("2026-06-10T21:00:00Z"))        // 00:00 on the 11th in Kyiv
	l.credit("A1u1", 2, utc("2026-06-10T20:59:59.999999Z")) // 23:59:59.999999 on the 10th
	l.credit("A1u1", 4, utc("2026-06-11T06:59:59.999999Z")) // 23:59:59.999999 on the 10th in Los Angeles (UTC-7)
	l.credit("A1u1", 8, utc("2026-06-11T07:00:00Z"))        // 00:00 on the 11th in Los Angeles

	for _, tz := range []string{"Europe/Kyiv", "America/Los_Angeles", "UTC", "Asia/Kolkata", "Pacific/Auckland"} {
		// One day windows on both sides of the midnight, and a wider one.
		for _, win := range [][2]string{{"2026-06-10", "2026-06-11"}, {"2026-06-11", "2026-06-12"}, {"2026-06-09", "2026-06-13"}} {
			c.requireSummary(l, "ownerRoot", "A1", period{from: win[0], to: win[1], tz: tz, gran: "day"})
		}
	}
	kyiv := period{from: "2026-06-10", to: "2026-06-11", tz: "Europe/Kyiv", gran: "day"}
	out, err := c.summaryOf("ownerRoot", "A1", kyiv)
	require.NoError(t, err)
	require.Equal(t, int64(1), out.Totals.Count, "[10th 00:00, 11th 00:00) in Kyiv holds only 20:59:59.999999Z")
	require.Equal(t, drep.Sum("2"), out.Totals.Credits)
	kyiv.from, kyiv.to = "2026-06-11", "2026-06-12"
	out, err = c.summaryOf("ownerRoot", "A1", kyiv)
	require.NoError(t, err)
	require.Equal(t, drep.Sum("23"), out.Totals.Credits, "all of 21:00Z, 22:30Z, 06:59Z and 07:00Z lie in [21:00Z on the 10th, 21:00Z on the 11th)")
}

// Test-ID: T-I-33
// EC-89: the days of the change to and from summer time have 23 and 25 hours;
// an operation each hour is neither lost nor counted twice; weeks start on
// Monday.
func TestDSTDays(t *testing.T) {
	w := newWorld(t)
	l := w.newLedger()
	c := w.reportCases()

	hourly := func(from, to string) {
		for at := day(from); at.Before(day(to)); at = at.Add(time.Hour) {
			l.credit("A1u1", 1, at)
		}
	}
	hourly("2026-03-27", "2026-04-02")
	hourly("2026-10-23", "2026-10-28")

	spring := period{from: "2026-03-28", to: "2026-03-31", tz: "Europe/Kyiv", gran: "day"}
	out := c.requireSummary(l, "ownerRoot", "A1", spring)
	require.Equal(t, map[string]int64{"2026-03-28": 24, "2026-03-29": 23, "2026-03-30": 24}, countsOf(out.Series))
	require.Equal(t, int64(71), out.Totals.Count)

	autumn := period{from: "2026-10-24", to: "2026-10-27", tz: "Europe/Kyiv", gran: "day"}
	out = c.requireSummary(l, "ownerRoot", "A1", autumn)
	require.Equal(t, map[string]int64{"2026-10-24": 24, "2026-10-25": 25, "2026-10-26": 24}, countsOf(out.Series))
	require.Equal(t, int64(73), out.Totals.Count)

	// The same hours in UTC are always 24 per day.
	spring.tz = "UTC"
	out = c.requireSummary(l, "ownerRoot", "A1", spring)
	require.Equal(t, map[string]int64{"2026-03-28": 24, "2026-03-29": 24, "2026-03-30": 24}, countsOf(out.Series))

	// Weeks: ISO, the period starts on a Monday, also when the window does not.
	for _, tz := range []string{"Europe/Kyiv", "UTC", "America/New_York"} {
		wk := period{from: "2026-03-26", to: "2026-04-01", tz: tz, gran: "week"}
		out = c.requireSummary(l, "ownerRoot", "A1", wk)
		require.Len(t, out.Series, 2, tz)
		for _, s := range out.Series {
			require.Equal(t, time.Monday, s.PeriodStart.Time().Weekday(), "%s %s", tz, s.PeriodStart)
		}
		require.Equal(t, drep.Date("2026-03-23"), out.Series[0].PeriodStart)
		require.Equal(t, drep.Date("2026-03-30"), out.Series[1].PeriodStart)
	}
	// Months: the first of the month.
	mo := period{from: "2026-03-28", to: "2026-04-03", tz: "Europe/Kyiv", gran: "month"}
	out = c.requireSummary(l, "ownerRoot", "A1", mo)
	require.Equal(t, []drep.Date{"2026-03-01", "2026-04-01"}, []drep.Date{out.Series[0].PeriodStart, out.Series[1].PeriodStart})
}
