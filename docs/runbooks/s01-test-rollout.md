# Локальная проверка v2

После С47 приёмка запускает Go runtime и отдельный web/Caddy; Python используется
только для независимых stdlib fixtures/orchestration. Старые adapter-команды
сохранены как [историческая версия](https://github.com/ekho/3xui-shop/blob/e53746c4a13d4209438012a33390ec84eb38539c/docs/runbooks/s01-test-rollout.md).

## Connected Go и браузер

Нужны Docker Compose, Go 1.27.1, Node 24.11.1, Python 3.13, OpenSSL и Chromium.
Сначала проверьте, что project/ports/networks не заняты чужим fixture. При
конфликте создайте собственную Compose-копию с отдельными port/subnet/project
и укажите её через `TEST_POSTGRES_COMPOSE_FILE`; чужие контейнеры не останавливать.

```sh
npm --prefix web ci --ignore-scripts
(cd web && npx playwright install chromium)
docker compose -f deploy/acceptance/compose.test.yml up -d --wait postgres redis
umask 077
mkdir -p .superpowers/acceptance/test-inputs
printf '%s' 'postgres://platform_test@127.0.0.1:55491/platform_test?sslmode=disable' > .superpowers/acceptance/test-inputs/database-url
printf '%s' 'redis://127.0.0.1:56391/0' > .superpowers/acceptance/test-inputs/redis-url
docker build --target operations -t cabinet-migration-operations:local backend
export TEST_DATABASE_URL_FILE="$(pwd)/.superpowers/acceptance/test-inputs/database-url"
export TEST_REDIS_URL_FILE="$(pwd)/.superpowers/acceptance/test-inputs/redis-url"
export TEST_OPERATIONS_IMAGE=cabinet-migration-operations:local
RUN_BROWSER_TESTS=1 make -C backend test-integration TEST_TIMEOUT=60m
python3 -m unittest discover -s tests -v
npm --prefix web run test:e2e
```

Входные файлы connected tests — абсолютные, приватные и принадлежат текущему
пользователю. Explicit неверный selector не разрешает fallback. Native tests
запускают синтетический TLS Bot API без внешнего poller; trial decisions, роли,
конфликт, повторы и restart вызывают публичные Go операции. Browser использует
настоящий HTTPS API/PG/Redis/River. Исключение для локального `httptest` сертификата
ограничено browser fixture; TLS checks продукта остаются включены.

Generated contracts и статические проверки:

```sh
python3 deploy/acceptance/check_names.py
make -C backend generate
npm --prefix web run api:generate
git diff --exit-code -- backend/internal/wire backend/internal/modules web/src/api/schema.gen.ts
go -C backend vet ./...
npm --prefix web run typecheck
npm --prefix web run build -- --mode test
(cd web && node scripts/runtime-config.test.mjs)
```

Проверка generated diff выполняется относительно зафиксированного implementation
commit; в рабочем изменении сначала оцените ожидаемый generated diff.

## Docker и настоящая локальная панель

```sh
docker compose --env-file deploy/acceptance/.env.example -f deploy/acceptance/compose.acceptance.yml config --quiet
docker compose --env-file deploy/acceptance/.env.example -f deploy/acceptance/compose.acceptance.yml build backend gateway
python3 deploy/acceptance/smoke.py
python3 deploy/acceptance/local.py up --reuse-images
python3 deploy/acceptance/local.py check
python3 deploy/server-management/local.py up
python3 deploy/server-management/local.py check
python3 deploy/acceptance/backup_restore.py up
python3 deploy/acceptance/backup_restore.py check
```

Native fixture поднимает закреплённую 3X-UI 3.7.0, TLS SMTP, PostgreSQL/Redis и
синтетические HTTPS keys. Проверяются операции и panel readback, restart, повтор
после потерянного ответа, VPN data plane и сохранённые identities. Две панели
server-management проверяют retirement/reconciliation, browser и реальные группы.
Это отдельные доказательства от unit tests и `/readyz`.

Backup fixture создаёт own dynamic ports/project и приватный ownership selector,
проверяет PostgreSQL schema/counts/sequences и attachments в новой БД.
`TestPopulatedLegacyMigrationPreservesNativeFacts` дополнительно сохраняет полный
digest всех public tables после funded/pending/grant/reward/late-receipt фактов,
включённого обслуживания и повторного import/restart. [Cutover](../../deploy/cutover/README.md)
фиксирует порядок передачи владельцев и ограничения rollback.

После проверки останавливайте только созданные этими командами проекты:

```sh
python3 deploy/acceptance/backup_restore.py down
python3 deploy/server-management/local.py down
python3 deploy/acceptance/local.py down
docker compose -f deploy/acceptance/compose.test.yml down -v
```

## Внешняя приёмка

Настоящие Telegram/PSP/SMTP, пользовательская 3X-UI и production здесь не
используются. Для внешней приёмки владелец отдельно разрешает окружение,
проверяет domain/TLS, bot/group/merchant identities, credentials, panel/client
совместимость, состояния очередей и одного активного исполнителя. Не менять
VPN UUID/subId/panel key/server ID и не выдавать деньги/доступ из unknown history.
Closed/Done означает локальную приёмку и merge в `v2`, а не production cutover.
