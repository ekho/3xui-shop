# М01: встроенный Telegram для апрува С01 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans for the preserved Native method; implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** В локальной среде один Go-процесс обслуживает кабинет, фоновые операции
и Telegram-апрув С01 через внутренние контракты, без отдельного Python-адаптера.

**Architecture:** Telegram получает два узких контракта — действия по триалу и
устойчивую доставку. Точка сборки связывает их с работающей реализацией С01 через
временный Go bridge; правила заявок/выдачи не копируются в Telegram. HTTP и River
продолжают работать при ошибках или отключении Telegram.

**Tech Stack:** Существующие Go/Echo, pgx/sqlc, River, Redis и Go testing/httptest.
Telegram HTTP-клиент использует `net/http` и `encoding/json`; новые зависимости
и новый `go.mod` не требуются. Docker 3X-UI 3.7.0, PostgreSQL, Redis и Mailpit.

**Spec:** [Принятая архитектура](../specs/2026-10-05-modular-monolith-design.md),
[поведение С01](../specs/2026-10-01-s01-web-trial-design.md).

Дата: 2026-10-05. Статус: автономное исполнение Native; подготовка окружения
и публикация общего решения перед первой задачей. Отдельное повторное одобрение
плана не запрашивается: действует ранее выданное разрешение на остальные
документы и реализацию.
Метод Native уже выбран владельцем и сохраняется. План покрывает только М01;
М02–М06 и новые клиентские сценарии получают отдельные планы при готовности.
Проверенный baseline `v2`: `157fb918c360a4c0f4f924dee34a38574a7d032f`.
PR #5 открыт на `afaeacf652964453ddd61883aa6da6a0d285783c` и не включён в baseline.
Перед выполнением fetch обновляет `origin/v2`; исходники/проверки С10 не
переносятся в ветку М01 автоматически. Документальные уточнения С10 сохраняются
отдельно для его ветки; общие документы обновляются поверх своего baseline,
а не копированием всего dirty worktree С10.

## Global Constraints

- Один Go-процесс для HTTP API, фоновых заданий и Telegram; одна версия backend.
- Один `backend/go.mod`; будущие пустые модули не создаются.
- Доменные интерфейсы не принимают Echo context, Telegram Update или HTTP wire-модели.
- Реализация, SQL-запросы и репозитории принадлежат модулю. В М01 доступ к
  существующим platform/store разрешён только явно указанному переходному bridge
  и прежним потребителям; новый Telegram-модуль их не импортирует. Исключение
  удаляется после М02–М06, оно не является финальной архитектурой.
- Таблицы, миграции, `account_id`, `vpn_id`, `sub_id`, `panel_key` и назначение
  сервера сохраняются. Изменение схемы ради перемещения кода запрещено.
- Approve + TrialGrant + Operation + River job — одна PG-транзакция. Отказ не
  блокирует аккаунт/покупку и не отменяет Stars; пересмотр только поддержкой.
- Claim limit=1, lease=60 секунд, send timeout=10 секунд. Реальный actor,
  allowlist, приватный чат и идемпотентность проверяются до защищённого действия.
- Секреты только из файлов; ни bot token, ни URL с token, email, причина,
  lease token или VPN-ссылка не попадают в логи и evidence.
- Локальные тесты используют собственные PG/Redis/3X-UI/SMTP и fake Bot API.
  Живой Happ не переключается. Реальный Telegram — дополнительная проверка
  отдельным тестовым ботом, после готовности локального пути и проверки его владельца.
- Production-переключение и полный клиентский бот/Stars/support-proxy не входят
  в М01. Новый частичный poller не запускается с production-токеном; у токена
  один обработчик. Нынешний Python-бот полностью удаляется по С47 после покрытия.
- Новая ветка создаётся от свежего `origin/v2`, без `codex/`; PR направляется в
  `v2`. PR #5 и его SHA не меняются ради М01; слияние/production отдельно.
