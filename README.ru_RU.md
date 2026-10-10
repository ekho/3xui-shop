# 3xui-mole

![3xui-mole](docs/assets/3xui-mole-banner.png)

[English](README.md)

Веб-кабинет и Telegram-интерфейс для управления подпиской через 3X-UI.
Один Go-процесс обслуживает HTTP, River, основной и support Telegram и schedulers.
Web/Caddy работают отдельным образом, PostgreSQL хранит данные продукта,
Redis используется для ограничения запросов. Старый Python runtime и переходный
HTTP-адаптер бота удалены.

Новый проект **3xui-mole** начинает собственную нумерацию с **0.1.0**;
предварительные сборки используют `0.1.0-dev.N`. Репозиторий GitHub пока остаётся
[`ekho/3xui-shop`](https://github.com/ekho/3xui-shop), действующие адреса образов
GHCR сохраняются; см. [предварительные релизы](docs/releases.md).
Исторические записи и ссылки на upstream сохраняют исходные названия.

Чтобы показать это имя в шапке кабинета и вкладке браузера, задайте необязательный
runtime-параметр web `PRODUCT_NAME=3xui-mole`. Имена существующих установок и
стандартные подписи кабинета сохраняются.

Для сборки нужны Go 1.27.1 и Node 24.11.1:

```sh
go -C backend build -o server ./cmd/server
npm --prefix web ci --ignore-scripts
npm --prefix web run build
```

Перед `server serve` выполните `server migrate` с приватными файлами конфигурации
своего окружения. `server reconcile` — ограниченный recovery executor; он не
работает одновременно с `serve` на той же PostgreSQL.

- [Развёртывание и настройки](DEPLOYMENT.md)
- [Локальная приёмка](docs/runbooks/s01-test-rollout.md)
- [Перенос SQLite](deploy/data-migration/README.md)
- [Переключение, поздние платежи и rollback](deploy/cutover/README.md)
- [Предварительные backend/web релизы](docs/releases.md)
- [Контракт API](docs/api/openapi.yaml)

Рабочие ветки создаются от актуальной `origin/v2`, PR направляются в `v2`.
Локальная приёмка использует собственные синтетические ресурсы. Production
переключение требует отдельного разрешения на конкретное окружение и операцию.
