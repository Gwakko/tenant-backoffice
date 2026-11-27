package money_test

import (
	"math"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
)

func requireCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	got, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, code, got)
}

// Test-ID: T-U-23
func TestPositive(t *testing.T) {
	// EC-75: zero and negatives are not operation amounts.
	for _, a := range []money.Amount{0, -1, -5, money.Min} {
		requireCode(t, a.Positive(), apperr.AmountMustBePositive)
	}
	for _, a := range []money.Amount{1, 1050, money.Max} {
		require.NoError(t, a.Positive())
	}
	// "-5" never parses into an amount at all.
	for _, s := range []string{"-5", "+5", "-0"} {
		_, err := money.ParseAmount(s)
		requireCode(t, err, apperr.InvalidAmount)
	}
	a, err := money.ParseAmount("0")
	require.NoError(t, err)
	requireCode(t, a.Positive(), apperr.AmountMustBePositive)
}

// Test-ID: T-U-24
func TestOverflow(t *testing.T) {
	// EC-77: balance MaxInt64-10, credit 11.
	_, err := (money.Max - 10).Add(11)
	requireCode(t, err, apperr.BalanceOverflow)
	got, err := (money.Max - 10).Add(10)
	require.NoError(t, err)
	require.Equal(t, money.Max, got)

	_, err = money.Max.Add(1)
	requireCode(t, err, apperr.BalanceOverflow)
	_, err = money.Max.Add(money.Max)
	requireCode(t, err, apperr.BalanceOverflow)
	_, err = money.Min.Add(-1)
	requireCode(t, err, apperr.BalanceOverflow)
	_, err = money.Max.Sub(-1)
	requireCode(t, err, apperr.BalanceOverflow)
	_, err = money.Min.Sub(1)
	requireCode(t, err, apperr.InsufficientFunds)

	// Property: Add succeeds exactly when the exact sum fits into int64, and a
	// successful sum is exact; it never wraps.
	boundary := []int64{0, 1, 2, 10, 11, math.MaxInt64, math.MaxInt64 - 1, math.MaxInt64 / 2, math.MaxInt64/2 + 1, math.MinInt64, math.MinInt64 + 1, -1}
	rng := rand.New(rand.NewPCG(1, 2))
	vals := append([]int64{}, boundary...)
	for i := 0; i < 400; i++ {
		vals = append(vals, int64(rng.Uint64()), int64(rng.Uint64()>>uint(rng.IntN(64))))
	}
	for _, x := range vals {
		for _, y := range boundary {
			checkAdd(t, x, y)
			checkAdd(t, y, x)
		}
	}
	for i := 0; i+1 < len(vals); i++ {
		checkAdd(t, vals[i], vals[i+1])
	}
}

func checkAdd(t *testing.T, x, y int64) {
	t.Helper()
	exact := new(big.Int).Add(big.NewInt(x), big.NewInt(y))
	fits := exact.IsInt64()
	got, err := money.Amount(x).Add(money.Amount(y))
	if !fits {
		requireCode(t, err, apperr.BalanceOverflow)
		return
	}
	require.NoError(t, err, "%d + %d", x, y)
	require.Equal(t, exact.Int64(), got.Minor(), "%d + %d", x, y)
}

func TestSub(t *testing.T) {
	got, err := money.Amount(51).Sub(1)
	require.NoError(t, err)
	require.Equal(t, money.Amount(50), got)
	got, err = money.Amount(50).Sub(50)
	require.NoError(t, err)
	require.Equal(t, money.Amount(0), got)
	_, err = money.Amount(50).Sub(51)
	requireCode(t, err, apperr.InsufficientFunds)

	rng := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 2000; i++ {
		a := int64(rng.Uint64() >> 1)
		b := int64(rng.Uint64() >> uint(1+rng.IntN(63)))
		got, err := money.Amount(a).Sub(money.Amount(b))
		if a < b {
			requireCode(t, err, apperr.InsufficientFunds)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, a-b, got.Minor())
	}
}

func TestCompare(t *testing.T) {
	require.Equal(t, -1, money.Amount(1).Cmp(2))
	require.Equal(t, 0, money.Amount(2).Cmp(2))
	require.Equal(t, 1, money.Max.Cmp(money.Min))
	require.True(t, money.Amount(1).Less(2))
	require.False(t, money.Amount(2).Less(2))
	require.Equal(t, money.Amount(1), money.MinOf(1, 2))
	require.Equal(t, money.Amount(1), money.MinOf(2, 1))
}