- Conventional Commits и Co-Authored; проверенные docs/code stages можно
  публиковать отдельными PR в `v2`. Слияние в `v2` запускает образы/prerelease;
  этот план не разрешает автоматически сливать PR.

## Review Focus

1. **RF1: решение committed, ответ потерян или update повторён.** Тот же
   callback ID возвращает прежний результат, в DB одна выдача; задача 1/5.
2. **RF2: чужой чат/actor или старая кнопка подтверждения.** Нельзя подтвердить
   причину другого оператора или нового диалога; задача 3.
3. **RF3: сообщение отправлено, ack доставки не сохранён.** Возможна повторная
   карточка, но прежний lease не принимается и второй VPN-доступ не создаётся;
   задача 4/5. Exactly-once Telegram-доставка не обещается.
4. **RF4: Telegram timeout/429/401/409 во время работы кабинета.** Канал имеет
   наблюдаемое состояние; HTTP, web-решение и принятые River jobs продолжаются;
   задача 2/4/5.
5. **RF5: shutdown/restart между принятием решения и выдачей.** Запись решения
   переживает рестарт; HTTP drain и отмена polling ограничены временем; новая
   выдача использует прежнюю Operation и идентификаторы; задача 4/5.

---

## Порядок и файлы

Задачи выполняются **1 → 2 → 3 → 4 → 5**. Первый сквозной результат после
задачи 4 — существующая заявка получает решение из fake Telegram; задача 5
закрывает проверку полного пути и поставки. Реальные внешние ресурсы не являются
предпосылкой разработки этого переноса.

| Файлы | Ответственность |
| --- | --- |
| `backend/internal/modules/telegram/contracts.go` | Нейтральные DTO и consumer-owned контракты; задача 1 |
| `backend/internal/app/trial_bridge.go`, `trial_bridge_test.go` | Временное преобразование существующего С01 в локальные контракты; задача 1 |
| `backend/internal/modules/telegram/internal/botapi/client.go`, `client_test.go` | Telegram HTTP, ограниченный ответ и безопасные ошибки; задача 2 |
| `backend/internal/modules/telegram/approval.go`, `approval_test.go` | Карточки, callbacks и диалог причины; задача 3 |
| `backend/internal/modules/telegram/runtime.go`, `runtime_test.go`, `config.go` | Polling/delivery, config/state/cancel; задача 4 |
| `backend/internal/app/telegram.go`, `lifecycle.go`, `lifecycle_test.go` | Связывание и lifecycle одного процесса; задача 4 |
| `backend/cmd/server/main.go`, `main_lifecycle_test.go` | Подключение runtime и сохранение graceful HTTP drain; задача 4 |
| `backend/internal/platform/config.go`, `config_test.go` | Разделение операторской allowlist и временного HTTP adapter secret; задача 4 |
| `backend/internal/app/native_trial_integration_test.go`, `boundaries_test.go` | Полный путь, рестарт и запрет legacy-imports; задача 5 |
| `deploy/acceptance/local.py`, `compose.local.yml`, `compose.acceptance.yml` | Native local-профиль и запуск проверки без Python-адаптера; задача 5 |
| `.github/workflows/platform-checks.yml`, `docs/evidence/m01-acceptance.md` | CI и точные границы локальной приёмки; задача 5 |

HTTP/OpenAPI/web-схемы и правила С10 не меняются в М01. Временные endpoints
старых потребителей остаются закрытыми; их фактическое удаление входит в С47.
Root Python-образ пока нужен оставшимся сценариям и не удаляется из release
matrix раньше покрытия. В native локальном профиле нет отдельных постоянно
работающих bot/reconcile контейнеров; разовые административные команды допустимы.

### Task 1: локальные контракты и переходный bridge

**Files:** Create `contracts.go`, `trial_bridge.go`, `trial_bridge_test.go`
из таблицы. Existing reference: `platform/trial.go`, `telegram.go`,
`provision.go`; они продолжают владеть своими транзакциями до М02–М06.

