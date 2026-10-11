# 3xui-mole

![3xui-mole](docs/assets/3xui-mole-banner.png)

[Русский](README.ru_RU.md)

A web cabinet and Telegram interface for subscription access managed by 3X-UI.
One Go process owns HTTP, River jobs, main/support Telegram and schedulers.
The web application and Caddy run in a separate image; PostgreSQL holds product
state and Redis supports throttling. Product Python and the transitional HTTP
bot adapter have been retired.

The new project **3xui-mole** starts its own versioning at **0.1.0**;
preview builds use `0.1.0-dev.N`. Its GitHub repository is still
[`ekho/3xui-shop`](https://github.com/ekho/3xui-shop), and the current GHCR image
addresses remain unchanged; see [preview releases](docs/releases.md).
Historical records and upstream references retain their original names.

Set the optional `PRODUCT_NAME=3xui-mole` web runtime setting to display this
name in the cabinet header and browser tab. Existing deployment names and the
generic cabinet labels remain available.

Build the backend with Go 1.27.1 and the web application with Node 24.11.1:

```sh
go -C backend build -o server ./cmd/server
npm --prefix web ci --ignore-scripts
npm --prefix web run build
```

Run `server migrate` before `server serve` with the deployment's private
configuration files. `server reconcile` is the bounded recovery executor and
cannot run beside `serve` against the same PostgreSQL database.

- [Deployment and configuration](DEPLOYMENT.md)
- [Local acceptance](docs/runbooks/s01-test-rollout.md)
- [SQLite data migration](deploy/data-migration/README.md)
- [Cutover, late payments and rollback](deploy/cutover/README.md)
- [Preview backend/web releases](docs/releases.md)
- [API contract](docs/api/openapi.yaml)

Feature branches start at current `origin/v2`; pull requests target `v2`.
Local acceptance uses owned synthetic resources. A production cutover requires
separate authorization for its exact environment and operation.
