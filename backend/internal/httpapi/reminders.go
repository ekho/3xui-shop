package httpapi

import (
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
)

func toReminders(in notifications.ReminderResult) wire.ReminderResult {
	out := wire.ReminderResult{Version: wire.ReminderResultVersion(in.Version), EmailEnabled: in.EmailEnabled, EmailAvailable: in.EmailAvailable, Reminders: []wire.Reminder{}}
	for _, r := range in.Reminders {
		out.Reminders = append(out.Reminders, wire.Reminder{Id: r.ID, Kind: wire.ReminderKind(r.Kind), Threshold: wire.ReminderThreshold(r.Threshold), ObservedAt: r.ObservedAt, ExpiresAt: r.ExpiresAt, TrafficUsedBytes: r.TrafficUsedBytes, TrafficLimitBytes: r.TrafficLimitBytes, PaidUntil: r.PaidUntil, Route: wire.ReminderRoute(r.Route)})
	}
	return out
}

func (a *API) GetReminders(c *echo.Context) error {
	auth, err := a.auth(c, false)
	if err != nil {
		return err
	}
	out, err := a.reminders.Read(c.Request().Context(), auth.Account.ID)
	if err != nil {
		return notificationError(err)
	}
	return c.JSON(200, toReminders(out))
}

func (a *API) SetReminderEmailPreference(c *echo.Context) error {
	auth, err := a.auth(c, true)
	if err != nil {
		return err
	}
	in, err := decode[wire.ReminderPreferenceInput](a, c, "ReminderPreferenceInput")
	if err != nil {
		return err
	}
	out, err := a.reminders.SetEmailPreference(c.Request().Context(), auth.Account.ID, in.EmailEnabled)
	if err != nil {
		return notificationError(err)
	}
	return c.JSON(200, toReminders(out))
}

func (a *API) DismissReminder(c *echo.Context) error {
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
	if err = a.reminders.Dismiss(c.Request().Context(), auth.Account.ID, id); err != nil {
		return notificationError(err)
	}
	return c.NoContent(204)
}
