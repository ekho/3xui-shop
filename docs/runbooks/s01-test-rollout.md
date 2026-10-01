# С01: локальная проверка и приёмка на выделенном стенде

Реализация: Go/Echo API, PostgreSQL/pgx/sqlc, River, Redis, React/TypeScript/Vite;
бот использует private HTTP API. Граница — новые web-аккаунты и триал через апрув.
Старые SQLite-аккаунты, платежи и production compose этот выпуск не переносит.

## Что уже можно повторить локально

Нужны Docker Compose, Go 1.27.1, Node 24.11.1, Poetry 2.5.1 и Python 3.13.
Запускать из корня отдельной feature-ветки. URL-файлы — только для собственной
локальной тестовой PostgreSQL/Redis, не для production.

```sh
docker compose -f deploy/s01/compose.test.yml up -d --wait postgres redis
poetry install --no-interaction
npm --prefix web ci --ignore-scripts
(cd web && npx playwright install chromium)
```

Создать вне Git два файла, mode0600, с локальными тестовыми URL:
`postgres://s01_test@127.0.0.1:55491/s01_test?sslmode=disable` и
`redis://127.0.0.1:56391/0`. Передать **пути** через
`S01_TEST_DATABASE_URL_FILE` и `S01_TEST_REDIS_URL_FILE`.
Fixture создаёт свою случайную БД и удаляет только её. Restore-тест использует
именно этот локальный контейнер и собственные временные dump-файлы, mode0600.

```sh
make -C backend generate
npm --prefix web run api:generate
git diff --exit-code backend/internal/store backend/internal/wire web/src/api/schema.gen.ts
go -C backend vet ./...
make -C backend test-integration
poetry run python -m unittest discover -s tests -v
npm --prefix web run typecheck
npm --prefix web run test:e2e
(cd web && S01_E2E_MODE=real npm run test:e2e)
```

Real API browser mode означает настоящий HTTPS API, PG/Redis/River, Python
consumer и браузер. Панель, SMTP и Telegram transport в этом режиме — fixtures;
он не доказывает фактическое подключение VPN. TLS bypass разрешён только
браузеру к локальному `httptest` сертификату; Python доверяет отдельному CA,
production-клиенты не отключают проверку сертификатов. Reporter этого режима
не печатает ошибки с паролями, email-токенами или ссылками подписки.

Для контейнерной сборки и проверки маршрутов:

```sh
docker compose --env-file deploy/s01/.env.example -f deploy/s01/compose.acceptance.yml config --quiet
docker compose --env-file deploy/s01/.env.example -f deploy/s01/compose.acceptance.yml build
python3 deploy/s01/smoke.py
```

Smoke создаёт отдельный `cabinet-s01-smoke`, временные ключи/сертификаты и свою
PG volume, проверяет HTTPS, закрытый public `/internal`, private HTTPS, доступ
к secret files, повторные миграции и остановку ingress. Telegram poller не
запускается, внешние SMTP/панель не вызываются. Затем удаляется только smoke
project. Базы `compose.test.yml` и будущего acceptance project сохраняются.

## Ресурсы для реальной приёмки — предоставляет владелец

| Ресурс | Условие готовности |
| --- | --- |
| Выделенная 3X-UI | Client-centric v3.1+, подтверждённая версия и API; regular inbounds; никаких legacy writers в этой панели |
| Subscription endpoint | HTTPS URL доступен поддерживаемому VPN-клиенту, подходит `subId`16 |
| SMTP | TLS endpoint, test sender/учётная запись, надёжная доставка в настоящий test mailbox |
| Test bot и оператор | Отдельный bot token; оператор начал личный чат; allowlist соответствует BOT_ADMINS + DEV_ID основного бота |
| HTTPS origin | DNS/сертификат cabinet; `CABINET_ORIGIN` точно соответствует браузеру и письмам, включая внешний порт |
| Private HTTPS | Сертификат с SAN `gateway`, CA доверен адаптеру; 9443 не публикуется на host |
| Политики/поддержка | Опубликованные terms/privacy версии совпадают в backend и сборке; email/web контакт работает без Telegram |
| Файлы секретов | PG password и URL, Redis URL, MAIL_KEY/CODE_KEY (base64 32 bytes каждый), adapter token, SMTP/panel credentials, TLS keys и отдельный test bot token |

Ключи MAIL_KEY/CODE_KEY хранятся вне PostgreSQL и резервируются отдельно.
Утрата MAIL_KEY не позволяет расшифровать ещё не доставленные письма. Секреты
не вставляются в .env, CLI values, build args, логи или evidence. В .env только
публичные значения и абсолютные пути к файлам. Контейнерные UID/GID должны
читать mode0600 файлы; Compose file-backed secrets не меняют владельца файла.
Указать `S01_RUNTIME_UID/GID` соответствующим владельцу на тестовой машине.

## Порядок запуска выделенного acceptance project

1. Подготовить private config вне Git по `deploy/s01/.env.example`. `DATABASE_URL_FILE`
   должен указывать на database `cabinet_s01`, host `postgres:5432`; password
   соответствует PG password file. Redis — `redis:6379/0`. Никаких root .env/app/data mounts.
2. Убедиться, что выбранный subnet свободен; IP gateway и TRUSTED_PROXY_CIDRS `/32`
   согласованы. Host port по умолчанию `127.0.0.1:58443`; внешний HTTPS origin
   задаётся отдельно. Default origin example использует этот порт.
