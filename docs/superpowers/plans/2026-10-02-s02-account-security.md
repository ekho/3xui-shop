# С02: управление входом и сессиями — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Владелец web-аккаунта восстанавливает и меняет пароль, подтверждает смену email и отзывает сессии, сохраняя аккаунт и VPN-доступ.

**Architecture:** Расширить существующий конкретный `s01.Service`, HTTP API и React-кабинет. PostgreSQL атомарно применяет изменение и отзыв сессий; Redis ограничивает попытки, существующий River mail worker доставляет письма. Первым закончить reset через настоящий TLS SMTP и браузер, затем остальные действия.

**Tech Stack:** Закреплённые в репозитории Go/Echo v5, pgx/sqlc, PostgreSQL, Redis, River, React/TypeScript/Vite, Playwright. Новых зависимостей и сервисов нет.

**Spec:** [Принятая спецификация С02](../specs/2026-10-02-s02-account-security-design.md), версия 2026-10-02. Статус плана: принят пользователем; выбрано Native, реализация начата 2026-10-02.

## Global Constraints

- Только новые web-аккаунты С01; legacy-import, Telegram linking, MFA, React-admin и операторское восстановление вне С02.
- Повторно использовать регистрацию, password helpers, шифрование, Redis scripts, River `MailArgs{DeliveryID uuid.UUID}` / kind=`s01_mail`, testkit и Docker-стенд С01. Не переименовывать пакет `s01` и не выделять auth-service.
- Текущий пароль и подтверждение **обоих** email обязательны для смены адреса. Недоступность старой почты не создаёт обход; повторное письмо заменяет всю пару.
- Пароль: 15–128 Unicode-символов, без trim/усечения; existing common-password check; Argon2id19MiB/2/1, максимум два одновременных хеширования.
- Proof: crypto/rand256-bit token, ссылка30min, код8digits/10min/5 неверных попыток; после пяти ошибок код закрыт, ссылка остаётся действительной. Purpose не взаимозаменяемы.
- Почтовый бюджет получателя общий для С01/С02: 1/min и5/h; для двух адресов атомарный. Anonymous IP30/15min; current-password и login делят бюджет ошибок account5/IP30 за15min. Redis outage →503 для зависимых действий, logout/действующие сессии работают.
- Cookie `__Host-session`: Secure/HttpOnly/SameSite=Lax/Path=/, без Domain; idle7d/absolute30d. Ротация не продлевает исходный absolute expiry. Session POST требуют точный `CABINET_ORIGIN` и CSRF; proof POST — Origin и proof.
- Token только во fragment, удаляется bootstrap до приложения; нет browser storage, query, Referer или логов с секретами. Strict JSON/query, body≤16KiB, no-store, APIError/Retry-After по спецификации.
- Account/VPN/grant/trial identities и лимиты неизменны; действия С02 не вызывают3X-UI. Название продукта и HTTPS-origin остаются настройками.
- Собственный Docker3X-UI **3.7.0**, PG/Redis/Mailpit, тестовые браузеры. Переключение установленного Happ запрещено; внешняя почта/целевой benchmark и приёмка С01 остаются открытыми.
- План разрешает подготовку; исполнение начинается после review и выбора метода. При исполнении допустимы локальные scoped commits; push, PR, production, публикация и удалённый CI не входят в этот план.

## Review Focus

1. **RF1:** Почтовый scanner/preview и reset A при открытой cookie B: только явный POST меняет A, cookie B и её владелец сохраняются — Tasks1/2.
2. **RF2:** Login уже проверил старый пароль, когда reset/change завершился: такой login не создаёт сессию со старой версией — Tasks1/3/4.
3. **RF3:** Текущий пароль меняется, пока письмо на pending новый адрес отправляется: все затронутые email блокируются в одном порядке, нет deadlock или действующего старого proof — Tasks1/4.
4. **RF4:** Ответ с новой cookie потерян или restricted-страница перезагружена: обычный вход восстанавливает управление, logout получает только CSRF и остаётся доступным — Task3.
5. **RF5:** Новый адрес заняла регистрация между двумя подтверждениями, либо backup вернул старые сессии: чужой аккаунт не присваивается; restore отзывает сессии/proofs до ingress — Tasks4/6.

---

## Карта файлов и проверок

