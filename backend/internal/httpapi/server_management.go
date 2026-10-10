package httpapi

import (
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
	"net/url"
)

func managedServerID(c *echo.Context) (string, error) {
	id, err := url.PathUnescape(c.Param("server_id"))
	if err != nil || id == "" {
		return "", invalid()
	}
	return id, nil
}

func serverSummary(v vpn.Server) wire.OperatorServerSummary {
	return wire.OperatorServerSummary{Id: v.ID, Name: v.Name, Host: v.Host, MaxClients: v.MaxClients,
		Online: v.Online, ObservedAt: v.ObservedAt, AssignedClients: v.AssignedClients,
		ReservedClients: v.ReservedClients, Retired: v.Retired}
}
func serverDetail(v vpn.Server) wire.OperatorServerDetail {
	return wire.OperatorServerDetail{Id: v.ID, Name: v.Name, Host: v.Host, MaxClients: v.MaxClients,
		Online: v.Online, ObservedAt: v.ObservedAt, AssignedClients: v.AssignedClients,
		ReservedClients: v.ReservedClients, Retired: v.Retired, CanPing: !v.Retired,
		CanDelete: !v.Retired && v.Online && v.AssignedClients == 0 && v.ReservedClients == 0}
}
func serverList(rows []vpn.Server) wire.OperatorServers {
	out := wire.OperatorServers{Servers: make([]wire.OperatorServerSummary, 0, len(rows))}
	for _, row := range rows {
		out.Servers = append(out.Servers, serverSummary(row))
	}
	return out
}

func (a *API) ListManagedServers(c *echo.Context) error {
	actor, err := a.operatorAuth(c, false)
	if err != nil {
		return err
	}
	rows, err := a.vpn.ListManagedServers(c.Request().Context(), actor.Account.AccountId)
	if err != nil {
		return subscriptionError(err)
	}
	return c.JSON(200, serverList(rows))
}
func (a *API) GetManagedServer(c *echo.Context) error {
	actor, err := a.operatorAuth(c, false)
	if err != nil {
		return err
	}
	id, err := managedServerID(c)
	if err != nil {
		return err
	}
	v, err := a.vpn.GetManagedServer(c.Request().Context(), actor.Account.AccountId, id)
	if err != nil {
		return subscriptionError(err)
	}
	return c.JSON(200, serverDetail(v))
}
func (a *API) CreateManagedServer(c *echo.Context) error {
	actor, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.OperatorServerCreateInput](a, c, "OperatorServerCreateInput")
	if err != nil {
		return err
	}
	v, created, err := a.vpn.CreateManagedServer(c.Request().Context(), actor.Account.AccountId, key, vpn.ServerInput{Name: in.Name, Host: in.Host, MaxClients: int64(in.MaxClients)}, "web")
	if err != nil {
		return subscriptionError(err)
	}
	status := 200
	if created {
		status = 201
	}
	return c.JSON(status, serverDetail(v))
}
func (a *API) PingManagedServer(c *echo.Context) error {
	actor, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	if _, err = decode[wire.OperatorServerEmptyInput](a, c, "OperatorServerEmptyInput"); err != nil {
		return err
	}
	id, err := managedServerID(c)
	if err != nil {
		return err
	}
	v, err := a.vpn.PingManagedServer(c.Request().Context(), actor.Account.AccountId, id, key, "web")
	if err != nil {
		return subscriptionError(err)
	}
	return c.JSON(200, serverDetail(v))
}
func (a *API) SyncManagedServers(c *echo.Context) error {
	actor, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	if _, err = decode[wire.OperatorServerEmptyInput](a, c, "OperatorServerEmptyInput"); err != nil {
		return err
	}
	rows, err := a.vpn.SyncManagedServers(c.Request().Context(), actor.Account.AccountId, key, "web")
	if err != nil {
		return subscriptionError(err)
	}
	return c.JSON(200, serverList(rows))
}
func (a *API) DeleteManagedServer(c *echo.Context) error {
	actor, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.OperatorServerDeleteInput](a, c, "OperatorServerDeleteInput")
	if err != nil {
		return err
	}
	if !bool(in.Confirmation) {
		return invalid()
	}
	id, err := managedServerID(c)
	if err != nil {
		return err
	}
	v, err := a.vpn.DeleteManagedServer(c.Request().Context(), actor.Account.AccountId, id, key, "web")
	if err != nil {
		return subscriptionError(err)
	}
	return c.JSON(200, serverDetail(v))
}
