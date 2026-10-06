# М06d — Удаление общего platform/store

Дата: 2026-10-06. Владелец [#60](https://github.com/ekho/3xui-shop/issues/60), контракт `2026-10-06-m06d-composition-v1`.
Спецификация self-reviewed и принята в рамках разрешённого автономного ведения документов; исполнение Native.
Основание: [модульный монолит](2026-10-05-modular-monolith-design.md), правило7 и завершённые М01–М06c.
Fresh base origin/v2 `d796e3a076785ce0f5b933fcd2a22c0f4baf4e26`; [М06c PR70/dev29 полностью доставлен](https://github.com/ekho/3xui-shop/issues/60#issuecomment-6011429496).

## Результат и выбранный подход

Убрать общий platform.Service, его store и все runtime/test consumers. App собирает конкретных владельцев; HTTP и Telegram вызывают их публичные методы. Перенос не меняет клиентские сценарии, данные и внешние контракты. После собственной общей приёмки и доставки этого этапа М06/#60 можно закрыть.

Выбран прямой composition root и транспортные преобразования DTO. Переименование Service в app оставило бы фасад всех доменов; общий dispatch/event bus добавил бы связь и инфраструктуру без нужного нового поведения. Эти варианты не используются.

Текущие факты: HTTP/API/CLI/app зависят от platform.Service/Config; module SQL уже принадлежит владельцам. В platform154 regression tests и повторная тестовая сборка. Root store нужен только старому account adapter, неиспользуемому replay helper и двум persisted-row test fixtures. Root queries осталось4 idempotency/lock запроса без активных вызывающих сценариев.

## 1. Сборка и конфигурация

`app.Modules` содержит только публичные конкретные ссылки Accounts, Catalogue, Subscriptions, VPN, Payments, Support, Notifications, MailDelivery, AuditReports. У него нет доменных методов, SQL, wire, общей Error или store. `app.NewService` удаляется; новая сигнатура:

```go
func NewModules(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg *Config) *Modules
```

Config/LoadConfig/SecretFile/Validate переходят в app. Config содержит DatabaseURL, RedisURL и группы HTTP(HTTPConfig), Accounts(accounts.Config), Subscriptions(subscriptions.Config), VPN(vpn.Settings), Payments(payments.Config), Mail(notifications.MailConfig). HTTPConfig содержит CabinetOrigin, AdapterToken, TrustedProxyCIDRs. Модули не импортируют app и не получают весь Config.

Все существующие env names/defaults/validation и secret-file conflict rules сохраняются. Defaults: TRIAL_PERIOD=3, TRIAL_TRAFFIC_GB=15, BONUS_DEVICES_COUNT=1, ACCESS_RESET_TIMEZONE=UTC, LEGACY_BOT_API_ENABLED=true; TrialEnabled/YooMoneyEnabled/PanelDuplicateGuardVerified=false. Имена настроек и обязательность secret files не переименовываются. RateNamespace остаётся `platform`: это существующий Redis namespace, его смена меняла бы rate-limit контракт. Новых настроек нет. Disabled Telegram не требует token; native Telegram по-прежнему несовместим с enabled legacy bearer API. Нет произвольной загрузки новых plugins.

Общие deployment values связывает app: Accounts.Operators — источник списка для notifications и subscriptions; Subscriptions.PanelID — для VPN/payments; HTTP.CabinetOrigin — для Mail/payments. Provider callbacks читают актуальные значения этих групп, как прежние callbacks. Accounts сохраняет прежний constructor snapshot operator allowlist; общий clock берётся из существующего Accounts.Now, nil означает текущие default time.Now. Production cfg не меняется после загрузки; tests используют прежние контролируемые clock/config transitions. SMTP/mail proof/recipient/account guards и порядок блокировок М06b2 сохраняются.

## 2. HTTP, Telegram, CLI и River

```go
func httpapi.New(modules *app.Modules, pool *pgxpool.Pool, cfg app.HTTPConfig) *echo.Echo
func app.NewTrialBridge(trials *subscriptions.Service, delivery *notifications.Service) *app.TrialBridge
func app.NewTelegram(cfg telegram.Config, trials *subscriptions.Service, delivery *notifications.Service, client *http.Client) (*telegram.Runtime, error)
```

HTTP API хранит concrete owners и pool только для прежнего health Ping. Существующие wire/domain преобразования и safe error mapping находятся в httpapi рядом с соответствующими handlers; они принимают/возвращают transport DTO и не содержат SQL/денежных/панельных правил. Обработчики больше не используют a.svc. Echo context, wire DTO и Telegram Update не передаются доменным модулям.

Сохраняются auth/Origin/CSRF/body/schema/resource/role проверки до защищённых операций, cookie rotation/logout, Retry-After, conflict details allowlist, статусы и JSON null/empty arrays, строки больших IDs/сумм. Accounts.Authenticate и subscriptions.CanRequestTrial по-прежнему совместно формируют capabilities. В operator card/history остаются accounts role/target checks, независимые legacy source_id cursor, trial/audit50+more, support/profile fallback и прежняя server presentation.

Минимальный публичный read в subscriptions — `func (s *Service) TrialServer() (panelID string, enabled bool)`: возвращает существующие config.PanelID/TrialEnabled, без credentials/нового I/O/правил. HTTP показывает Server только при непустом panelID после operator/target authorization. Другие публичные domain contracts сохраняются.

TrialBridge держит только subscriptions/notifications; IDs, raw completed union, payload/hash, lease, retries и Telegram.ActionError сохраняются. Cmd/server использует app.Config/Modules и module workers непосредственно; CLI decode types/errors/import/seed/grant/revoke принадлежат accounts/catalogue. CLI output/error codes и лимит32MiB/UTF8/unknown fields/EOF не меняются. Run/Serve/readiness/shutdown и один Go-процесс сохраняются.

## 3. Тесты и окончательное удаление

Все154 platform test functions переносятся один-к-одному в httpapi как TestRegression<старое имя без Test>. Исходные проверки поведения/rollback/replay/гонок не удаляются и не ослабляются. Snapshot inventory сохраняется; mechanical import/type/owner-call адаптация фиксируется. Fixture собирает actual app.NewModules и HTTP API; native-only calls в test fixture обращаются прямо к соответствующим owners. Test fixture не попадает в runtime dependency graph и не копирует business rules.

Два persisted-row fixtures становятся независимыми test-only structs/SELECT fixtures с прежними колонками, без root или peer-private generated store. App composition tests обращаются к public owners; HTTP/connected/native tests используют actual Modules и module workers, а не другую assembly. Неиспользуемые facade helpers не переносятся в runtime.

После миграции удалить internal/platform, internal/store, db/queries и root sqlc block. Удалять только эти известные переходные Go sources: Python/legacy main/support bot и HTTP adapter остаются до С47. Private module SQL/model generation, db/migrations1–15, maintenance/backup/restore, dependencies и один go.mod сохраняются. Runtime import graph не содержит platform/store; app не становится новым all-domain service.

## 4. Проверяемые критерии

| AC | Доказательство |
| --- | --- |
| D01 | Actual server dependency graph исключает platform/store; app Modules только composition/public refs; no app SQL/wire/all-domain methods. |
| D02 | Старые env/secret-file/default/TLS/proxy/trial/timezone/payment constraints и clock/provider configuration cases сохранены; Telegram disabled без token. |
| D03 | Actual HTTP security/session/support/operator/trial/payment cases сохраняют JSON/status/cookies/cursors/nulls/big IDs/error safety. |
| D04 | TrialBridge/native callbacks и River используют тех же owners; outage/stop/restart/replay/restore не теряют операции. |
| D05 | Все154 legacy tests имеют явное соответствие и green results; persisted13-column audit/TX/52-row proof и mail MaxConns1/revocation/account-order cases сохранены. |
| D06 | Folders/root sqlc block удалены; все module import/SQL boundaries green, API/all15 migrations/dependencies и generation stable. |
| D07 | Full22 matrix на одной committed product revision, actual native3.7.0/TLS/purchase/restore и teardown; whole-M06 ownership/composition acceptance. |
| D08 | Один fresh Astra/high final review; один Important/Critical RED→GREEN fix pass и green suite без re-review; exact-source CI/manual v2 merge/parents/source-equal tree/preview+3multiarch images, затем #60/Project Done. |

## 5. Границы и доставка

Сохранённые функциональные приёмки остаются действительными; М06 повторно проверяет реальные границы и композицию. Новые reports/retention/campaigns/bonuses/Telegram support/UI функции остаются своими сценариями. С45/#47 сверяет полный будущий config; С46/#53 переносит остальные данные; С47/#54 удаляет Python/cutover. Ответственность всех open dependents #60 прочитана, несовместимых заявок на этот contract нет.

Production/реальный provider/Telegram/Happ/VPN/macOS trust и внешняя SMTP-доставка исключены. Local/CI/merge/preview acceptance разделяются. Branch feature/m06d-composition от свежей origin/v2, PR в v2, no codex prefix/force-push/auto-merge. Предварительный release и GHCR после ручного merge уже разрешены; CI waiver PR62 неприменим.

Rollback этого source change — revert доставленного merge с неизменными DB/API/IDs; production переключение требует отдельного разрешения. #60 остаётся OPEN/In progress, пока D01–D08 и доставка не доказаны. Native/autonomous documents/manual v2 merge/preview authority: https://github.com/ekho/3xui-shop/issues/55#issuecomment-6004574101.