| Файлы | Ответственность |
| --- | --- |
| `docs/api/openapi.yaml`; `backend/internal/wire/models.gen.go`; `web/src/api/schema.gen.ts` | Авторский additive API и generated DTO, Tasks1/3/4 |
| `backend/db/migrations/00006_account_security.sql`; `db/queries/credentials.sql`, `registration.sql`, `sessions.sql`; `internal/store/*.go` | Версия credentials, proofs, purpose писем, атомарные queries; generated store не редактировать вручную |
| `backend/internal/s01/credentials.go`, `credentials_test.go` | Reset и общие границы credential proofs/отзыва, Task1 |
| `backend/internal/s01/session.go`, `session_test.go`, `registration.go`, `mail.go`, `registration_test.go` | Существующий login/logout, лимиты и worker; расширение без замены С01, Tasks1/3/4 |
| `backend/internal/s01/email_change.go`, `email_change_test.go` | Двухсторонняя смена, отмена и конфликты, Task4 |
| `backend/internal/httpapi/api.go`, `account_security.go`, `account_security_test.go` | Routes, strict decode, Origin/CSRF, cookie и HTTP tests |
| `web/src/Auth.tsx`, `Security.tsx`, `Cabinet.tsx`, `main.tsx`, `api/client.ts`, `i18n.ts`, `style.css`; `web/index.html` | Формы/навигация/состояния, Tasks2/3/5; bootstrap менять только если его текущего поведения недостаточно |
| `web/tests/s02.spec.ts`, `s01.spec.ts`, `playwright.config.ts`; `backend/tests/s02_integration_test.go` | Browser/API/River/TLS интеграция на существующих fixtures |
| `deploy/s01/browser.mjs`, `local.py`; `deploy/s02/browser.mjs` | Повторно используемые Docker checks и проверка реальных Mailpit-писем; без отдельного Compose |
| `backend/db/maintenance/post_restore_auth.sql`, `internal/s01/restore_test.go`; `docs/runbooks/s02-account-security.md`, `docs/evidence/s02-acceptance.md` | Ограниченная restore-процедура и evidence, Tasks6/7 |
| `.github/workflows/s01-checks.yml`; `docs/roadmaps/2026-10-01-platform-roadmap.md` | Расширить имеющиеся checks и фактический статус; удалённый запуск не разрешён |

Все пути ниже относительно корня **рабочего worktree**, Go-команды — из `backend`. До исполнения прочитать оба документа и live dirty state. Использовать `fixture`, `verified`, `secrets`, `status`, `testkit.Open`/`MailServer`, существующие HTTP `open`/`send`; новую инфраструктуру fixtures не строить.

Установить `S01_TEST_DATABASE_URL_FILE`/`S01_TEST_REDIS_URL_FILE` на private test-only файлы по [runbook С01](../../runbooks/s01-test-rollout.md). Их значения не печатать. Red — провал ожидаемого поведения с компилируемым минимальным stub; отсутствие ресурсов/компиляции не доказывает red. Каждый green завершается `git diff --check`, selective staging только файлов задачи и локальным commit; чужие изменения сохраняются. Проверки запускать с timeout; не повторять полный набор после green без изменения кода или новой причины.

### Task 1: Reset через API, атомарное изменение и совместимая почта

**Files:** Create `00006_account_security.sql`, `credentials.sql`, `credentials.go`, `credentials_test.go`, `httpapi/account_security.go`, `account_security_test.go`; Modify авторский OpenAPI, `registration.sql`, `sessions.sql`, `session.go`, `registration.go`, `mail.go`, `api.go`, `registration_test.go`; Generate wire/store/TS files.

**Interfaces:**
- Consumes: `Service.Login(ctx context.Context, in wire.LoginInput, ip string) (wire.LoginResult, string, error)`, `Authenticate(ctx context.Context, raw string) (wire.AccountResult, error)`, `SendMail(ctx context.Context, id uuid.UUID) error`; existing password/opaque/digest/codeDigest/mail helpers.
- Produces: `RequestPasswordReset(ctx context.Context, in wire.PasswordResetInput, ip string) (wire.PasswordResetAccepted, error)`; `CompletePasswordReset(ctx context.Context, in wire.PasswordResetCompleteInput, ip string) error`. Schemas: `PasswordResetInput{email,locale}`, `PasswordResetAccepted{challenge_id,resend_after}`, `PasswordResetCompleteInput{token? OR challenge_id?+code?,new_password}`. Routes/operationId точно по §5 спецификации.
- Produces: `credentialSecrets(t *testing.T, s *Service, e *testkit.Env, id uuid.UUID) (deliveryID uuid.UUID, token string, code string)` в `credentials_test.go`: decrypt существующим AES-GCM через новый FK, только тесты. `credential_version` и purpose schema доступны Tasks3/4; `MailArgs` не меняется.
- Produces shared methods в `credentials.go`: `lockCredentialAccount(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) (store.Account, error)` (SELECT FOR UPDATE), `lockCredentialEmails(ctx context.Context, tx pgx.Tx, emails []string) error` (distinct sorted advisory locks), `revokeCredentialProofs(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) error` (после locks отменяет proofs и их mail ciphertext). Это конкретные helpers трёх credential flows, без repository interfaces.

