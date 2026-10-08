package auditreports

import (
	"math"
	"testing"
)

func TestStatisticsPercent(t *testing.T) {
	for _, tc := range []struct {
		n, d int64
		want string
	}{{1, 3, "33.33"}, {2, 3, "66.67"}, {1, 32, "3.13"}, {0, 7, "0.00"}, {math.MaxInt64, math.MaxInt64, "100.00"}} {
		if got := percent(tc.n, tc.d); got == nil || *got != tc.want {
			t.Fatal("exact percent", tc.want)
		}
	}
	if percent(0, 0) != nil {
		t.Fatal("empty denominator")
	}
}
