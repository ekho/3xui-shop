package notifications

import (
	"math"
	"strings"
	"testing"
	"time"
)

// Wrong urgency, rounding, expiry0 handling or int64 overflow changes these observable selections.
func TestReminderThresholds(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		expiry int64
		want   int
	}{
		{"no expiry", 0, 0}, {"past", now.UnixMilli() - 1, 0}, {"now", now.UnixMilli(), 1},
		{"one-day-window", now.Add(47 * time.Hour).UnixMilli(), 1},
		{"two-day-boundary", now.Add(48 * time.Hour).UnixMilli(), 3},
		{"three-day-window", now.Add(95 * time.Hour).UnixMilli(), 3},
		{"four-day-boundary", now.Add(96 * time.Hour).UnixMilli(), 0},
		{"far future", math.MaxInt64, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := expiryThreshold(tc.expiry, now); got != tc.want {
				t.Fatalf("threshold=%d want %d", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name        string
		used, quota int64
		want        int
	}{
		{"unlimited", math.MaxInt64, 0, 0}, {"below80", 79, 100, 0}, {"at80", 80, 100, 80},
		{"at100", 100, 100, 100}, {"above100", 101, 100, 100},
		{"small-quota-below", 0, 1, 0}, {"small-quota-full", 1, 1, 100},
		{"ceil-below", 4, 6, 0}, {"ceil-at", 5, 6, 80},
		{"max-below", 7378697629483820645, math.MaxInt64, 0},
		{"max-at80", 7378697629483820646, math.MaxInt64, 80},
		{"max-full", math.MaxInt64, math.MaxInt64, 100},
		{"invalid-counter", -1, 100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trafficThreshold(tc.used, tc.quota); got != tc.want {
				t.Fatalf("threshold=%d want %d", got, tc.want)
			}
		})
	}
}

func TestReminderDatedMessages(t *testing.T) {
	observed := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, locale := range []string{"ru", "en"} {
		for _, r := range []reminderRow{{Kind: "expiry", ObservedAt: observed, ExpiryMS: observed.Add(time.Hour).UnixMilli()}, {Kind: "traffic", ObservedAt: observed, Threshold: 100, UsedBytes: math.MaxInt64, LimitBytes: math.MaxInt64}, {Kind: "stars_lapsed", ObservedAt: observed, PaidUntil: &observed}} {
			v := reminderMessage(r, locale)
			if !strings.Contains(v, "2026-10-08T12:00:00Z") {
				t.Fatal("message without observation")
			}
			switch r.Kind {
			case "expiry":
				if !strings.Contains(v, "2026-10-08T13:00:00Z") {
					t.Fatal("exact expiry lost")
				}
			case "traffic":
				if !strings.Contains(v, "100%") || !strings.Contains(v, "9223372036854775807") {
					t.Fatal("traffic rounded")
				}
			case "stars_lapsed":
				if !strings.Contains(v, "Stars") || r.public().Route != "cabinet" {
					t.Fatal("unsafe lapse advice")
				}
			}
		}
	}
}