**Interfaces:** Пакет `telegram` объявляет следующие контракты; bridge
реализует оба. DTO используют `uuid.UUID`, `time.Time`, строки/числа и enums,
не generated `wire`.

```go
type TrialActions interface {
    Decide(context.Context, TrialDecision) (Decision, error)
    Reconsider(context.Context, SupportAction) (Trial, error)
    Reconcile(context.Context, SupportAction) (Operation, error)
}
type Outbox interface {
    Claim(context.Context) (*Delivery, error)
    Complete(context.Context, Delivery, DeliveryOutcome) error
}
func NewTrialBridge(svc *platform.Service) *TrialBridge
```

`TrialDecision`: RequestID UUID, ActorID int64, Action `approve|reject`,
CallbackID string. `SupportAction`: TargetID UUID, ActorID int64, Key UUID,
Reason string. `Trial`: ID UUID, PreviousID *UUID, Status string, OperationID
*UUID. `Decision`: Trial плюс актуальная TrialCard. `Operation`: ID UUID,
Status string. `TrialCard`: RequestID/OperationID/TargetMessageID, Email или
DisplayName+TelegramID, Comment, CreatedAt, Status — те же безопасные поля С01.
`Delivery`: ID UUID, ChatID int64, Kind, Card TrialCard, LeaseToken string,
LeaseExpiresAt time.Time. `DeliveryOutcome`: Kind `sent|delivery_failed`,
ChatID/MessageID int64, Code из существующих TelegramFailureCode.
`ActionError`: Code string и CurrentRequestStatus string; не содержит raw HTTP
body, токенов или статуса транспорта. `nil` Claim означает пустую очередь.

- [ ] **Step 1:** В `TestTrialBridgeDecisionReplay` через `testkit.Open(t)` и
  существующий `platform.NewService` создать подтверждённый аккаунт/заявку.
  Два локальных `Decide` с одним callback ID должны вернуть одну Operation;
  count trial_grants=1. В `TestTrialBridgeUnauthorized` ActorID вне allowlist
  получает `INVALID_CREDENTIALS`, count trial_grants=0. В
  `TestTrialBridgeLeaseMapping` повторяется result, неверный lease отклоняется.
- [ ] **Step 2:** С тестовыми URL-файлами выполнить
  `go test ./internal/app -run 'TestTrialBridge' -count=1`; ожидать RED по
  отсутствующему bridge, затем реализовать указанные DTO и методы.
- [ ] **Step 3:** Bridge преобразует вызовы в существующие DecideTrialRequest,
  ReconsiderTrialRequest, ReconcileTrialOperation, ClaimTelegramJobs и
  CompleteTelegramJob. Он не вычисляет eligibility и не открывает новую
  транзакцию вокруг этих методов. Ошибки переводятся в ActionError.
- [ ] **Step 4:** Повторить focused команду: PASS; сохранить assertions
  существующих `platform` trial/telegram tests. Commit `refactor: add local trial contracts`.

### Task 2: минимальный Telegram HTTP-клиент

**Files:** Create `telegram/internal/botapi/client.go`, `client_test.go`.
**Consumes:** stdlib `*http.Client`, token из файла через задачу 4.
**Produces:** Приватный для Telegram `Client`, `Update`, `Message`,
`InlineKeyboard` и безопасная `APIError`.

```go
func New(token string, client *http.Client) *Client
func (*Client) GetUpdates(ctx context.Context, offset int64) ([]Update, error)
func (*Client) GetWebhookInfo(ctx context.Context) (WebhookInfo, error)
func (*Client) SendMessage(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboard) (Message, error)
func (*Client) EditMessage(ctx context.Context, chatID, messageID int64, text string, keyboard *InlineKeyboard) (Message, error)
func (*Client) AnswerCallback(ctx context.Context, callbackID, text string, alert bool) error
func (*Client) ClearKeyboard(ctx context.Context, chatID, messageID int64) error
```

