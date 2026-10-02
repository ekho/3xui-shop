package httpapi

import (
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
	"net/url"
	"strconv"
	"strings"
)

func (a *API) GetCatalogue(c *echo.Context) error {
	account, err := a.auth(c, false)
	if err != nil {
		return err
	}
	out, err := a.svc.Catalogue(c.Request().Context(), account.Account.AccountId)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

func cataloguePage(c *echo.Context) (int, int, error) {
	for _, part := range strings.Split(c.Request().URL.RawQuery, "&") {
		if part == "" {
			return 0, 0, invalid()
		}
	}
	query, parseErr := url.ParseQuery(c.Request().URL.RawQuery)
	if parseErr != nil {
		return 0, 0, invalid()
	}
	for key, values := range query {
		if (key != "page" && key != "per_page") || len(values) != 1 {
			return 0, 0, invalid()
		}
	}
	parse := func(key string, max int) (int, error) {
		value, exists := query[key]
		if !exists {
			return 0, invalid()
		}
		if value[0] == "" {
			return 0, invalid()
		}
		for _, digit := range value[0] {
			if digit < '0' || digit > '9' {
				return 0, invalid()
			}
		}
		n, err := strconv.ParseInt(value[0], 10, 32)
		if err != nil || n < 1 || n > int64(max) {
			return 0, invalid()
		}
		return int(n), nil
	}
	page, err := parse("page", 2147483647)
	if err != nil {
		return 0, 0, err
	}
	perPage, err := parse("per_page", 50)
	if err != nil {
		return 0, 0, err
	}
	return page, perPage, nil
}

func (a *API) GetOperatorCatalogue(c *echo.Context) error {
	page, perPage, err := cataloguePage(c)
	if err != nil {
		return err
	}
	account, err := a.operatorAuth(c, false)
	if err != nil {
		return err
	}
	out, err := a.svc.OperatorCatalogue(c.Request().Context(), account.Account.AccountId, page, perPage)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

func (a *API) CreateCataloguePlan(c *echo.Context) error {
	account, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.CataloguePlanCreateInput](a, c, "CataloguePlanCreateInput")
	if err != nil {
		return err
	}
	out, err := a.svc.CreateCataloguePlan(c.Request().Context(), account.Account.AccountId, key, in)
	if err != nil {
		return err
	}
	return c.JSON(201, out)
}

func (a *API) ReviseCataloguePlan(c *echo.Context) error {
	actor, id, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.CataloguePlanRevisionInput](a, c, "CataloguePlanRevisionInput")
	if err != nil {
		return err
	}
	out, err := a.svc.ReviseCataloguePlan(c.Request().Context(), actor, id, key, in)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

func (a *API) ArchiveCataloguePlan(c *echo.Context) error {
	actor, id, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.CataloguePlanArchiveInput](a, c, "CataloguePlanArchiveInput")
	if err != nil {
		return err
	}
	out, err := a.svc.ArchiveCataloguePlan(c.Request().Context(), actor, id, key, in)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