func TestParseAmount(t *testing.T) {
	ok := map[string]money.Amount{
		"0": 0, "1": 1, "1050": 1050, "9223372036854775807": money.Max,
	}
	for s, want := range ok {
		got, err := money.ParseAmount(s)
		require.NoError(t, err, s)
		require.Equal(t, want, got, s)
	}
	// EC-76.
	for _, s := range []string{
		"", " ", "10.5", "1e3", "007", "00", "-1", "+1", "1 ", " 1", "0x10", "1_000", "١",
		"9223372036854775808", "99999999999999999999", "10000000000000000000",
	} {
		_, err := money.ParseAmount(s)
		requireCode(t, err, apperr.InvalidAmount)
	}
}

func TestParseDecimal(t *testing.T) {
	ok := []struct {
		in   string
		dec  int
		want money.Amount
	}{
		{"10.50", 2, 1050},
		{"10.5", 2, 1050},
		{"10", 2, 1000},
		{"0.1", 2, 10},
		{"0.01", 2, 1},
		{"0", 2, 0},
		{"0.00", 2, 0},
		{"1.234", 3, 1234},
		{"7", 0, 7},
		{"92233720368547758.07", 2, money.Max},
		{"9223372036854775807", 0, money.Max},
		{"9.223372036854775807", 18, money.Max},
	}
	for _, c := range ok {
		got, err := money.ParseDecimal(c.in, c.dec)
		require.NoError(t, err, c.in)
		require.Equal(t, c.want, got, c.in)
	}
	bad := []struct {
		in  string
		dec int
	}{
		{"10.505", 2},
		{"10.500", 2},
		{"1.5", 0},
		{"1e3", 2},
		{"1E3", 2},
		{"", 2},
		{".5", 2},
		{"10.", 2},
		{"-1", 2},
		{"+1", 2},
		{"007", 2},
		{"01.5", 2},
		{"1,5", 2},
		{"1.5.2", 2},
		{" 1", 2},
		{"1.-5", 2},
		{"92233720368547758.08", 2},
		{"92233720368547759", 2},
		{"9223372036854775808", 0},
		{"10", -1},
		{"10", 19},
	}
	for _, c := range bad {
		_, err := money.ParseDecimal(c.in, c.dec)
		requireCode(t, err, apperr.InvalidAmount)
	}
}

func TestFormatDecimal(t *testing.T) {
	cases := []struct {
		a    money.Amount
		dec  int
		want string
	}{
		{1050, 2, "10.50"},
		{10, 2, "0.10"},
		{1, 2, "0.01"},
		{0, 2, "0.00"},
		{7, 0, "7"},
		{money.Max, 2, "92233720368547758.07"},
		{money.Max, 0, "9223372036854775807"},
		{-1050, 2, "-10.50"},
		{money.Min, 2, "-92233720368547758.08"},
		{money.Min, 0, "-9223372036854775808"},
		{1234, 3, "1.234"},
		{5, 3, "0.005"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, c.a.FormatDecimal(c.dec))
	}
	require.Equal(t, "1050", money.Amount(1050).String())
}

// Property: parse(format(x)) == x for every non-negative amount and every
// supported number of decimals, across the whole int64 range.
func TestFormatParseRoundTrip(t *testing.T) {
	vals := []int64{0, 1, 9, 10, 99, 100, 101, 999, 1000, 1050, math.MaxInt64, math.MaxInt64 - 1, math.MaxInt64/10 + 1}
	for p := int64(10); p > 0 && p <= math.MaxInt64/10; p *= 10 {
		vals = append(vals, p-1, p, p+1)
	}
	rng := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < 500; i++ {
		vals = append(vals, int64(rng.Uint64()>>1), int64(rng.Uint64()>>uint(1+rng.IntN(63))))
	}
	for _, v := range vals {
		for dec := 0; dec <= money.MaxDecimals; dec++ {
			s := money.Amount(v).FormatDecimal(dec)
			got, err := money.ParseDecimal(s, dec)
			require.NoError(t, err, "%d/%d -> %q", v, dec, s)
			require.Equal(t, v, got.Minor(), "%q", s)
		}
		got, err := money.ParseAmount(money.Amount(v).String())
		require.NoError(t, err)
		require.Equal(t, v, got.Minor())
	}
}