- [ ] **Step 1:** `TestClientBoundaries` на httptest/подменённом RoundTripper:
  неверный JSON, ответ >1 MiB, неверные message/chat IDs, redirect, 401, 409,
  429 с retry_after и timeout возвращают типизированную ошибку. Проверить,
  что error/log не содержат token/URL/body. Run
  `go test ./internal/modules/telegram/internal/botapi -count=1`: RED.
- [ ] **Step 2:** Реализовать только перечисленные методы. Production origin
  `https://api.telegram.org`; client не следует redirect. Send/edit имеют
  context timeout=10 секунд; long polling timeout=30 секунд, limit=1,
  allowed_updates `message,callback_query`, HTTP timeout=40 секунд. Ответ
  ограничен 1 MiB. Нет автоматического deleteWebhook/drop_pending_updates.
- [ ] **Step 3:** В задаче 4, где появляется polling loop,
  `TestPollingAcknowledgesAfterHandling` проверяет offset:
  следующий запрос подтверждает update только после успешной доменной записи
  или безопасного отказа. Transient ошибка записи оставляет offset прежним.
  Webhook conflict не переключает транспорт автоматически. Здесь проверяется
  HTTP polling contract; подтверждение update проверяется вместе с обработчиком
  в задаче 4. PASS focused;
  commit `feat: add bounded Telegram transport`.