3. Оставить `TRIAL_ENABLED=false`, `PANEL_DUPLICATE_GUARD_VERIFIED=false` до panel preflight.
   Выполнить `config --quiet` с подготовленным env file. Backend/gateway не публикуются
   напрямую; единственный host listener — public HTTPS gateway.
4. Собрать образы; выполнить one-shot `migrate`, затем `backend/gateway/bot` с
   `compose.acceptance.yml`. Compose ожидает успешные SQL и River migrations
   до backend. Повторный `migrate` не очищает данные.
5. Проверить `/healthz` по HTTPS. Public `/internal/*` должен вернуть404 даже
   с service credential; private listener должен пропускать только `/internal/*`.
   В браузере internal credential отсутствует. TLS verification включён.
6. После panel preflight включить trial и пересоздать backend. Исходный snapshot
   уже принятых операций не меняется при изменении этих настроек.

Dedicated acceptance bot использует те же adapter/handlers, но не запускает
legacy SQLite/payment jobs. Проверка main-bot startup/shutdown отдельно покрыта
автоматическими тестами. Production bot token в этот стенд не предоставляется.

## Panel preflight до первого trial

Записать только версии/форматы/результаты, без учётных данных и клиентских ключей.
На disposable test client проверить GET `panel/api/inbounds/list`, GET
`panel/api/clients/get/{panel_key}`, POST `panel/api/clients/add`, POST
`panel/api/clients/{panel_key}/attach`. Required readback — `client.id/email/subId`,
`expiryTime`, `limitIp`, `totalGB`, `enable`, `inboundIds`; usage может отсутствовать.
Backend считает absence только точный `success:false,msg:"record not found",obj:null`
(или отсутствующий obj). Empty object/200 без полей/timeout не считаются absence.
Несовпадение формата или версии — compatibility blocker, включать trial нельзя.

Проверить enabled tags с отдельным hyphen-сегментом regular и возможность
добавлять членство, сохраняя protocol fields. Нельзя применять reset/delete/update
для повторной выдачи. Проверить duplicate panel_key и VPN UUID на изолированных
тестовых данных; только доказанная гарантия позволяет включить
`PANEL_DUPLICATE_GUARD_VERIFIED=true`. До неё неоднозначный create не повторяется
по «пустому» чтению; reservation остаётся, поддержка сверяет исходную operation.

## Реальный пользовательский сценарий

Новый email без Telegram → письмо → explicit verify с password → login → заявка →
настоящая кнопка оператора в личном чате → подтверждённая выдача → отдельный key
fetch → copy/manual import в Happ → фактическое подключение и запрос через VPN.
Happ уже предложен текущим ботом; clipboard subscriptions описаны в
[официальной документации](https://www.happ.su/main/faq/adding-configuration-subscription).
Версию приложения и факт соединения заполнить после реального теста.

Проверить отказ отдельному аккаунту, отсутствие повторной клиентской заявки,
support reconsider с причиной и старую карточку. Остановить bot до решения:
заявка должна дождаться его возвращения. После commit approve остановить bot:
выдача должна завершиться backend. Тестировать ограничение аккаунта до worker.
В evidence сохранить дату/версии/статусы; email, UUID VPN, subId и subscription URL
не публиковать. Доступ к backup с действующими sessions ограничен владельцем.

## Backup/restore при неоднозначной выдаче

Автоматическая репетиция: `go -C backend test ./tests -run '^TestS01BackupRestore$' -count=1`.
Она делает настоящий pg_dump/pg_restore после external add и до DB applied,
с running operation, immutable target, reserved grant, River job и session.
Восстановление в отдельную БД запускает только provision queue; ключи/expiry
сохраняются, внешний клиент создаётся один раз, owner key fetch работает.
Панель этого автоматического теста — HTTPS fixture. Повторить на реальной
выделенной панели до закрытия AC19.

На реальном стенде: остановить новые writers и backend jobs, убедиться, что
нет живого provisioning owner; сохранить согласованный PG dump, файлы внешних
ключей и соответствующую версию приложения. Восстановить в **новую пустую БД**,
не поверх действующей. Старая БД/worker не должны продолжать выдачу в ту же панель.
Сохранить panel ownership и те же keys. Сначала открыть только reconcile worker,
сверить reservation, UUID/subId/expiry/bytes/N+1 и один Grant; потом owner access.
Никакого отката PostgreSQL старым snapshot или автоматического создания новой
operation при потерянном ответе.

## Bounded rollback и будущее окно обслуживания

Остановить bot и gateway (новые registrations/requests/decisions), сохранить PG,
queue и исходные keys, оставить backend для завершения принятых jobs. Если
подозревается неверная выдача, остановить и backend; устранить причину, затем
разрешить сверку той же operation. Не удалять volumes и не запускать legacy
writers для web-аккаунтов. Xray не требуется останавливать, ключи не перевыдавать.

Для будущего переноса сценария: maintenance window → остановить соответствующие
старые writers → мигрировать/проверить данные → включить одного нового владельца →
smoke. Это инструкция будущего переключения; production сейчас не меняется.

## Что означает «готово»

[Матрица](../evidence/s01-acceptance.md) разделяет локальную реализацию, Git delivery
и реальную приёмку. Green fixtures/build не закрывают реальный VPN/restore.
После предоставления ресурсов заполнить pending строки и зафиксировать точную
ревизию. Push/PR/merge/deploy не выполнены и требуют соответствующего мандата.
