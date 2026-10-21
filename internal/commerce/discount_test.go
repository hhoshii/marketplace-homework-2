package commerce

import (
	"github.com/shopspring/decimal"
	"testing"
)

func TestDiscount(t *testing.T) {
	for _, c := range []struct{ total, value, kind, want string }{
		{"100", "70", "PERCENTAGE", "70"}, {"100", "70.01", "PERCENTAGE", "0"},
		{"10", "100", "FIXED_AMOUNT", "10"}, {"10.05", "10", "PERCENTAGE", "1.01"},
		{"0.01", "50", "PERCENTAGE", "0.01"},
	} {
		got := discount(decimal.RequireFromString(c.total), decimal.RequireFromString(c.value), c.kind)
		if !got.Equal(decimal.RequireFromString(c.want)) {
			t.Errorf("%+v: %s", c, got)
		}
	}
}