Long polling используется только для начального операторского test-пути М01.
С34/С35 отдельно реализуют платежный приём и его устойчивое подтверждение.
Правило offset и несовместимость активного webhook подтверждены
[Telegram Bot API](https://core.telegram.org/bots/api#getupdates).

### Task 3: карточки и операторские действия

**Files:** Create `approval.go`, `approval_test.go`.
**Consumes:** TrialActions, Outbox из задачи 1 и Client из задачи 2.
**Produces:** `renderCard(card TrialCard) (string, *botapi.InlineKeyboard, error)`
и `(*dispatcher).handle(ctx context.Context, update botapi.Update) error`.
Dispatcher создаётся в `Runtime` задачи 4; события обрабатываются последовательно.

- [ ] **Step 1:** `TestApprovalActorAndPayload` — allowlist + настоящий from.id,
  is_bot=false, private chat.id=actor, корректный UUID и `wt1:a/r/s/c/y/x:<id>`;
  malformed, inline/inaccessible callback и чужой чат не вызывают TrialActions.
  `TestApprovalCardEscapesHTML` проверяет comment/name/email и отсутствие ключа
  VPN; statuses/buttons повторяют render_card старого С01. Run
  `go test ./internal/modules/telegram -run 'TestApproval' -count=1`: RED.
- [ ] **Step 2:** Сохранить callback prefix `wt1` и существующие действия:
  approve/reject, пересмотр rejected, reconcile needs_review, причина,
  подтверждение/отмена. Карточки меняет только Outbox delivery; handler не
  создаёт вторую копию процесса обновления карточки. Причина 1–1000 Unicode
  code points, HTML escaping; APIError не выдаёт сырые сообщения пользователю.
- [ ] **Step 3:** `TestApprovalConfirmationIsolation` — pending подтверждение
  связано с actor+private chat+target+message_id подтверждения. Кнопка старого
  диалога/другого оператора не подтверждает новую причину. Idempotency Key
  сохраняется после потерянного ответа; повтор вызывает ту же команду.
- [ ] **Step 4:** Диалог хранится в памяти, как нынешний FSM. После restart
  старое подтверждение объявляется устаревшим; незаписанное действие не
  исполняется. Добавить `ponytail:` комментарий с пределом: при необходимости
  продолжать незавершённый диалог после restart потребуется устойчивое хранение.
  Записанные решения/задания остаются в PG. PASS focused;
  commit `feat: handle trial approval inside Telegram module`.

### Task 4: delivery и lifecycle одного процесса

**Files:** Create runtime/config/app wiring/lifecycle files из таблицы;
modify `cmd/server/main.go`, existing lifecycle/config tests.
**Consumes:** Контракты задачи 1, Client задачи 2, dispatcher задачи 3.
**Produces:**

```go
type Config struct { Enabled bool; Token string; Operators []int64 }
func LoadConfig(operators []int64) (Config, error)
func New(cfg Config, client *http.Client, actions TrialActions, outbox Outbox) (*Runtime, error)
func (*Runtime) Run(ctx context.Context) error
func (*Runtime) State() State // enabled/degraded/stable code; no sensitive data
// package app:
func NewTelegram(cfg telegram.Config, svc *platform.Service, client *http.Client) (*telegram.Runtime, error)
func Serve(ctx context.Context, server *http.Server, httpResult, schedulerResult <-chan error, tg *telegram.Runtime) error
```

- [ ] **Step 1:** `TestNativeLifecycleTelegramFailure` — runtime error 401/409
  не завершает Serve; HTTP отвечает, web-решение вызывает прежний сервис.
  `TestNativeLifecycleDrain` переносит existing HTTP drain assertions и
  добавляет отмену getUpdates/send при shutdown. Focused app/module tests: RED.
- [ ] **Step 2:** `TELEGRAM_ENABLED` default=false; включённый модуль читает
  `BOT_TOKEN_FILE`, выключенный не читает его. Operator allowlist — существующий
  `BOT_OPERATOR_IDS`. Platform.LoadConfig больше не требует adapter secret
  только из-за списка операторов: обязательность определяется включённым
  legacy HTTP transport. Для перехода `LEGACY_BOT_API_ENABLED` default=true;
  native-профиль задаёт false, internal routes при отсутствии token отказывают.
  Проверить disabled/missing-secret/malformed-allowlist и пустой bearer.
- [ ] **Step 3:** Run имеет polling и delivery loops с общей отменой модуля.
  Claim=1; render/send/edit/Complete сохраняют lease=60 секунд. После timeout
  send результат неизвестен: lease истекает, успешный ack не выдумывается.
  `message is not modified` считается успехом; другой edit failure допускает
  одну отправку новой карточки. 429 учитывает retry_after; 401/409 прекращают
  модуль с degraded status, HTTP/River остаются. Transient backoff 1/2/4/…/30
  секунд, idle delivery poll=5 секунд, ожидания прерываются context.
- [ ] **Step 4:** Запустить Telegram из существующего `server serve` через
  app.NewTelegram/app.Serve. Domain workers и scheduler используют прежний
  путь; их fatal ошибки сохраняют существующее поведение. Shutdown cancel
  прерывает Telegram, HTTP drain/River Stop сохраняют предел 20 секунд.
  `TestUnsupportedPaymentNotAcknowledged`: обнаруженный payment event в
  частичном М01 не подтверждается/не теряется, poller останавливается degraded.
- [ ] **Step 5:** `TestDeliveryUnknownResult` и `TestDeliveryLateLease` закрепляют
  RF3; `TestTelegramDisabledWithoutToken` закрепляет независимый кабинет.
  PASS `go test -race ./internal/app ./internal/modules/telegram/... -count=1`
  и existing main/config tests. Commit `feat: run Telegram with HTTP and workers`.

### Task 5: локальная сквозная приёмка и правила импортов

**Files:** Create app integration/boundaries tests и evidence из таблицы;
modify native acceptance profile/runner и platform-checks workflow.
**Consumes:** Все интерфейсы задач 1–4; `testkit.Open(t)` для изолированной PG,
существующие Docker панели/SMTP и HTTP/browser regression.
**Produces:** Проверенный М01 и evidence точной ревизии; не приёмка М02–М06/С47.

- [ ] **Step 1:** `TestNativeTrialFlow` запускает HTTP/River/Telegram одного
  приложения с fake Bot API и собственными Docker зависимостями. Проверить
  регистрацию/письмо/подтверждение, request, карточку двум операторам,
  approve/reject, конкуренцию кнопок, пересмотр с причиной и reconcile.
  Assertions: одна TrialGrant/Operation/панельная запись, прежние IDs,
  actor/reason/audit, web-профиль/ключ только владельцу; RF1–RF3.
- [ ] **Step 2:** `TestNativeTrialTelegramOutage` принимает решение, блокирует
  fake Bot API, проверяет выдачу и web-решение второй заявки. Повторить с
  `TELEGRAM_ENABLED=false`. `TestNativeTrialRestart` останавливает весь процесс
  после commit, запускает снова с той же DB и проверяет исходную Operation,
  прежние ключи и отсутствие второй выдачи; RF4/RF5. Fake transport внедряется
  через `*http.Client`; дополнительный production URL/HTTP endpoint не создаётся.
- [ ] **Step 3:** `TestModuleBoundaries` проверяет `go list -json` для новых
  modules: запрещены imports platform, store, wire, httpapi, app и чужой
  приватной реализации. Добавить отрицательный fixture импортов в тесте.
  Временные app bridge/httpapi исключения перечислены явно; CI не считает
  общую platform/store структуру уже удалённой.
- [ ] **Step 4:** Native local-профиль содержит один backend процесс; в нём
  не запускаются Python bot/reconcile service. `local.py` вызывает Go native
  integration вместо импорта app/WebTrialAdapter; для реального test Telegram
  токен передаётся файлом отдельному backend профилю. Старый acceptance runner
  обозначается legacy и не считается проверкой М01. Оба профиля не работают
  одновременно с одним токеном/DB. Проверить Compose parser и compiled Go image.
- [ ] **Step 5:** С поднятыми собственными test PG/Redis выполнить из backend:
  `go test -race ./... -count=1`, `go vet ./...`, `make generate` и проверить
  отсутствие generated drift. Сохранить existing browser/Python checks по CI;
  в native flow нет импорта app или доставки отдельным Python-процессом.
  Проверить Docker image и `/healthz` при выключенном Telegram. Не проводить
  новый реальный платёж YooMoney или переключение Happ.
- [ ] **Step 6:** Native whole-branch review свежим reviewer, обязательные fixes
  и relevant rechecks. В `docs/evidence/m01-acceptance.md` отдельно записать
  local/fake Telegram, real test Telegram при наличии, commit/push/PR/CI и
  внешнюю готовность. Commit `test: verify embedded Telegram trial flow`;
  PR в `v2` со ссылкой на принятую архитектуру и техническую задачу М01.

## Общая архитектура и следующие задачи

М01 создаёт действующий Telegram-модуль и переходный bridge. Существующий
platform/store пока сохраняется; целевые границы остальных модулей реализуются
последовательно М02–М06 из [роадмапа](../../roadmaps/2026-10-01-platform-roadmap.md).
Public API, денежные правила и ранее принятые сценарии сохраняются. Для
продолжения функционального Р3 необходимо завершить границы денег/подписки/VPN
М05. Полный Mini App, Stars и support relay остаются С30–С37; бонусы — Р7.

Перед началом выполнения М01 принятую архитектуру/порядок нужно отразить в
GitHub v2: добавить технические переносы М01–М06 к milestone/project, связать
native dependencies по таблице роадмапа и обновить описания С32/С37/С44/С45/С47.
Для остальных 48 сценариев указать модуль-владелец и архитектурную ссылку.
Done старых функциональных задач не превращается в Done новых переносов;
доказательства прежних ревизий сохраняются. С10/PR #5 остаются открытыми.
Это подготовка shared decision до зависимого выполнения, без публикации релиза.

После М01 готовится ограниченный план М02. Внешний запуск по-прежнему требует
ранее открытых SMTP/benchmark/provider checks; local acceptance их не закрывает.
