package payments

import (
	"context"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"math/big"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type StatisticsMoney = auditreports.StatisticsMoney
type StatisticsRefund = auditreports.StatisticsRefund
type LegacyStatisticsMoney = auditreports.LegacyStatisticsMoney
type LegacyPaymentStatistics = auditreports.LegacyPaymentStatistics
type Statistics = auditreports.PaymentStatistics

// A later fulfillment review/refund does not erase the original money proof.
// Extra/review receipts lacking this exact funding association are excluded.
const fundedStatistics = ` FROM purchase_orders p JOIN purchase_receipts r
 ON r.order_id=p.id AND r.operation_id=p.funding_operation_id
 WHERE p.account_id=ANY($1::uuid[]) AND p.payment_status='paid'`

// StatisticsTx uses only payment-owned facts in the caller's read snapshot.
func (s *Service) StatisticsTx(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (Statistics, error) {
	out := Statistics{Money: []StatisticsMoney{}, Refunds: []StatisticsRefund{}, Legacy: LegacyPaymentStatistics{Money: []LegacyStatisticsMoney{}}}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(n),0)::bigint,count(*),count(*) FILTER(WHERE n>1) FROM (SELECT count(*) n`+fundedStatistics+` GROUP BY p.account_id) x`, ids).Scan(&out.PaidOrders, &out.PaidUsers, &out.RepeatUsers); err != nil {
		return out, unavailable()
	}
	rows, err := tx.Query(ctx, `SELECT CASE WHEN r.currency='643' THEN 'RUB' ELSE r.currency END currency,
 sum(r.gross_minor::numeric)::text,COALESCE(sum(r.net_minor::numeric),0)::text,count(*) FILTER(WHERE r.net_minor IS NULL)`+fundedStatistics+` GROUP BY 1 ORDER BY 1`, ids)
	if err != nil {
		return out, unavailable()
	}
	for rows.Next() {
		var m StatisticsMoney
		if rows.Scan(&m.Currency, &m.GrossMinor, &m.KnownNetMinor, &m.UnknownNetReceipts) != nil {
			rows.Close()
			return out, unavailable()
		}
		if m.Currency != "RUB" && m.Currency != "USD" && m.Currency != "XTR" {
			rows.Close()
			return out, unavailable()
		}
		out.Money = append(out.Money, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, unavailable()
	}
	rows, err = tx.Query(ctx, `SELECT f.returned_currency,sum(f.returned_amount::numeric)::text FROM purchase_refunds f JOIN purchase_orders p ON p.id=f.order_id
 WHERE p.account_id=ANY($1::uuid[]) AND p.payment_status='paid' AND f.receipt_operation_id=p.funding_operation_id GROUP BY 1 ORDER BY 1`, ids)
	if err != nil {
		return out, unavailable()
	}
	for rows.Next() {
		var r StatisticsRefund
		if rows.Scan(&r.Currency, &r.ReturnedAmount) != nil {
			rows.Close()
			return out, unavailable()
		}
		out.Refunds = append(out.Refunds, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, unavailable()
	}
	rows, err = tx.Query(ctx, `SELECT account_id,source_tg_id,subscription FROM legacy_payment_transactions WHERE account_id=ANY($1::uuid[]) AND status='completed'`, ids)
	if err != nil {
		return out, unavailable()
	}
	defer rows.Close()
	users := map[uuid.UUID]int{}
	money := map[string]*big.Int{}
	for rows.Next() {
		var id uuid.UUID
		var tg int64
		var packed string
		if rows.Scan(&id, &tg, &packed) != nil {
			return out, unavailable()
		}
		out.Legacy.CompletedTransactions++
		users[id]++
		decoded, e := decodeLegacySubscription(packed, tg)
		if e != nil || decoded.method == nil {
			out.Legacy.UnknownQuoteCount++
			continue
		}
		amount, ok := new(big.Int).SetString(decoded.quote.AmountMinor, 10)
		if !ok {
			return out, unavailable()
		}
		sum := money[decoded.quote.Currency]
		if sum == nil {
			sum = new(big.Int)
			money[decoded.quote.Currency] = sum
		}
		sum.Add(sum, amount)
	}
	if rows.Err() != nil {
		return out, unavailable()
	}
	out.Legacy.PaidUsers = int64(len(users))
	for _, n := range users {
		if n > 1 {
			out.Legacy.RepeatUsers++
		}
	}
	currencies := make([]string, 0, len(money))
	for c := range money {
		currencies = append(currencies, c)
	}
	sort.Strings(currencies)
	for _, c := range currencies {
		out.Legacy.Money = append(out.Legacy.Money, LegacyStatisticsMoney{Currency: c, QuotedMinor: money[c].String()})
	}
	return out, nil
}
