package payments

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"strconv"
	"strings"
)

type legacySubscription struct {
	state  string
	method *string
	quote  LegacyQuote
}

func decodeLegacySubscription(packed string, tg int64) (legacySubscription, error) {
	var out legacySubscription
	p := strings.Split(packed, ":")
	if tg <= 0 || len(p) != 9 || p[0] != "subscription" || (p[2] != "0" && p[2] != "1") || (p[3] != "0" && p[3] != "1") {
		return out, failure(400, "INVALID_INPUT")
	}
	id, err := strconv.ParseInt(p[4], 10, 64)
	if err != nil || (id != 0 && id != tg) {
		return out, failure(400, "INVALID_INPUT")
	}
	values := []*int64{&out.quote.Devices, &out.quote.PeriodDays, &out.quote.TrafficGb}
	for i, v := range values {
		n, err := strconv.ParseInt(p[i+5], 10, 64)
		if err != nil || n < 0 {
			return out, failure(400, "INVALID_INPUT")
		}
		*v = n
	}
	amount, err := minorUnits(p[8])
	if err != nil {
		return out, failure(400, "INVALID_INPUT")
	}
	out.state = p[1]
	method, currency := "", ""
	switch p[1] {
	case "subscription", "change", "extend", "process", "devices", "duration", "promocode", "get_trial", "back_to_duration", "back_to_payment":
	case "pay_yoomoney":
		method, currency = "yoomoney", "RUB"
	case "pay_yookassa":
		method, currency = "yookassa", "RUB"
	case "pay_manual":
		method, currency = "manual", "RUB"
	case "pay_cryptomus":
		method, currency = "cryptomus", "USD"
	case "pay_heleket":
		method, currency = "heleket", "USD"
	case "pay_telegram_stars":
		if amount%100 != 0 {
			return out, failure(400, "INVALID_INPUT")
		}
		method, currency = "telegram_stars", "XTR"
		amount /= 100
	default:
		return out, failure(400, "INVALID_INPUT")
	}
	out.quote.Action = "purchase"
	if p[2] == "1" || p[1] == "extend" {
		out.quote.Action = "renew"
	} else if p[3] == "1" || p[1] == "change" {
		out.quote.Action = "change_plan"
	}
	out.quote.AmountMinor, out.quote.Currency = strconv.FormatInt(amount, 10), currency
	if method != "" {
		out.method = &method
	}
	return out, nil
}

func LegacyTelegramButton(packed string, tg int64) (string, error) {
	if tg > 1<<52-1 || len(packed) > 64 {
		return "", failure(400, "INVALID_INPUT")
	}
	b, err := decodeLegacySubscription(packed, tg)
	if err == nil && b.method == nil && b.state != "get_trial" {
		if b.quote.Action == "renew" {
			return "extend", nil
		}
		if b.quote.Action == "change_plan" {
			return "change", nil
		}
	}
	return b.state, err
}

// An old button has no order ID; only a single exact immutable invoice is addressable.
func (s *Service) LegacyTelegramReference(ctx context.Context, tg int64, packed string) (*string, error) {
	if tg > 1<<52-1 || len(packed) > 64 {
		return nil, failure(400, "INVALID_INPUT")
	}
	b, err := decodeLegacySubscription(packed, tg)
	if err != nil {
		return nil, err
	}
	if b.method == nil || b.quote.AmountMinor == "0" || b.quote.Devices == 0 || b.quote.PeriodDays == 0 {
		return nil, nil
	}
	a, err := s.authority.LookupTelegram(ctx, tg)
	if errors.Is(err, accounts.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !accounts.SourceEligible(a) || a.TelegramLoginDisabled {
		return nil, failure(401, "INVALID_CREDENTIALS")
	}
	if a.Restricted {
		return nil, failure(403, "ACCOUNT_RESTRICTED")
	}
	rows, err := s.pool.Query(ctx, `SELECT source_id FROM legacy_payment_transactions WHERE account_id=$1 AND subscription=$2 LIMIT 2`, a.ID, packed)
	if err != nil {
		return nil, unavailable()
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			return nil, unavailable()
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		return nil, unavailable()
	}
	if len(ids) != 1 {
		return nil, nil
	}
	id := strconv.FormatInt(ids[0], 10)
	return &id, nil
}