- [x] **1. Закрепить additive contract и миграцию.** Не менять существующие operationId/DTO; сначала добавить только две reset-операции и их schemas. Миграция сразу содержит всю принятую структуру proofs (reset/old/new), version0, mail kind default registration + новый FK/взаимоисключение FK. `account_id=NULL` разрешён только dummy reset; token hash32bytes, purpose check, отдельная уникальность live reset и live old/new proof на account. Для пары `original_email` — старый адрес, `target_email` — общий новый; получатель old-письма определяется purpose. Истёкшие строки явно revoke перед replacement; старые migrations/jobs/ключи сохраняются. Выполнить `make -C backend generate` и `npm --prefix web run api:generate`.
- [x] **2. Написать failing cases `TestPasswordReset`, `TestCredentialProofBoundary`, `TestCredentialMailIsolation`, `TestCredentialConcurrency`, `TestAccountSecurityMigration`, `TestAccountSecurityHTTP`.** Проверить известный/неизвестный202 и dummy job без SMTP/account; reset link/code и все TTL/5 ошибок; новый запрос не отзывает sessions, старый proof заменяется; restricted reset сохраняет restriction; registration proof не работает как reset. Пример ядра после получения token:
  ```go
  err := s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &token, NewPassword: "a different safe password ✨"}, "127.0.0.1")
  if err != nil { t.Fatal(err) }
  if _, err = s.Authenticate(ctx, oldRaw); status(err) != 401 { t.Fatal("old session survived reset") }
  if err = s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &token, NewPassword: "a different safe password ✨"}, "127.0.0.1"); status(err) != 400 { t.Fatal("used proof accepted") }
  ```
  RF1: HTTP reset A с cookie B не возвращает Set-Cookie, B работает; scanner GET не пишет. RF2: отдельный test Service на той же БД блокируется своим `now` callback после old-password hash и перед финальной transaction; второй Service выполняет настоящий reset, затем первый продолжает — old login401/no session. Не добавлять production test hooks или sleep для угадывания порядка. RF3: sender/cancellation с pending target проверяется Task4 после появления настоящего email-change flow (ruling в ledger). Проверить body/query/Origin/no-store и token XOR code.
  Migration case: только в новой пустой `testkit.Open` БД выполнить existing Goose `DownTo(5)` **до** наполнения; rawSQL создать С01 account/session/registration mail/River job, затем `db.Migrate` дважды. Все прежние IDs/hash/expiry/ciphertext/jobkind сохранены, version0/kindregistration установлены, worker отправляет старое письмо. Ни active Docker DB, ни production не откатывать этой проверкой.
- [x] **3. Выполнить red.** `go test ./internal/s01 ./internal/httpapi -run 'TestPasswordReset|TestCredentialProofBoundary|TestCredentialMailIsolation|TestCredentialConcurrency|TestAccountSecurityMigration|TestAccountSecurityHTTP' -count=1` → FAIL по этим assertions.
- [x] **4. Реализовать reset и финальную границу Login.** Request проходит одинаковые normalize/limits/DB/InsertTx этапы, включая dummy proof/job; нет быстрого unknown-return. Complete: preliminary lookup без lock → hash до транзакции → account lock → сортированные email advisory locks → повторная проверка proof/version/TTL → password/version update + revoke всех sessions/proofs + audit/security notice в одной transaction. Login снимает `now` для создания session после hash до Begin, затем блокирует account, сверяет email/hash/version/restriction и только потом вставляет session; не создаёт session после stale проверенного пароля. Anonymous IP limit общий для request/complete; ошибки не раскрывают существование аккаунта.
- [x] **5. Расширить существующий mail путь.** Заменить broad `RevokeMail` на `RevokeRegistrationMail`, обновить **все** Register/Resend/Verify callers. Credential cancellation фильтрует точные отменённые proof IDs; security_notice не отменяется. Existing `enqueueMail` остаётся wrapper регистрации, добавить конкретный `enqueueCredentialMail(ctx context.Context, tx pgx.Tx, email string, proofID uuid.UUID, payload mailPayload) error` и `enqueueSecurityNotice(ctx context.Context, tx pgx.Tx, email string, payload mailPayload) error`. Worker берёт email→proof→delivery, читает version без account lock, отправляет актуальный proof или no-op dummy/revoked/expired; ciphertext очищает. Старые registration jobs продолжают работать. Локализованные reset/notice письма без секретов в audit/job args; security notices обходят request mail budget.
- [x] **6. Выполнить green и регрессию затронутой почты.** Команда шага3 PASS; `go test ./internal/s01 ./internal/httpapi -run 'TestRegistration|TestMailRevocation|TestPasswordUnicode|TestSessionBoundary|TestLoginRateLimit|TestMail' -count=1` PASS. Добавить очередь/Redis/SMTP failure cases: одинаковый503known/unknown, retry не повторяет credential mutation, отменённые ciphertext пусты. Commit: `feat: add account password reset`.

