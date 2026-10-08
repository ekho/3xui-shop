package payments

import (
	"time"

	"github.com/google/uuid"
)

// Field order and tags preserve persisted body hashes and replay results.
type PurchaseOrderInput struct {
	Action                  string     `json:"action"`
	PaymentMethod           string     `json:"payment_method"`
	PaymentType             string     `json:"payment_type"`
	PeriodDays              int64      `json:"period_days"`
	PlanId                  uuid.UUID  `json:"plan_id"`
	Revision                int64      `json:"revision"`
	SourceAccessOperationId *uuid.UUID `json:"source_access_operation_id,omitempty"`
	StarsRecurring          bool       `json:"stars_recurring,omitempty"`
}
type PurchaseQuote struct {
	AmountMinor             string     `json:"amount_minor"`
	Currency                string     `json:"currency"`
	Devices                 int64      `json:"devices"`
	PeriodDays              int64      `json:"period_days"`
	PlanId                  uuid.UUID  `json:"plan_id"`
	Profile                 string     `json:"profile"`
	Revision                int64      `json:"revision"`
	SourceAccessOperationId *uuid.UUID `json:"source_access_operation_id,omitempty"`
	TrafficGb               int64      `json:"traffic_gb"`
	StarsRecurring          bool       `json:"stars_recurring,omitempty"`
}
type PlanChangeContext struct {
	CurrentPlanId           uuid.UUID `json:"current_plan_id"`
	SourceAccessOperationId uuid.UUID `json:"source_access_operation_id"`
}
type PurchaseOrder struct {
	AccessOperationId *uuid.UUID         `json:"access_operation_id"`
	Action            string             `json:"action"`
	CanCancel         bool               `json:"can_cancel"`
	CanPay            bool               `json:"can_pay"`
	Checkout          *YooMoneyCheckout  `json:"checkout"`
	CreatedAt         time.Time          `json:"created_at"`
	Expired           bool               `json:"expired"`
	ExpiresAt         time.Time          `json:"expires_at"`
	FulfillmentStatus string             `json:"fulfillment_status"`
	OrderId           uuid.UUID          `json:"order_id"`
	PaymentMethod     string             `json:"payment_method"`
	PaymentStatus     string             `json:"payment_status"`
	PaymentType       string             `json:"payment_type"`
	Quote             PurchaseQuote      `json:"quote"`
	ReviewRequired    bool               `json:"review_required"`
	FullyRefunded     bool               `json:"fully_refunded,omitempty"`
	ManualPayment     *ManualPayment     `json:"manual_payment,omitempty"`
	YooKassaCheckout  *YooKassaCheckout  `json:"yookassa_checkout,omitempty"`
	CryptomusCheckout *CryptomusCheckout `json:"cryptomus_checkout,omitempty"`
	HeleketCheckout   *HeleketCheckout   `json:"heleket_checkout,omitempty"`
	StarsCheckout     *StarsCheckout     `json:"stars_checkout,omitempty"`
}
type CryptomusCheckout struct {
	State string  `json:"state"`
	URL   *string `json:"url"`
}
type HeleketCheckout struct {
	State string  `json:"state"`
	URL   *string `json:"url"`
}
type YooKassaCheckout struct {
	State string  `json:"state"`
	URL   *string `json:"url"`
}
type ManualPayment struct {
	CanReport    bool       `json:"can_report"`
	DecidedAt    *time.Time `json:"decided_at"`
	Instructions string     `json:"instructions"`
	Reason       *string    `json:"reason"`
	ReportedAt   *time.Time `json:"reported_at"`
	State        string     `json:"state"`
}
type ManualPaymentDecisionInput struct {
	ConfirmedAmountMinor *string `json:"confirmed_amount_minor,omitempty"`
	Decision             string  `json:"decision"`
	Reason               string  `json:"reason"`
}
type ManualPaymentItem struct {
	AccountId uuid.UUID     `json:"account_id"`
	Order     PurchaseOrder `json:"order"`
}
type ManualPaymentPage struct {
	HasMore    bool                `json:"has_more"`
	Items      []ManualPaymentItem `json:"items"`
	NextCursor *uuid.UUID          `json:"next_cursor"`
}
type CurrentPurchaseOrder struct {
	Order       *PurchaseOrder `json:"order"`
	CanPurchase *bool          `json:"can_purchase,omitempty"`
}
type PaymentMethod struct {
	Currency string `json:"currency"`
	Id       string `json:"id"`
}
type PaymentMethods struct {
	Methods []PaymentMethod `json:"methods"`
}
type PurchaseReconcileInput struct {
	Reason string `json:"reason"`
}
type YooMoneyCheckout struct {
	Action string                 `json:"action"`
	Fields YooMoneyCheckoutFields `json:"fields"`
	Method string                 `json:"method"`
}
type YooMoneyCheckoutFields struct {
	Label        uuid.UUID `json:"label"`
	PaymentType  string    `json:"paymentType"`
	QuickpayForm string    `json:"quickpay-form"`
	Receiver     string    `json:"receiver"`
	SuccessURL   string    `json:"successURL"`
	Sum          string    `json:"sum"`
}
