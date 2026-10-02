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
к secret files, повторные миграции и остановку ingress. Режим `reconcile`
в поставляемом образе восстанавливает зависшую River job только в provision
queue; почтовая job остаётся без попыток, HTTP listener отсутствует. Telegram poller не
запускается, внешние SMTP/панель не вызываются. Затем удаляется только smoke
project. Базы `compose.test.yml` и будущего acceptance project сохраняются.

## Локальный Docker-стенд с настоящей панелью

Из корня feature-worktree, с Docker, OpenSSL, Python и установленным web Playwright:

```sh
python3 deploy/s01/local.py up
python3 deploy/s01/local.py check
node deploy/s01/browser.mjs
```

`up` собирает backend/web/adapter и поднимает только проект `cabinet-s01-local`:
PG/Redis, 3X-UI **3.7.0**, Mailpit **1.31.1**, HTTPS gateway и тестовый origin.
Images панели/почты закреплены digest в `compose.local.yml`; panel и VPN-client
используют один multi-platform image3.7.0, index digest
`sha256:3b3131f1876e6bf35063a9ec4dd1c594e4525180bfc2e1c477dcc8a3c9550ca1`.
Версия соответствует production по сообщению владельца; production здесь не проверяется. Сначала trial выключен;
native API preflight проверяет absence, дубли и attach на удаляемых probe-клиентах.
Панель использует собственную новую SQLite; initial credentials заменяются на
сгенерированные. Нет production mounts, системного trust/hosts/VPN-переключения.

| Доступ на этом компьютере | Адрес |
| --- | --- |
| Кабинет | `https://localhost:58443` |
| Панель | `https://localhost:59444`, login `local-operator` |
| Mailpit UI | `https://localhost:59446` |
| Подписки | `https://localhost:59445/sub/` |

Self-signed CA доверяется только тестовым клиентам; Chromium доверяет SPKI этого
сертификата, без общего TLS bypass. Для ручного браузера нужен локальный certificate
exception. Все private state/секреты лежат в ignored
`.superpowers/sdd/2026-10-01-s01-web-trial/local-docker` (dir0700/files0600).
Пароль панели — файл `panel-password`; не копировать содержимое в чат/лог.
Порты привязаны к loopback; subnet `172.31.99.0/28` должен быть свободен.
Панель копирует Xray в свой executable tmpfs: её native config пишется рядом
с бинарником, root/capabilities не нужны. SMTP принимает только `@example.test`
через TLS/auth, исходящей доставки/relay нет. Terms/privacy — явно тестовые fixtures.