**Gate:** HTTP reset действительно меняет пароль нужного аккаунта, отзывает все его сессии и сохраняет чужую cookie; mail/job граница С01 остаётся совместимой. AC1–3/8/11–13/16.

### Task 2: Первый сквозной reset в кабинете и Mailpit

**Files:** Modify `Auth.tsx`, `main.tsx`, `api/client.ts`, `i18n.ts`, `style.css`, `playwright.config.ts`, `s01.spec.ts`, `deploy/s01/browser.mjs`; Create `web/tests/s02.spec.ts`, `deploy/s02/browser.mjs`, `backend/tests/s02_integration_test.go`.

**Interfaces:**
- Consumes: Task1 generated reset schemas; `request<T>`/`ApiError`, существующий bootstrap `window.__emailToken`; real integration `open(t *testing.T) *fixture` / `send` из `s01_integration_test.go`, pinned TLS browser/свой Mailpit из С01.
- Produces: `requestPasswordReset(input: PasswordResetInput): Promise<PasswordResetAccepted>`, `completePasswordReset(input: PasswordResetCompleteInput): Promise<void>` в client; `Auth` поддерживает пути `/forgot-password` и `/reset-password`. `TestS02ResetFlow` исполняет actual API+River+TLS SMTP; `node deploy/s02/browser.mjs reset` проверяет тот же путь через Docker Mailpit.

- [x] **1. Добавить failing browser cases в `s02.spec.ts`.** RU/EN request показывает условное обещание инструкции; password/repeat mismatch не отправляет POST; link и manual challenge_id+code; GET/preview не подтверждает; двойной click даёт один POST. `expect(new URL(page.url()).hash).toBe('')`; `expect(await page.evaluate(() => JSON.stringify([localStorage, sessionStorage]))).not.toContain(token)`. RF1: context B открывает reset A, после выполнения `/me` по-прежнему B; нет auto-login/reset-cookie. Отдельно rate limit/cooldown и503/lost-response не вызывают автоматического retry подтверждения.
- [x] **2. Выполнить red.** Расширить existing Playwright `testMatch` для `s01.spec.ts` и `s02.spec.ts`, сохранив real-mode. `npm --prefix web run test:e2e -- s02.spec.ts -g reset` → FAIL по UI assertions.
- [x] **3. Реализовать формы и навигацию.** Расширить существующий Auth без отдельного auth framework; links в login, token читать только на подходящих proof-страницах. Bootstrap уже удаляет fragment; сохранить его и текущий CSP hash, в `main.tsx` расширить перечень разрешённых proof pages. После успешного reset обычный login; пароль/repeat не сохранять. Labels, focus/error aria-live, autocomplete/new-password/one-time-code, paste/password-manager, busy/cooldown и мобильный viewport375px.202 означает принятие, а не доставку: обновить соответствующую существующую RU/EN registration copy и её проверки в `s01.spec.ts`/`deploy/s01/browser.mjs`.
- [x] **4. Проверить настоящий vertical slice.** В `TestS02ResetFlow` использовать existing TLS SMTP и River: два старых session jars → письмо → explicit complete → обе401 → old password401/new200. В `deploy/s02/browser.mjs reset` читать reset письмо именно по recipient+purpose из настоящего Mailpit; не брать secret из БД. Повторно использовать pin собственного CA/own test stack, не менять trust Mac. `go test ./tests -run '^TestS02ResetFlow$' -count=1 -v` и `node deploy/s02/browser.mjs reset` → PASS. До успешного среза Task3 не начинать.
- [x] **5. Выполнить green.** Команда шага2, `npm --prefix web run typecheck`, `npm --prefix web run build` → PASS. Commit: `feat: expose password reset in cabinet`.

**Gate:** Пользователь проходит reset от браузера через доставленное TLS Mailpit-письмо до нового login. AC1–3/11/12/16; локальная почта не закрывает external deliverability.

### Task 3: Известный пароль, другие сессии и безопасный выход

**Files:** Modify OpenAPI/generated files, `session.go`, `session_test.go`, `credentials.go`, `credentials_test.go`, `sessions.sql`, `httpapi/account_security.go`, `api.go`, `account_security_test.go`, `Cabinet.tsx`, `main.tsx`, `client.ts`, `i18n.ts`, `style.css`, `s02.spec.ts`; Create `web/src/Security.tsx`.

