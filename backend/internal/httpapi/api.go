package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/s01"
	"example.com/cabinet/backend/internal/wire"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"io"
	"mime"
	"strconv"
	"time"
	"unicode/utf8"
)

type API struct {
	svc      *s01.Service
	cfg      s01.Config
	contract *openapi3.T
}

func New(svc *s01.Service, cfg s01.Config) *echo.Echo {
	e := echo.New()
	e.IPExtractor = echo.ExtractIPDirect()
	contract, err := wire.GetSwagger()
	if err != nil {
		panic("invalid compiled API contract")
	}
	a := &API{svc: svc, cfg: cfg, contract: contract}
	e.HTTPErrorHandler = func(c *echo.Context, err error) {
		status := 503
		code := "SERVICE_UNAVAILABLE"
		var domain *s01.Error
		if errors.As(err, &domain) {
			status = domain.Status
			code = domain.Code
			if domain.RetryAfter > 0 {
				c.Response().Header().Set("Retry-After", strconv.Itoa(domain.RetryAfter))
			}
		}
		var httpError *echo.HTTPError
		if errors.As(err, &httpError) {
			status = httpError.Code
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
			if c.Request().URL.RawQuery != "" && c.Path() != "" {
				return invalid()
			}
			if c.Request().Method == "POST" && len(c.Path()) >= 4 && c.Path()[:4] == "/api" && c.Request().Header.Get("Origin") != cfg.CabinetOrigin {
				return &s01.Error{Status: 403, Code: "INVALID_CREDENTIALS"}
			}
			ctx, cancel := context.WithTimeout(c.Request().Context(), 15*time.Second)
			defer cancel()
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	})
	e.GET("/healthz", func(c *echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()
		if !svc.Health(ctx) {
			return &s01.Error{Status: 503, Code: "SERVICE_UNAVAILABLE"}
		}
		return c.JSON(200, map[string]bool{"ok": true})
	})
	e.POST("/api/v1/auth/register", a.RegisterAccount)
	e.POST("/api/v1/auth/verify-email", a.VerifyEmail)
	e.POST("/api/v1/auth/resend-verification", a.ResendVerification)
	return e
}
func invalid() error { return &s01.Error{Status: 400, Code: "INVALID_INPUT"} }
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
	out, err := a.svc.Register(c.Request().Context(), in)
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
	out, err := a.svc.VerifyEmail(c.Request().Context(), in)
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
	out, err := a.svc.ResendVerification(c.Request().Context(), in)
	if err != nil {
		return err
	}
	return c.JSON(202, out)
}
