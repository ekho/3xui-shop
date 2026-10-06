package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/wire"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type API struct {
	accounts       *accounts.Service
	catalogueOwner *catalogue.Service
	subscriptions  *subscriptions.Service
	payments       *payments.Service
	supportOwner   *support.Service
	notifications  *notifications.Service
	auditReports   *auditreports.Service
	pool           *pgxpool.Pool
	cfg            app.HTTPConfig
	contract       *openapi3.T
}
type apiError struct {
	Status        int
	Code, Message string
	Details       map[string]any
	RetryAfter    int
}

func (e *apiError) Error() string { return e.Code }
func failure(status int, code string) error {
	return &apiError{Status: status, Code: code, Message: code}
}
func unavailable() error { return failure(503, "SERVICE_UNAVAILABLE") }
func newAPI(modules *app.Modules, pool *pgxpool.Pool, cfg app.HTTPConfig, contract *openapi3.T) *API {
	return &API{accounts: modules.Accounts, catalogueOwner: modules.Catalogue, subscriptions: modules.Subscriptions, payments: modules.Payments, supportOwner: modules.Support, notifications: modules.Notifications, auditReports: modules.AuditReports, pool: pool, cfg: cfg, contract: contract}
}

func New(modules *app.Modules, pool *pgxpool.Pool, cfg app.HTTPConfig) *echo.Echo {
	e := echo.New()
	e.IPExtractor = echo.ExtractIPDirect()
	if len(cfg.TrustedProxyCIDRs) > 0 {
		opts := []echo.TrustOption{echo.TrustLoopback(false), echo.TrustPrivateNet(false), echo.TrustLinkLocal(false)}
		for _, cidr := range cfg.TrustedProxyCIDRs {
			_, network, err := net.ParseCIDR(cidr)
			if err != nil {
				panic("invalid trusted proxy CIDR")
			}
			opts = append(opts, echo.TrustIPRange(network))
		}
		e.IPExtractor = echo.ExtractIPFromXFFHeader(opts...)
	}
	contract, err := wire.GetSwagger()
	if err != nil {
		panic("invalid compiled API contract")
	}
	a := newAPI(modules, pool, cfg, contract)
	e.HTTPErrorHandler = func(c *echo.Context, err error) {
		status := 503
		code := "SERVICE_UNAVAILABLE"
		var domain *apiError
		if errors.As(err, &domain) {
			status = domain.Status
			code = domain.Code
			if domain.RetryAfter > 0 {
				c.Response().Header().Set("Retry-After", strconv.Itoa(domain.RetryAfter))
			}
		}
		var httpError echo.HTTPStatusCoder
		if errors.As(err, &httpError) {
			status = httpError.StatusCode()
			code = "INVALID_INPUT"
		}
		body := map[string]any{"code": code, "message": code, "request_id": uuid.NewString()}
		if domain != nil && domain.Status == 409 && c.Path() == "/internal/v1/trial-requests/:id/decision" {
			for k, v := range domain.Details {
				if k == "current_request_status" || k == "operation_id" {
					body[k] = v
				}
			}
		}
		c.JSON(status, map[string]any{"error": body})
	}
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Response().Header().Set("Cache-Control", "no-store")
			c.Response().Header().Set("X-Content-Type-Options", "nosniff")
			c.Response().Header().Set("Referrer-Policy", "no-referrer")
			if c.Request().URL.RawQuery != "" && !(c.Request().Method == "GET" && c.Request().URL.Path == "/api/v1/operator/catalogue") {
				return invalid()
			}
			if c.Request().Method == "POST" && len(c.Path()) >= 4 && c.Path()[:4] == "/api" && c.Request().Header.Get("Origin") != cfg.CabinetOrigin {
				return &apiError{Status: 403, Code: "INVALID_CREDENTIALS"}
			}
			ctx, cancel := context.WithTimeout(c.Request().Context(), 15*time.Second)
			defer cancel()
			c.SetRequest(c.Request().WithContext(ctx))
			if strings.HasPrefix(c.Path(), "/internal/") {
				if cfg.AdapterToken == "" || subtle.ConstantTimeCompare([]byte(c.Request().Header.Get("Authorization")), []byte("Bearer "+cfg.AdapterToken)) != 1 {
					return &apiError{Status: 401, Code: "INVALID_CREDENTIALS"}
				}
				if c.Request().Header.Get("Origin") != "" {
					return &apiError{Status: 403, Code: "INVALID_CREDENTIALS"}
				}
			}
			return next(c)
		}
	})
	e.GET("/healthz", func(c *echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()
		if pool.Ping(ctx) != nil {
			return &apiError{Status: 503, Code: "SERVICE_UNAVAILABLE"}
		}
		return c.JSON(200, map[string]bool{"ok": true})
	})
	e.POST("/api/v1/auth/register", a.RegisterAccount)
	e.POST("/api/v1/auth/verify-email", a.VerifyEmail)
	e.POST("/api/v1/auth/resend-verification", a.ResendVerification)
	e.POST("/api/v1/auth/password-reset", a.RequestPasswordReset)
	e.POST("/api/v1/auth/password-reset/complete", a.CompletePasswordReset)
	e.POST("/api/v1/auth/login", a.LoginAccount)
	e.POST("/api/v1/auth/logout", a.LogoutAccount)
	e.GET("/api/v1/me", a.GetAccount)
	e.GET("/api/v1/auth/session", a.GetSessionContext)
	e.GET("/api/v1/me/security", a.GetAccountSecurity)
	e.POST("/api/v1/me/password-change", a.ChangePassword)
	e.POST("/api/v1/me/email-change", a.RequestEmailChange)
	e.POST("/api/v1/auth/email-change/confirm", a.ConfirmEmailChange)
	e.POST("/api/v1/me/email-change/cancel", a.CancelEmailChange)
	e.POST("/api/v1/me/sessions/revoke-others", a.RevokeOtherSessions)
	e.POST("/api/v1/trial-requests", a.CreateTrialRequest)
	e.GET("/api/v1/trial-requests/current", a.GetCurrentTrialRequest)
	e.POST("/internal/v1/trial-requests/:id/decision", a.DecideTrialRequest)
	e.POST("/internal/v1/trial-requests/:id/reconsider", a.ReconsiderTrialRequest)
	e.GET("/api/v1/subscription", a.GetSubscription)
	e.GET("/api/v1/subscription/key", a.GetSubscriptionKey)
	e.POST("/internal/v1/trial-operations/:id/reconcile", a.ReconcileTrialOperation)
	e.POST("/internal/v1/telegram/jobs/claim", a.ClaimTelegramJobs)
	e.POST("/internal/v1/telegram/jobs/:id/result", a.CompleteTelegramJob)
	e.GET("/api/v1/support", a.GetSupport)
	e.POST("/api/v1/support/history", a.GetSupportHistory)
	e.POST("/api/v1/support/messages", a.CreateSupportMessage)
	e.POST("/api/v1/support/read", a.AcknowledgeSupport)
	e.POST("/api/v1/support/state", a.SetSupportState)
	e.GET("/api/v1/support/messages/:id/attachment", a.GetSupportAttachment)
	e.GET("/api/v1/operator/clients/:id/support", a.GetOperatorSupport)
	e.POST("/api/v1/operator/clients/:id/support/history", a.GetOperatorSupportHistory)
	e.POST("/api/v1/operator/clients/:id/support/messages", a.CreateOperatorSupportMessage)
	e.POST("/api/v1/operator/clients/:id/support/read", a.AcknowledgeOperatorSupport)
	e.POST("/api/v1/operator/clients/:id/support/state", a.SetOperatorSupportState)
	e.POST("/api/v1/operator/clients/:id/support/ban", a.SetOperatorSupportBan)
	e.GET("/api/v1/operator/session", a.GetOperatorSession)
	e.GET("/api/v1/catalogue", a.GetCatalogue)
	e.GET("/api/v1/payment-methods", a.GetPaymentMethods)
	e.POST("/api/v1/orders", a.CreatePurchaseOrder)
	e.GET("/api/v1/orders/current", a.GetCurrentPurchaseOrder)
	e.GET("/api/v1/orders/:id", a.GetPurchaseOrder)
	e.POST("/api/v1/orders/:id/cancel", a.CancelPurchaseOrder)
	e.GET("/api/v1/operator/clients/:id/orders/current", a.GetOperatorPurchaseOrder)
	e.POST("/api/v1/operator/clients/:id/orders/:order_id/reconcile", a.ReconcilePurchaseOrder)
	e.POST("/webhooks/yoomoney", a.ReceiveYooMoney)
	e.GET("/api/v1/operator/catalogue", a.GetOperatorCatalogue)
	e.POST("/api/v1/operator/catalogue/plans", a.CreateCataloguePlan)
	e.POST("/api/v1/operator/catalogue/plans/:id/revision", a.ReviseCataloguePlan)
	e.POST("/api/v1/operator/catalogue/plans/:id/archive", a.ArchiveCataloguePlan)
	e.POST("/api/v1/operator/clients/search", a.SearchOperatorClients)
	e.GET("/api/v1/operator/clients/:id", a.GetOperatorClient)
	e.POST("/api/v1/operator/clients/:id/history", a.GetOperatorClientHistory)
	e.GET("/api/v1/operator/clients/:id/key", a.GetOperatorClientKey)
	e.POST("/api/v1/operator/clients/:id/restriction", a.SetOperatorRestriction)
	e.POST("/api/v1/operator/clients/:id/access-operations", a.CreateAccessOperation)
	e.GET("/api/v1/operator/clients/:id/access-operations/:operation_id", a.GetAccessOperation)
	e.POST("/api/v1/operator/clients/:id/access-operations/:operation_id/reconcile", a.ReconcileAccessOperation)
	e.POST("/api/v1/operator/trial-requests/:id/decision", a.DecideOperatorTrial)
	e.POST("/api/v1/operator/trial-requests/:id/reconsider", a.ReconsiderOperatorTrial)
	e.POST("/api/v1/operator/trial-operations/:id/reconcile", a.ReconcileOperatorTrial)
	e.POST("/api/v1/operator/clients/trial", a.CreateOperatorTelegramTrial)
	return e
}
func invalid() error { return &apiError{Status: 400, Code: "INVALID_INPUT"} }
func decode[T any](a *API, c *echo.Context, schema string) (T, error) {
	var out T
	media, _, err := mime.ParseMediaType(c.Request().Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return out, invalid()
	}
	data, err := io.ReadAll(io.LimitReader(c.Request().Body, 16385))
	if err != nil || len(data) > 16384 || !utf8.Valid(data) {
		return out, invalid()
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err = d.Decode(&value); err != nil {
		return out, invalid()
	}
	if d.Decode(new(any)) != io.EOF {
		return out, invalid()
	}
	if err = a.contract.Components.Schemas[schema].Value.VisitJSON(value); err != nil {
		return out, invalid()
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return out, invalid()
	}
	return out, nil
}
func (a *API) RegisterAccount(c *echo.Context) error {
	in, err := decode[wire.RegisterInput](a, c, "RegisterInput")
	if err != nil {
		return err
	}
	out, err := a.register(c.Request().Context(), in)
	if err != nil {
		return err
	}
	return c.JSON(202, out)
}
func (a *API) VerifyEmail(c *echo.Context) error {
	in, err := decode[wire.VerifyInput](a, c, "VerifyInput")
	if err != nil {
		return err
	}
	out, err := a.verifyEmail(c.Request().Context(), in)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) ResendVerification(c *echo.Context) error {
	in, err := decode[wire.ResendInput](a, c, "ResendInput")
	if err != nil {
		return err
	}
	out, err := a.resendVerification(c.Request().Context(), in)
	if err != nil {
		return err
	}
	return c.JSON(202, out)
}

func (a *API) LoginAccount(c *echo.Context) error {
	in, err := decode[wire.LoginInput](a, c, "LoginInput")
	if err != nil {
		return err
	}
	out, raw, err := a.login(c.Request().Context(), in, c.RealIP())
	if err != nil {
		return err
	}
	c.SetCookie(&http.Cookie{Name: "__Host-session", Value: raw, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/", MaxAge: 30 * 24 * 3600})
	return c.JSON(200, out)
}
func (a *API) auth(c *echo.Context, write bool) (wire.AccountResult, error) {
	cookie, err := c.Cookie("__Host-session")
	if err != nil {
		return wire.AccountResult{}, &apiError{Status: 401, Code: "INVALID_CREDENTIALS"}
	}
	out, err := a.authenticate(c.Request().Context(), cookie.Value)
	if err != nil {
		return out, err
	}
	if write && subtle.ConstantTimeCompare([]byte(c.Request().Header.Get("X-CSRF-Token")), []byte(out.CsrfToken)) != 1 {
		return out, &apiError{Status: 403, Code: "INVALID_CREDENTIALS"}
	}
	return out, nil
}
func (a *API) GetAccount(c *echo.Context) error {
	out, err := a.auth(c, false)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) LogoutAccount(c *echo.Context) error {
	if err := requireEmptyBody(c); err != nil {
		return err
	}
	cookie, err := c.Cookie("__Host-session")
	raw := ""
	if err == nil {
		raw = cookie.Value
	}
	if raw != "" {
		var out wire.SessionContext
		out, err = a.getSessionContext(c.Request().Context(), raw)
		if err == nil && subtle.ConstantTimeCompare([]byte(c.Request().Header.Get("X-CSRF-Token")), []byte(out.CsrfToken)) != 1 {
			return &apiError{Status: 403, Code: "INVALID_CREDENTIALS"}
		}
		var domain *apiError
		if err != nil && (!errors.As(err, &domain) || domain.Status != 401) {
			return err
		}
		if err == nil {
			if err = a.logout(c.Request().Context(), raw); err != nil {
				return err
			}
		}
	}
	c.SetCookie(&http.Cookie{Name: "__Host-session", Value: "", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/", MaxAge: -1})
	return c.NoContent(204)
}

func idempotencyKey(c *echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Request().Header.Get("Idempotency-Key"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, invalid()
	}
	return id, nil
}
func resourceID(c *echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, invalid()
	}
	return id, nil
}
func (a *API) CreateTrialRequest(c *echo.Context) error {
	account, err := a.auth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.TrialRequestInput](a, c, "TrialRequestInput")
	if err != nil {
		return err
	}
	out, created, err := a.createTrialRequest(c.Request().Context(), account.Account.AccountId, key, in)
	if err != nil {
		return err
	}
	status := 200
	if created {
		status = 201
	}
	return c.JSON(status, out)
}
func (a *API) GetCurrentTrialRequest(c *echo.Context) error {
	account, err := a.auth(c, false)
	if err != nil {
		return err
	}
	out, err := a.currentTrialRequest(c.Request().Context(), account.Account.AccountId)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) DecideTrialRequest(c *echo.Context) error {
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.DecisionInput](a, c, "DecisionInput")
	if err != nil {
		return err
	}
	out, err := a.decideTrialRequest(c.Request().Context(), id, in)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) ReconsiderTrialRequest(c *echo.Context) error {
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.ReconsiderInput](a, c, "ReconsiderInput")
	if err != nil {
		return err
	}
	out, err := a.reconsiderTrialRequest(c.Request().Context(), id, key, in)
	if err != nil {
		return err
	}
	return c.JSON(201, out)
}

func (a *API) GetSubscription(c *echo.Context) error {
	account, e := a.auth(c, false)
	if e != nil {
		return e
	}
	out, e := a.subscription(c.Request().Context(), account.Account.AccountId)
	if e != nil {
		return e
	}
	return c.JSON(200, out)
}
func (a *API) GetSubscriptionKey(c *echo.Context) error {
	account, e := a.auth(c, false)
	if e != nil {
		return e
	}
	out, e := a.subscriptionKey(c.Request().Context(), account.Account.AccountId)
	if e != nil {
		return e
	}
	return c.JSON(200, out)
}
func (a *API) ReconcileTrialOperation(c *echo.Context) error {
	id, e := resourceID(c)
	if e != nil {
		return e
	}
	key, e := idempotencyKey(c)
	if e != nil {
		return e
	}
	in, e := decode[wire.ReconcileInput](a, c, "ReconcileInput")
	if e != nil {
		return e
	}
	out, e := a.reconcileTrialOperation(c.Request().Context(), id, key, in)
	if e != nil {
		return e
	}
	return c.JSON(202, out)
}

func (a *API) ClaimTelegramJobs(c *echo.Context) error {
	in, e := decode[wire.ClaimInput](a, c, "ClaimInput")
	if e != nil {
		return e
	}
	out, e := a.claimTelegramJobs(c.Request().Context(), in)
	if e != nil {
		return e
	}
	return c.JSON(200, out)
}
func (a *API) CompleteTelegramJob(c *echo.Context) error {
	id, e := resourceID(c)
	if e != nil {
		return e
	}
	in, e := decode[wire.TelegramResultInput](a, c, "TelegramResultInput")
	if e != nil {
		return e
	}
	if e = a.completeTelegramJob(c.Request().Context(), id, in); e != nil {
		return e
	}
	return c.JSON(200, wire.CompleteResult{})
}

func requireEmptyBody(c *echo.Context) error {
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, 1))
	if err != nil || len(body) != 0 {
		return invalid()
	}
	return nil
}