**Interfaces:**
- Consumes: Task1 version/mail cancellation/account lock, существующий Login/Authenticate/Logout и Task2 client/navigation.
- Produces: `SessionRotation{Raw string; AbsoluteExpiresAt time.Time}`; `ChangePassword(ctx context.Context, raw string, in wire.PasswordChangeInput, ip string) (SessionRotation, error)`; `RevokeOtherSessions(ctx context.Context, raw string, in wire.CurrentPasswordInput, ip string) (SessionRotation, error)`; `GetSessionContext(ctx context.Context, raw string) (wire.SessionContext, error)`; `GetAccountSecurity(ctx context.Context, raw string) (wire.AccountSecurity, error)`.
- Produces shared method `authenticateCurrentPassword(ctx context.Context, raw string, password string, ip string) (store.Account, store.Session, error)` в `credentials.go`: snapshot до transaction + общий limiter/hash; mutation всегда повторно проверяет session/version/restriction под lock.
- Produces DTO: `PasswordChangeInput{current_password,new_password}`, `CurrentPasswordInput{current_password}`, `SessionContext{csrf_token}`, `AccountSecurity{email,has_other_sessions,pending_email_change}` / `PendingEmailChange{new_email,expires_at,current_email_confirmed,new_email_confirmed}`. До Task4 pending=null. Client `changePassword(input: PasswordChangeInput): Promise<void>`, `revokeOtherSessions(input: CurrentPasswordInput): Promise<void>`, `getSessionContext(signal?: AbortSignal): Promise<SessionContext>` (обновляет private CSRF), `getAccountSecurity(signal?: AbortSignal): Promise<AccountSecurity>`.

- [x] **1. Добавить failing `TestPasswordChange`, `TestRevokeOtherSessions`, `TestRestrictedLogout` и HTTP/browser cases.** Wrong current400/CURRENT_PASSWORD_INVALID, no session401, same password400, common/length rejection. Скопированный current ID и other ID после rotation401; новый работает, CSRF новый, expiry строго прежний. Например:
  ```go
  rotation, err := s.ChangePassword(ctx, raw, wire.PasswordChangeInput{CurrentPassword: oldPassword, NewPassword: newPassword}, "127.0.0.1")
  if err != nil || rotation.Raw == raw || !rotation.AbsoluteExpiresAt.Equal(beforeExpiry) { t.Fatal("rotation/absolute TTL") }
  if _, err = s.Authenticate(ctx, raw); status(err) != 401 { t.Fatal("copied old current session survived") }
  ```
  Reset proofs отозваны при password change; revoke-others version/password не меняет. Проверить общий failure budget login+reauth5/account30/IP, Redis failure, чужие cookie. RF2: финальный stale-login barrier; RF4: потерян Set-Cookie → old current401, new-password login200; restricted reload GETauth/session толькоCSRF, `/me/security`403, logout с правильнымCSRF204, foreignOrigin/missingCSRF403.
- [x] **2. Выполнить red.** `go test ./internal/s01 ./internal/httpapi -run 'TestPasswordChange|TestRevokeOtherSessions|TestRestrictedLogout' -count=1`; `npm --prefix web run test:e2e -- s02.spec.ts -g 'password change|other sessions|restricted logout'` → FAIL по поведению.
- [x] **3. Реализовать Service/HTTP и generated контракт.** Hash/check до transaction, затем account/session revalidation; change увеличивает version и отзывает все старые proofs, revoke-others сохраняет их. Delete old current+others и AddSession с новым ID/CSRF и прежним absolute expiry атомарны. Cookie Expires/Max-Age только оставшийся срок. Извлечь общий current-password limiter из existing login Lua, known-account key привязать к стабильному account_id; unknown login оставляет непрозрачный email key. Это не даёт смене адреса обнулить budget. Successful check убирает только свою reservation. Logout audit без restrictions; `/auth/session` выдаёт только CSRF живой cookie, не расширяет права. Добавить четыре операции Task3 из §5; regenerate source.
- [x] **4. Реализовать `/cabinet/security` и logout fallback.** Security form current/new/repeat; отдельная кнопка other sessions/current password; после204 получить `/me` и новыйCSRF, до этого не отправлять следующее изменение. При network failure ротации очистить client-состояние и предложить обычный login, не retry старымпаролем. Restricted reload берёт `/auth/session`, активирует logout независимо от `/me`403; при logout/401 уничтожить CSRF и отображаемый VPN-key. Pending UI подключит Task5.
- [x] **5. Выполнить green и local commit.** Команды шага2 PASS; `go test ./internal/s01 ./internal/httpapi -run 'TestSessionBoundary|TestLoginRateLimit|TestPasswordReset|TestCredentialConcurrency' -count=1` и frontend typecheck PASS. Commit: `feat: manage passwords and cabinet sessions`.

**Gate:** Изменение известного пароля и отзыв других сессий работают с ротацией; ограниченный пользователь выходит после reload. AC4/8–10/12/16.

### Task 4: Два email proofs, отмена и гонки владения

**Files:** Create `email_change.go`, `email_change_test.go`; Modify `credentials.sql`, `credentials.go`, `mail.go`, `credentials_test.go`, OpenAPI/generated files, `account_security.go`, `account_security_test.go`.

