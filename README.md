# 3xui-shop v2

[Русский](README.ru_RU.md)

A web cabinet and Telegram interface for subscription access managed by 3X-UI.
One Go process owns HTTP, River jobs, main/support Telegram and schedulers.
The web application and Caddy run in a separate image; PostgreSQL holds product
state and Redis supports throttling. Product Python and the transitional HTTP
bot adapter have been retired.

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
- [Preview backend/web releases](docs/releases-v2.md)
- [API contract](docs/api/openapi.yaml)

Feature branches start at current `origin/v2`; pull requests target `v2`.
Local acceptance uses owned synthetic resources. A production cutover requires
separate authorization for its exact environment and operation.
