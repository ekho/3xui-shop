package httpapi

import (
	"bytes"
	"errors"
	"example.com/cabinet/backend/internal/platform"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"unicode/utf8"
)

const supportRequestMax = 10*1024*1024 + 16*1024

func (a *API) supportActor(c *echo.Context, write, operator bool) (uuid.UUID, uuid.UUID, error) {
	account, err := a.auth(c, write)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	actor := account.Account.AccountId
	if !operator {
		return actor, actor, nil
	}
	// Entitlement is checked before reading a request body, then again under
	// account/role locks in every write transaction.
	if err = a.svc.RequireSupportOperator(c.Request().Context(), actor); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	target, err := resourceID(c)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return actor, target, nil
}

func readSupportMessage(a *API, c *echo.Context) (string, string, []byte, error) {
	media, params, err := mime.ParseMediaType(c.Request().Header.Get("Content-Type"))
	if err != nil {
		return "", "", nil, invalid()
	}
	if media == "application/json" {
		in, err := decode[wire.SupportMessageInput](a, c, "SupportMessageInput")
		if err != nil {
			return "", "", nil, err
		}
		return in.Text, "", nil, nil
	}
	if media != "multipart/form-data" || params["boundary"] == "" {
		return "", "", nil, invalid()
	}
	if c.Request().ContentLength > supportRequestMax {
		return "", "", nil, &platform.Error{Status: 413, Code: "INVALID_INPUT"}
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request().Body, supportRequestMax+1))
	if err != nil {
		return "", "", nil, invalid()
	}
	if len(raw) > supportRequestMax {
		return "", "", nil, &platform.Error{Status: 413, Code: "INVALID_INPUT"}
	}
	reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	var text, name string
	var file []byte
	seenText, seenFile := false, false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", "", nil, invalid()
		}
		disposition, values, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil || disposition != "form-data" {
			return "", "", nil, invalid()
		}
		switch values["name"] {
		case "text":
			if seenText || values["filename"] != "" {
				return "", "", nil, invalid()
			}
			seenText = true
			data, err := io.ReadAll(io.LimitReader(part, 16*1024+1))
			if err != nil || len(data) > 16*1024 || !utf8.Valid(data) {
				return "", "", nil, invalid()
			}
			text = string(data)
		case "file":
			if seenFile || values["filename"] == "" {
				return "", "", nil, invalid()
			}
			seenFile = true
			name = values["filename"] // Keep the raw name so service rejects paths.
			file, err = io.ReadAll(io.LimitReader(part, 10*1024*1024+1))
			if err != nil {
				return "", "", nil, invalid()
			}
			if len(file) > 10*1024*1024 {
				return "", "", nil, &platform.Error{Status: 413, Code: "INVALID_INPUT"}
			}
		default:
			return "", "", nil, invalid()
		}
	}
	if !seenText || (text == "" && len(file) == 0) || (seenFile && len(file) == 0) {
		return "", "", nil, invalid()
	}
	return text, name, file, nil
}

func (a *API) supportGet(c *echo.Context, operator bool) error {
	actor, target, err := a.supportActor(c, false, operator)
	if err != nil {
		return err
	}
	out, err := a.svc.Support(c.Request().Context(), actor, target, operator)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) supportHistory(c *echo.Context, operator bool) error {
	actor, target, err := a.supportActor(c, true, operator)
	if err != nil {
		return err
	}
	in, err := decode[wire.SupportHistoryInput](a, c, "SupportHistoryInput")
	if err != nil {
		return err
	}
	out, err := a.svc.SupportHistory(c.Request().Context(), actor, target, operator, in.BeforeSequence)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) supportCreate(c *echo.Context, operator bool) error {
	actor, target, err := a.supportActor(c, true, operator)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	text, name, file, err := readSupportMessage(a, c)
	if err != nil {
		return err
	}
	out, created, err := a.svc.CreateSupportMessage(c.Request().Context(), actor, target, operator, key, text, name, file)
	if err != nil {
		return err
	}
	if created {
		return c.JSON(201, out)
	}
	return c.JSON(200, out)
}
func (a *API) supportRead(c *echo.Context, operator bool) error {
	actor, target, err := a.supportActor(c, true, operator)
	if err != nil {
		return err
	}
	in, err := decode[wire.SupportReadInput](a, c, "SupportReadInput")
	if err != nil {
		return err
	}
	if err = a.svc.AcknowledgeSupport(c.Request().Context(), actor, target, operator, in.Sequence); err != nil {
		return err
	}
	return c.NoContent(204)
}
func (a *API) supportState(c *echo.Context, operator bool) error {
	actor, target, err := a.supportActor(c, true, operator)
	if err != nil {
		return err
	}
	in, err := decode[wire.SupportStateInput](a, c, "SupportStateInput")
	if err != nil {
		return err
	}
	if err = a.svc.SetSupportState(c.Request().Context(), actor, target, operator, string(in.Status)); err != nil {
		return err
	}
	return c.NoContent(204)
}
func (a *API) GetSupport(c *echo.Context) error                   { return a.supportGet(c, false) }
func (a *API) GetSupportHistory(c *echo.Context) error            { return a.supportHistory(c, false) }
func (a *API) CreateSupportMessage(c *echo.Context) error         { return a.supportCreate(c, false) }
func (a *API) AcknowledgeSupport(c *echo.Context) error           { return a.supportRead(c, false) }
func (a *API) SetSupportState(c *echo.Context) error              { return a.supportState(c, false) }
func (a *API) GetOperatorSupport(c *echo.Context) error           { return a.supportGet(c, true) }
func (a *API) GetOperatorSupportHistory(c *echo.Context) error    { return a.supportHistory(c, true) }
func (a *API) CreateOperatorSupportMessage(c *echo.Context) error { return a.supportCreate(c, true) }
func (a *API) AcknowledgeOperatorSupport(c *echo.Context) error   { return a.supportRead(c, true) }
func (a *API) SetOperatorSupportState(c *echo.Context) error      { return a.supportState(c, true) }
func (a *API) SetOperatorSupportBan(c *echo.Context) error {
	actor, target, err := a.supportActor(c, true, true)
	if err != nil {
		return err
	}
	in, err := decode[wire.SupportBanInput](a, c, "SupportBanInput")
	if err != nil {
		return err
	}
	if err = a.svc.SetSupportBan(c.Request().Context(), actor, target, in.Banned, in.Reason); err != nil {
		return err
	}
	return c.NoContent(204)
}
func (a *API) GetSupportAttachment(c *echo.Context) error {
	account, err := a.auth(c, false)
	if err != nil {
		return err
	}
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	name, body, err := a.svc.SupportAttachment(c.Request().Context(), account.Account.AccountId, id)
	if err != nil {
		return err
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if disposition == "" || strings.ContainsAny(disposition, "\r\n") {
		return invalid()
	}
	c.Response().Header().Set("Content-Disposition", disposition)
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	return c.Blob(http.StatusOK, "application/octet-stream", body)
}