**Interfaces:**
- Consumes: Tasks1/3 `lockCredentialAccount`, `lockCredentialEmails`, `revokeCredentialProofs`, `enqueueCredentialMail`, `enqueueSecurityNotice`, `authenticateCurrentPassword` с сигнатурами выше; `credentialSecrets` и `AccountSecurity`.
- Produces: `RequestEmailChange(ctx context.Context, raw string, in wire.EmailChangeInput, ip string) (wire.EmailChangeAccepted, error)`; `ConfirmEmailChange(ctx context.Context, in wire.EmailChangeConfirmInput, ip string) (wire.EmailChangeResult, error)`; `CancelEmailChange(ctx context.Context, raw string) error`. DTO: `EmailChangeInput{new_email,current_password}`, `EmailChangeAccepted{change_id,expires_at,resend_after}`, `EmailChangeConfirmInput{token? OR challenge_id?+code?}`, `EmailChangeResult{completed}`. Операции request/confirm/cancel точно по §5; GETsecurity возвращает реальную pending-пару.

- [x] **1. Добавить failing `TestEmailChange`, `TestEmailChangeOwnershipRace`, `TestEmailChangeConcurrency`, `TestEmailChangeMailBudget`.** Два порядка token/code confirmation в разных jars; first completed=false/old login200, second=true/new200/old401/all sessions401/version+1. Cancel204 повторно; expires30min; code10min/5ошибок; resend после cooldown отменяет оба старых proof и первое подтверждение; one-mailbox не завершает. Same/busy address409 без чужих IDs. Проверить normalize/case/IDNA и сохранение dot/+suffix.
  ```go
  first, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &oldToken}, "127.0.0.1")
  if err != nil || first.Completed { t.Fatal("one mailbox changed account") }
  second, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &newToken}, "127.0.0.2")
  if err != nil || !second.Completed { t.Fatal("two mailbox confirmation") }
  ```
  RF3: sender vs password/reset/email cancellation с pending target, bounded wait/no deadlock; reset/version change отменяет оба email proofs, revoke-others сохраняет pending. RF5: С01 verification занимает target между first/second; final409, old email/password сохранены, pair отменена, чужой account не изменён. Общий1/min5/h для пары: limited target не списывает бюджет старого и не создаёт half-pair/job.
- [x] **2. Выполнить red.** `go test ./internal/s01 ./internal/httpapi -run 'TestEmailChange|TestCredentialMailIsolation|TestCredentialConcurrency' -count=1 -race` → FAIL assertions.
- [x] **3. Реализовать пару и лимит писем.** Расширить existing mail Redis Lua для1 или2 sorted recipients с предварительной проверкой всех и атомарным списанием только при разрешённой паре; С01 использует тот же бюджет. Request сначала current-password check, потом account lock + все affected old/new/pending emails sorted + proof/delivery locks; replace previous pair и два InsertTx mail jobs в той же transaction. Уникальность пары/общие account/change/original/target/version/expiry проверять до её записи, не доверять клиентским ID. Два независимых token/code, общий expires_at; никакого email reservation service.
- [x] **4. Реализовать confirmation/cancel и запрет старого входа.** Preliminary proof lookup без lock → account → sortedaffected email → proof → delivery. Первый confirmation только отмечает proof; второй проверяет оба и UNIQUEemail, применяет email/verified/version + all sessions/proofs revoke + audit + notices обоим адресам. Unique conflict перевести в409 и **commit отмены пары**, не оставлять abort transaction или half-change. Cancel требует только разрешённую сессию/CSRF, первый успешный cancel пишет audit, повтор204 без повторного события; pending истёкших/отменённых не возвращать. Security notices никогда не подавлять новым request. Дополнить три операции и generation.
- [x] **5. Выполнить green и mail regression.** Команда шага2 PASS; `go test ./internal/s01 ./internal/httpapi -run 'TestRegistration|TestMailRevocation|TestPasswordReset|TestPasswordChange|TestRevokeOtherSessions' -count=1` PASS. Освобождённый old email регистрирует новый account без старого grant/VPN. Commit: `feat: confirm account email changes with both mailboxes`.

**Gate:** Один mailbox не меняет email; пара применяется единожды, конкуренция с регистрацией не присваивает аккаунт и не блокирует worker навсегда. AC5–9/12–14/16.

### Task 5: Email change UI и полный браузерный путь

**Files:** Modify `Security.tsx`, `Auth.tsx`, `main.tsx`, `client.ts`, `i18n.ts`, `style.css`, `s02.spec.ts`, `deploy/s02/browser.mjs`, `backend/tests/s02_integration_test.go`.

**Interfaces:**
- Consumes: Tasks3/4 generated DTO/GETsecurity/pair flows и Task2 proof pages/real fixtures.
- Produces client: `requestEmailChange(input: EmailChangeInput): Promise<EmailChangeAccepted>`, `confirmEmailChange(input: EmailChangeConfirmInput): Promise<EmailChangeResult>`, `cancelEmailChange(): Promise<void>`; `Auth` route `/confirm-email-change`; `TestS02AccountSecurityFlow`; `node deploy/s02/browser.mjs all`.

