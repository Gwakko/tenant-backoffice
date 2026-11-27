// Package report is the model of ledger reports: the validated reporting
// period (dates, time zone, granularity), exact decimal sums and the totals
// the reports show. It does no arithmetic on money: sums are computed by the
// store in numeric and travel as decimal strings, so they cannot overflow.
package report

import (
	"fmt"
	"strings"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

// MaxPeriodDays is the longest reporting period: To minus From in days.
const MaxPeriodDays = 366

const maxTZLen = 64

// Sum is a signed integer amount in minor units as a string of decimal digits.
// It may exceed int64: totals of many operations are summed in numeric.
type Sum string

// NewSum checks that s is a signed decimal integer without leading zeros.
func NewSum(s string) (Sum, error) {
	digits := strings.TrimPrefix(s, "-")
	ok := digits != "" && (digits == "0" && s == "0" || digits[0] != '0')
	for i := 0; ok && i < len(digits); i++ {
		ok = digits[i] >= '0' && digits[i] <= '9'
	}
	if !ok {
		return "", fmt.Errorf("report: %q is not a decimal integer", s)
	}
	return Sum(s), nil
}

// String returns the digits.
func (s Sum) String() string { return string(s) }

// Date is a calendar date as "YYYY-MM-DD", local to the zone of the report.
type Date string

// ParseDate accepts exactly the form YYYY-MM-DD of a real calendar date.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil || t.Format(time.DateOnly) != s || t.Year() < 1 {
		return "", apperr.InvalidPeriodError()
	}
	return Date(s), nil
}

// String returns the YYYY-MM-DD text.
func (d Date) String() string { return string(d) }

// Time is midnight of the date as a plain calendar value (UTC is only the
// carrier). It is for date arithmetic and formatting, never for a boundary of
// a report: those are made by the store from the date and the zone name.
func (d Date) Time() time.Time {
	t, err := time.Parse(time.DateOnly, string(d))
	if err != nil {
		return time.Time{}
	}
	return t
}

// Range is the half-open period [From, To) of local dates.
type Range struct{ From, To Date }

// NewRange parses both dates and checks From < To and the length limit. Every
// failure is apperr.InvalidPeriod.
func NewRange(from, to string) (Range, error) {
	f, err := ParseDate(from)
	if err != nil {
		return Range{}, err
	}
	t, err := ParseDate(to)
	if err != nil {
		return Range{}, err
	}
	if f >= t {
		return Range{}, apperr.InvalidPeriodError()
	}
	days := int(t.Time().Sub(f.Time()) / (24 * time.Hour))
	if days > MaxPeriodDays {
		return Range{}, apperr.InvalidPeriodError()
	}
	return Range{From: f, To: t}, nil
}

// Granularity is the size of one period of a series.
type Granularity string

// Granularities. A week is an ISO week, it starts on Monday.
const (
	Day   Granularity = "day"
	Week  Granularity = "week"
	Month Granularity = "month"
)

// ParseGranularity accepts day, week or month; empty means day.
func ParseGranularity(s string) (Granularity, error) {
	switch g := Granularity(s); g {
	case "":
		return Day, nil
	case Day, Week, Month:
		return g, nil
	}
	return "", apperr.InvalidPeriodError()
}

// ParseTZ accepts an IANA zone name known to the runtime. The empty name and
// "Local" are rejected: Local depends on the host, the empty name means the
// effective display_timezone of the node, which the caller resolves first.
func ParseTZ(s string) (string, error) {
	if s == "" || s == "Local" || len(s) > maxTZLen {
		return "", apperr.InvalidPeriodError()
	}
	if _, err := time.LoadLocation(s); err != nil {
		return "", apperr.InvalidPeriodError()
	}
	return s, nil
}

// Period is a validated reporting period: [From, To) of local dates in TZ,
// cut into periods of Granularity.
type Period struct {
	Range
	TZ          string
	Granularity Granularity
}

// ParseKinds validates a repeatable kind filter and removes duplicates; an
// empty filter means every kind.
func ParseKinds(raw []string) ([]balance.Kind, error) {
	seen := map[balance.Kind]bool{}
	var out []balance.Kind
	for _, s := range raw {
		k, err := balance.ParseKind(s)
		if err != nil {
			return nil, apperr.New(apperr.ValidationFailed, "unknown operation kind").WithField("kind", "invalid")
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out, nil
}

// Totals are the figures of a set of operations. Credits sum credit and
// transfer_in, Debits sum debit and transfer_out, Net is Credits minus Debits.
type Totals struct {
	Count   int64
	Credits Sum
	Debits  Sum
	Net     Sum
}

// PeriodTotals are the totals of one period of a series.
type PeriodTotals struct {
	// PeriodStart is the local date on which the period starts.
	PeriodStart Date
	Totals
}

// ChildTotals are the totals of the subtree of one direct child, with the
// current balance of that subtree.
type ChildTotals struct {
	NodeID node.ID
	Name   string
	Type   node.Type
	// Active is the effective activity: the child and all its ancestors.
	Active  bool
	Totals  Totals
	Balance Sum
}
