package payments

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

// Removing canonical/current identity or the 24-hour grace changes the client advice.
func TestStarsReminderPolicy(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	until := now.Add(24 * time.Hour)
	current := uuid.New()
	row := starsSubscriptionRow{root: uuid.New(), canonical: true, bot: 123, payer: 701, current: &current, paidUntil: &until, provider: "active", desired: "keep", control: "none"}
	for _, tc := range []struct {
		name                    string
		at                      time.Time
		known, suppress, lapsed bool
	}{
		{"current", now, true, true, false}, {"grace", until.Add(time.Hour), true, true, false},
		{"exact-grace-end", until.Add(24 * time.Hour), true, true, false},
		{"lapse", until.Add(24*time.Hour + time.Nanosecond), true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := starsReminderPeriod(row, tc.at)
			if p.Known != tc.known || p.SuppressExpiry != tc.suppress || p.Lapsed != tc.lapsed || p.PaidUntil == nil || p.Period != current.String()+":1791547200" {
				t.Fatal("canonical paid period/grace", p)
			}
		})
	}
	for _, name := range []string{"noncanonical", "no-current", "no-paid-until", "unknown", "uncertain", "rejected"} {
		t.Run(name, func(t *testing.T) {
			r := row
			switch name {
			case "noncanonical":
				r.canonical = false
			case "no-current":
				r.current = nil
			case "no-paid-until":
				r.paidUntil = nil
			case "unknown":
				r.provider = "unknown"
			case "uncertain", "rejected":
				r.control = name
			}
			if starsReminderPeriod(r, now).Known {
				t.Fatal("unknown billing became known")
			}
		})
	}
	r := row
	r.provider = "canceled"
	if p := starsReminderPeriod(r, now); !p.Known || p.SuppressExpiry || p.Lapsed {
		t.Fatal("native user cancellation mistaken for active renewal")
	}
	r = row
	r.desired = "cancel"
	r.control = "pending"
	if p := starsReminderPeriod(r, now); !p.Known || !p.SuppressExpiry {
		t.Fatal("unconfirmed cancel promised external renewal")
	}
}