- [x] **1. Добавить failing browser cases `email change both orders`, `email change cancel and replace`, `email change errors`.** Pending показывает target/общий срок и обе отметки; first confirmation оставляет old login, second предлагает new login и не заменяет unrelated cookie. GET/scanner/двойной click; form без current password; expired/used/wrong-purpose ошибки; busy409,429cooldown/503, refresh по двум устройствам. Отмена убирает pending; повторное письмо предупреждает о сбросе обоих подтверждений. RU/EN/keyboard/mobile375px/labels/aria-live проходят.
- [x] **2. Выполнить red.** `npm --prefix web run test:e2e -- s02.spec.ts -g 'email change'` → FAIL UI assertions.
- [x] **3. Реализовать pending и подтверждение.** Формы используют generated schemas и existing client request/CSRF; polling только GETsecurity при pending, abort на уходе, без автоподтверждений. Текст после first просит вторую почту, после second обычный login; при потерянном ответе не повторять proof POST. Если старая почта недоступна — объяснение необходимости поддержки, без новой несуществующей функции/канала.
- [x] **4. Проверить actual integration и Docker browser.** `TestS02AccountSecurityFlow` использует существующий S01 grant/operation fixture, actual TLS SMTP/River и два browser/session context; до/после reset/password/email/revoke/logout сравнить account_id, grant/operation, trial-used, panel_key/vpn_id/sub_id, expiry/bytes/devices. Счётчик panel requests во время действий С02 не меняется. В `deploy/s02/browser.mjs all` реальные Mailpit письма на оба адреса, refresh/relogin и та же subscription URL; 3X-UI3.7.0 используется для readback, Happ не трогать. `go test ./tests -run '^TestS02(ResetFlow|AccountSecurityFlow)$' -count=1 -v` и `node deploy/s02/browser.mjs all` → PASS.
- [x] **5. Выполнить green.** Команда шага2, frontend typecheck/build → PASS. Commit: `feat: expose confirmed email changes in cabinet`.

**Gate:** Все клиентские действия С02 доступны в браузере с сохранённым VPN ownership и без Telegram. AC5/6/11/12/14/16.

### Task 6: Restore не возобновляет отозванный browser доступ

**Files:** Create `backend/db/maintenance/post_restore_auth.sql`, `backend/internal/s01/restore_test.go`, `docs/runbooks/s02-account-security.md`; Modify `deploy/s01/local.py`, `docs/runbooks/s01-test-rollout.md`.

**Interfaces:**
- Consumes: Task1 credential schema/mail kinds; existing `db.Migrate`, owned `deploy/s01/local.py restore()` и restore-only reconcile mode.
- Produces: статический idempotent `post_restore_auth.sql`, применяется psql после restore+migrate и **до** reconcile/serve/ingress; `TestAccountSecurityRestore`. Не новый HTTP endpoint/CLI service. SQL возвращает только counts, без секретов/адресов.

- [x] **1. Добавить failing `TestAccountSecurityRestore`.** Fixture содержит sessions, pending reset/pair(first confirmed), mail jobs и security notices; применить maintenance SQL дважды. Assertions: sessions0, old proofs INVALID_VERIFICATION, proof ciphertext NULL; accounts/password/version/identity/grants/operations unchanged, notices/job IDs retained. RF5: restored revoked-before-backup cookie не работает после запуска; login credentials на моментbackup создаёт новую session, не унаследованную.
- [x] **2. Выполнить red.** `go test ./internal/s01 -run '^TestAccountSecurityRestore$' -count=1` → FAIL по отзывам.
- [x] **3. Написать maintenance SQL и runbook.** Одна transaction: DELETEsessions; revoke все незавершённые credential proofs, в том числе первое email-confirmation; очистить ciphertext их credential-писем. Registration/security_notice и account/VPN facts не менять. Выполнять только при закрытом ingress и остановленных writers/mail workers; если SQL не выполнен/ошибка — не открывать ingress. Внешний оператор использует secret files/psql service без URL в аргументах. Backup не содержит credential changes после своего времени; новый пароль не обещать восстановленным.
- [x] **4. Обновить реальный restore check.** В `local.py` добавить `restore` в existing command dispatch. `signup()` возвращает `(opener, trial, {email,password})` только в памяти; обновить оба существующих callers `check`/`restore`, ничего не печатать. После migrate исполнить тот жеSQL в owned restored DB, перед reconcile; прежний owner jar должен401, затем явный login и прежнийVPN/readback. Добавить pending reset/pair до dump и отказ их proofs послеrestore. Публиковать только redacted PASS/counts. Историческую приёмку С01 не переписывать как провал: runbook отмечает новое правило С02 и ссылку. `go test ./internal/s01 -run '^TestAccountSecurityRestore$' -count=1` и `python3 deploy/s01/local.py restore` → PASS; тест только собственный Docker stack, без Telegram/Happ.
- [x] **5. Локальный commit.** `fix: revoke restored sessions and credential proofs before ingress`.

