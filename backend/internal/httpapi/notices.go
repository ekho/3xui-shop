package httpapi

import (
	"net/url"
	"strconv"

	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
)

func (a *API) PreviewNotice(c *echo.Context) error {
	auth, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	in, err := decode[wire.NoticePreviewInput](a, c, "NoticePreviewInput")
	if err != nil {
		return err
	}
	n := notifications.NoticePreviewInput{Mode: string(in.Mode), Reason: in.Reason}
	switch n.Mode {
	case "send":
		if in.Audience == nil || in.Body == nil || in.ExpectedRevision != nil {
			return invalid()
		}
	case "edit":
		if in.Audience != nil || in.AccountId != nil || in.ExpectedRevision == nil || in.Body == nil {
			return invalid()
		}
	case "delete":
		if in.Audience != nil || in.AccountId != nil || in.ExpectedRevision == nil || in.Body != nil {
			return invalid()
		}
	default:
		return invalid()
	}
	if in.Audience != nil {
		n.Audience = string(*in.Audience)
	}
	if in.Body != nil {
		n.Body = *in.Body
	}
	if in.AccountId != nil {
		n.AccountID = *in.AccountId
	}
	if in.ExpectedRevision != nil {
		n.ExpectedRevision = *in.ExpectedRevision
	}
	out, err := a.notices.Preview(c.Request().Context(), auth.Account.AccountId, n)
	if err != nil {
		return notificationError(err)
	}
	return c.JSON(201, out)
}
func (a *API) ConfirmNotice(c *echo.Context) error {
	auth, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	if id.String() != c.Param("id") {
		return invalid()
	}
	in, err := decode[wire.NoticeConfirmInput](a, c, "NoticeConfirmInput")
	if err != nil {
		return err
	}
	if !bool(in.Confirmed) {
		return invalid()
	}
	out, err := a.notices.Confirm(c.Request().Context(), auth.Account.AccountId, id)
	if err != nil {
		return notificationError(err)
	}
	return c.JSON(200, out)
}
func (a *API) GetLastNotice(c *echo.Context) error {
	auth, err := a.operatorAuth(c, false)
	if err != nil {
		return err
	}
	out, err := a.notices.Last(c.Request().Context(), auth.Account.AccountId)
	if err != nil {
		return notificationError(err)
	}
	return c.JSON(200, out)
}
func (a *API) GetNotices(c *echo.Context) error {
	auth, err := a.auth(c, false)
	if err != nil {
		return err
	}
	q, err := url.ParseQuery(c.Request().URL.RawQuery)
	if err != nil {
		return invalid()
	}
	page := int64(1)
	if len(q) > 0 {
		if len(q) != 1 || len(q["page"]) != 1 {
			return invalid()
		}
		page, err = strconv.ParseInt(q.Get("page"), 10, 32)
		if err != nil || page < 1 || strconv.FormatInt(page, 10) != q.Get("page") {
			return invalid()
		}
	}
	out, err := a.notices.Read(c.Request().Context(), auth.Account.ID, int32(page))
	if err != nil {
		return notificationError(err)
	}
	return c.JSON(200, out)
}
func (a *API) SetNoticeEmailPreference(c *echo.Context) error {
	auth, err := a.auth(c, true)
	if err != nil {
		return err
	}
	in, err := decode[wire.NoticePreferenceInput](a, c, "NoticePreferenceInput")
	if err != nil {
		return err
	}
	out, err := a.notices.SetEmailPreference(c.Request().Context(), auth.Account.ID, in.EmailEnabled)
	if err != nil {
		return notificationError(err)
	}
	return c.JSON(200, out)
}
func (a *API) DismissNotice(c *echo.Context) error {
	auth, err := a.auth(c, true)
	if err != nil {
		return err
	}
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	if id.String() != c.Param("id") {
		return invalid()
	}
	if err = requireEmptyBody(c); err != nil {
		return err
	}
	if err = a.notices.Dismiss(c.Request().Context(), auth.Account.ID, id); err != nil {
		return notificationError(err)
	}
	return c.NoContent(204)
}