`check` проверяет email/password/API, решение через поставляемый Python adapter,
readback UUID/subId/expiry/N+1/bytes, HTTPS-подписку и VLESS/TLS **Xray26.7.28**.
HTTP proxy Xray направляет запрос только к Docker origin. Host подписки заменяется
на Docker DNS той же панели; UUID/flow/port берутся из выданной подписки.
Для этого локального теста `up` задаёт в Freedom `finalRules` разрешение только
на текущий IP собственного origin `/32`, TCP8000. Xray26.7.28 блокирует частные
адреса по умолчанию даже после разрешения домена; остальные адреса/порты
сохраняют штатные ограничения. Источник: [Xray finalRules](https://xtls.github.io/en/config/outbounds/freedom.html#finalrules).
Это не проверка ручного импорта в Happ. Telegram transport не вызывается.

Затем `check` делает настоящий PG dump в момент reserved grant + running River job:
клиент уже существует в панели, фиксация applied задержана test trigger. Он убивает
только свой backend, восстанавливает dump в новую собственную БД и запускает
только `reconcile`. В restored fixture сдвигается `attempted_at` на4min; штатный
River rescue выполняет сверку. Target/UUID/subId/expiry/limits и один client/grant
сохраняются; прежняя owner session и VPN проверяются после возврата backend.
После проверки `database-url` указывает на восстановленную БД. Dumps mode0600,
не публиковать. Проверка создаёт test accounts и может повторяться.
`check` также останавливает gateway: новый ingress недоступен, PG/панель/существующий
VPN сохранены; затем возвращает gateway. `browser.mjs` отдельно проходит мобильный
кабинет375px, включая no-store/logout.

На 3.7.0 одинаковый panel_key с тем же subId принят без изменения UUID;
UUID в разных inbounds принят. Поэтому `PANEL_DUPLICATE_GUARD_VERIFIED=false`
сохраняется: uncertain-create не повторяется, поддержка сверяет исходную operation.
Native attach сохранил credentials/expiry/limits и добавил только membership.

Остановить только этот стенд, сохранив volumes/private state:

```sh
python3 deploy/s01/local.py down
```

По умолчанию test bot выключен профилем `telegram`. Для настоящего апрува владелец предоставляет
отдельный bot token file и operator ID, начинает личный чат. Нужно согласовать
`BOT_OPERATOR_IDS` в backend и bot, заменить dummy token path в private config;
после этого запустить профиль `telegram` на этом же собственном проекте.
Dummy token не должен использоваться для Telegram-запросов.

## Дополнительные ресурсы для внешней приёмки — предоставляет владелец

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
`panel/api/clients/{panel_key}/attach`. Required readback — `client.uuid/email/subId`,
`expiryTime`, `limitIp`, `totalGB`, `enable`, `inboundIds`; usage может отсутствовать.
Backend считает absence только `success:false,obj:null` (или отсутствующий obj)
с точным msg `record not found` (compatibility fixture), ` (record not found)` (native3.5.0)
либо `Obtain (record not found)` — подтверждён на3.7.0. Backend запрашивает
`Accept-Language: en-US`; другие сообщения не считаются absence. Чтение возвращает числовой `client.id` и отдельный VPN UUID
`client.uuid`; при создании VPN UUID передаётся в `client.id`. Empty object/200 без полей/timeout не считаются absence.
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
fetch → copy → фактическое подключение собственного Docker VPN-клиента и запрос
к своему origin. Переключение живого Happ исключено из приёмки по решению
владельца 2026-10-02. Ручной импорт в Happ5.9.0 ранее подтверждён; его туннель
не заявляется проверенным. Не менять Happ, системный VPN или доверие macOS.

Проверить отказ отдельному аккаунту, отсутствие повторной клиентской заявки,
support reconsider с причиной и старую карточку. Остановить bot до решения:
заявка должна дождаться его возвращения. После commit approve остановить bot:
выдача должна завершиться backend. Тестировать ограничение аккаунта до worker.
В evidence сохранить дату/версии/статусы; email, UUID VPN, subId и subscription URL
не публиковать. Доступ к backup с действующими sessions ограничен владельцем.

## Backup/restore при неоднозначной выдаче

С02 вводит новое правило: восстановленные browser sessions и незавершённые
credential proofs отзываются до reconcile/serve/ingress. Историческая приёмка
С01 проверяла сохранённую owner session; теперь проверяется новый login того
же владельца и прежний VPN. Порядок и SQL — в [runbook С02](s02-account-security.md).

Автоматическая репетиция: `go -C backend test ./tests -run '^TestS01BackupRestore$' -count=1`.
Она делает настоящий pg_dump/pg_restore после external add и до DB applied,
с running operation, immutable target, reserved grant, **running River job** и session.
Восстановление в отдельную БД запускает только provision queue; ключи/expiry
сохраняются, внешний клиент создаётся один раз, owner key fetch работает после
нового login и обязательной restore-очистки С02.
Тест сдвигает только `attempted_at` восстановленной job на четыре минуты назад,
чтобы проверить штатный River rescue без реального ожидания порога.
Панель этого автоматического теста — HTTPS fixture. Повторить на реальной
выделенной панели до закрытия AC19.

На реальном стенде: остановить новые writers и backend jobs, убедиться, что
нет живого provisioning owner; сохранить согласованный PG dump, файлы внешних
ключей и соответствующую версию приложения. Восстановить в **новую пустую БД**,
не поверх действующей. Старая БД/worker не должны продолжать выдачу в ту же панель.
Сохранить panel ownership и те же keys. После восстановления переключить
`DATABASE_URL_FILE` на новую БД. При закрытом ingress и остановленных bot/backend
и mail workers выполнить additive migrate и `post_restore_auth.sql` по runbook
С02. Ошибка любого шага оставляет ingress закрытым. После очистки запустить
поставляемый режим восстановления:

```sh
docker compose --profile restore --env-file /secure/s01/public.env -f deploy/s01/compose.acceptance.yml up --no-build -d reconcile
```

Он не открывает HTTP и не обрабатывает mail queue. Running job становится
доступной для rescue после трёх минут от `attempted_at`; River проверяет её
раз в 30 секунд, затем действует обычная задержка повтора. Этот порог превышает
таймаут worker 125 секунд. Бюджет повторов и исходная operation сохраняются.
Не менять state/attempted_at руками на реальном стенде. Jobs с исчерпанными
попытками требуют разбора поддержки, а не новой выдачи.
Сверить reservation, UUID/subId/expiry/bytes/N+1 и один Grant. Остановить `reconcile`
перед возвращением обычных backend/gateway/bot и проверить owner access новым
login; восстановленная старая cookie должна401, незавершённые credential proofs
— INVALID_VERIFICATION.
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
и готовность к внешнему запуску. Локальная приёмка закрыта решением владельца
2026-10-02. Green fixtures/build не закрывают реальный VPN/restore;
настоящая3X-UI3.7.0 и PG dump/restore проверены на своём Docker-стенде.
Собственный Docker VLESS/TLS-клиент закрывает проверку рабочего подключения;
переключение живого Happ не является обязательным критерием.
Перед внешним запуском обязательны доставка через выделенный SMTP в настоящий
test mailbox и Argon2 benchmark на целевом сервере. Владелец предоставляет эти
ресурсы, а также DNS/сертификаты и опубликованные policies/support из таблицы выше;
после проверок зафиксировать точную ревизию и результаты. Эти пункты пока OPEN.
Push/PR/merge/deploy не выполнены и требуют соответствующего мандата.