**Gate:** Restore возвращает account/VPN, но требует нового login и новых proofs; maintenance повторяем и fail-closed. AC15/14.

### Task 7: Интеграция, review и фиксирование приёмки

**Files:** Modify `.github/workflows/s01-checks.yml`, `web/playwright.config.ts` при необходимости, С02 план и roadmap; Create `docs/evidence/s02-acceptance.md`; Modify С02 runbook.

**Interfaces:**
- Consumes: Tasks1–6, существующие генераторы/тесты/Compose, запрет Happ и открытые С01 prerequisites.
- Produces: работающий на exact local commit набор S01+S02, coverage ниже, redacted evidence с раздельными implementation/delivery/acceptance; report свежего whole-branch reviewer по выбранному исполнению. Источник evidence — команды/HTTP/browser/readback, не наличие документов.

- [ ] **1. Проверить generation и статические границы.** `make -C backend generate`, `npm --prefix web run api:generate`; `git diff --exit-code -- backend/internal/wire backend/internal/store web/src/api/schema.gen.ts` после committed generation → no diff. `make -C backend vet`, `npm --prefix web run typecheck`, `npm --prefix web run build`, `git diff --check` → PASS.
- [ ] **2. Выполнить один полный релевантный regression.** `make -C backend test-integration` (existing isolated PG/Redis, race); `npm --prefix web run test:e2e` (S01+S02 mocked-boundary browser); `S01_E2E_MODE=real npm --prefix web run test:e2e` (существующий real S01); `node deploy/s02/browser.mjs all` (actual Docker S02). PASS всех обязательных checks, без skip по окружению. Увеличить existing CI checks ровно на новые tests/maintenance paths, без нового workflow/публикации; удалённый CI не запускать. Restore Task6 не повторять без новых изменений в его границе.
- [ ] **3. Выполнить выбранный review.** При Native — один свежий final reviewer всей ветки; при Subagent-driven — также результаты task reviews. Проверить credential/login locks, reauth/shared budgets, scoped mail cancellation, unique conflict commit, expiry cookie, fallbacklogout и реальный provider/consumer API. Findings исправить и повторить только затронутые checks; blockers не превращать в PASS.
- [ ] **4. Записать evidence и rollout.** Отметить точный tested revision, команды,16AC, реальные mail/browser/restore результаты и ограничения. Для test rollout: обслуживание/ingress closed → backup+сохранённые MAIL_KEY/CODE_KEY → additive migrate/repeat check → backend/web compatible build → smoke → ingress. При несовместимом откате оставить ingress закрытым, восстановить согласованные DB/build и применить restoreSQL перед открытием; не делать destructive Down. Legacy bot handlers не переключаются вС02, новый Telegram worker не запускать. External SMTP/mailbox и target Argon2 benchmark имеют owner=user и readiness point=до external acceptance; Happ tunnel-check остаётся запрещённым. Delivery=local only, external acceptance=OPEN.
- [ ] **5. Завершить local commit и отчёт.** Scoped documentation/check changes commit `test: verify account security and document acceptance`; обновить checkboxes только доказанными результатами. Проверить clean/known dirty state. Не push/merge/release; итог не закрывает автоматически внешнюю приёмку С01.

**Gate:** Все локальные AC доказаны на текущей ревизии и review завершён. Непредоставленные внешние ресурсы остаются явными prerequisites, не мешая локальной реализации.

## Покрытие и последовательность

| AC спецификации | Tasks |
| --- | --- |
| 1–3: reset, proof limits, anti-enumeration | 1,2 |
| 4: known password/current rotation | 3 |
| 5–7: два email, cancel/resend, владение | 4,5 |
| 8: stale login/credentials/lock order | 1,3,4 |
| 9–10: revoke-others, restricted logout | 3,4 |
| 11–13: чужая cookie, failures, mail isolation | 1–5 |
| 14: account/VPN invariants | 4–6 |
| 15: additive migration/restore invalidation | 1,6 |
| 16: source/generation/API/browser/a11y | 1–5,7 |

Порядок: **1 →2 (первый сквозной gate) →3 →4 →5 →6 →7**. Это один связанный auth subsystem: инфраструктура и DTO входят в использующий их срез. Я рекомендую **Native** с одним свежим whole-branch review: Tasks зависят от общей версии credentials, блокировок и mail helper, параллельная реализация добавит передачу контекста без независимых границ. Метод выбирает пользователь после чтения плана.
