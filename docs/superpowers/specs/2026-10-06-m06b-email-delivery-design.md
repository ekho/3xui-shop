# М06b2 — Email delivery внутри notifications

Дата: 2026-10-06. Контракт `2026-10-06-m06b2-email-delivery-v1`, owner [#60](https://github.com/ekho/3xui-shop/issues/60).
Спецификация принята в рамках разрешённого автономного ведения документов и Native.
Base fresh origin/v2 `729eea58b9a510ccc8e5ca191673861407d50555`;
branch feature/m06b-email-delivery. [М06b1 полностью доставлен](https://github.com/ekho/3xui-shop/issues/60#issuecomment-6009555826).

## Результат и границы

Существующая email-доставка имеет одного владельца notifications: mail_deliveries SQL,
шифрование, шаблоны, SMTP и River worker. Accounts сохраняет регистрацию, credential
proofs, лимиты, их проверку/отзыв и общий email guard. API/schema/data/dependencies
не меняются. Один Go HTTP/River/Telegram процесс; никаких новых кампаний или писем.
#60 остаётся OPEN: далее audit M06c → удаление shared platform/store M06d и собственная
архитектурная приёмка. Python retirement — С47. Production, реальные платежи/Telegram,
установленный Happ/VPN и macOS trust исключены. Только local + ранее разрешённый v2 preview.

## Проверенные исходные факты и выбор

accounts/mail.go сейчас шифрует и отправляет, accounts/private registration/credentials
queries владеют mail SQL. platform/mail.go содержит TLS SMTP/worker; cmd/server регистрирует
его. Во время SMTP удерживается SQL Tx с email/proof/delivery locks. Сетевые вызовы вне
SQL Tx — принятый архитектурный контракт; просто убрать Tx нельзя: concurrent revoke
тогда сможет завершиться раньше старой отправки.

Выбран существующий в vpn/owner.go pattern: выделенная pooled connection с session
advisory lock на прежнем `hashtextextended(email,0)`. Она сохраняется через короткие Tx
и SMTP вне Tx. Альтернатива — оставить открытый Tx — нарушает архитектуру; новая отдельная
mail lease/outbox migration добавила бы данные и изменённые гарантии без необходимости.
Accounts предоставляет guard/validation function hooks; notifications не импортирует
accounts, private peers, app/platform/wire/HTTP. Accounts использует конкретный public
MailService. App связывает hooks через поздно присвоенный accounts owner, без import cycle.
В notifications отдельный concrete MailService соседствует с существующим Telegram Service;
это два канала одного владельца, без общей шины или нового framework/interface.

## Публичный контракт

`notifications.MailPayload` — `struct{ Type, Locale, Token, Code string }` без JSON tags;
сохраняются старые uppercase encrypted payload keys. `MailArgs{DeliveryID uuid.UUID}`
имеет `json:"delivery_id"`, Kind()="mail_delivery". MailConfig содержит CabinetOrigin,
MailKey, SMTPAddress/From/User/Password, SMTPRootCAs и Now; cfg getter сохраняет актуальные
настройки тестовой композиции. При Now=nil используется time.Now. Sender=nil выбирает
собственный SendSMTP; отдельный sender применяется для контролируемого transport test.

- `NewMail(pool *pgxpool.Pool, queue *river.Client[pgx.Tx], cfg func() MailConfig, guard func(context.Context,string,func(*pgxpool.Conn) error) error, valid func(context.Context,pgx.Tx,*uuid.UUID,*uuid.UUID)(bool,error), sender func(context.Context,string,string,string) error) *MailService`.
- `(*MailService).EnqueueMailTx(ctx context.Context, tx pgx.Tx, email string, registrationID, credentialID *uuid.UUID, kind string, payload MailPayload, createdAt time.Time) error`.
- `(*MailService).ClearRegistrationMailTx(ctx context.Context, tx pgx.Tx, email string) error`.
- `(*MailService).ClearCredentialMailTx(ctx context.Context, tx pgx.Tx, proofIDs []uuid.UUID) error`.
- `(*MailService).SendMail(ctx context.Context, id uuid.UUID) error`.
- `SendSMTP(ctx context.Context, cfg MailConfig, to, subject, body string) error`.
- `MailWorker{Service *MailService}`: Work invokes SendMail(job.Args.DeliveryID), existing River registration/default queue workers unchanged.
- `accounts.New(pool *pgxpool.Pool, limiter *redis.Client, mail *notifications.MailService, cfg Config) *Service`; mail encryption/SMTP/queue config leaves accounts.
- `(*accounts.Service).WithMailGuard(ctx context.Context, email string, work func(*pgxpool.Conn) error) error`.
- `(*accounts.Service).MailProofValidTx(ctx context.Context, tx pgx.Tx, registrationID, credentialID *uuid.UUID) (bool,error)`.

Enqueue/Clear use only caller Tx; mail row + River job MaxAttempts5 остаются атомарны
с challenge/credential/account/session/audit. ClearRegistration сохраняет email + kind
registration + delivered_at IS NULL. Accounts выбирает ID revoked/used credential proofs
по прежним account/reset predicates и передаёт их owner; ClearCredential обновляет только
kind credential. Не вводятся foreign-table JOIN или SQL вне владельца mail.

## Guard, проверка и отправка

1. Lookup delivery: отсутствующий ID — success/no send. Acquire dedicated pooled connection,
   session advisory lock по прежнему email namespace, без account row lock. Work использует
   эту же connection: MaxConns1 не требует второй connection для собственных Tx.
2. В короткой Tx accounts проверяет credential proof (FOR UPDATE) и account (plain SELECT),
   с прежними nil account/revoked/used/confirmed/expired/version/original email правилами.
   Owner lock mail row; cleared/delivered — no send. Invalid proof — CompleteMail и commit,
   без SMTP. Registration challenge проверяется после этого с прежними revoked/expired правилами.
3. Owner decrypt AES-GCM: nonce prefix, AAD delivery UUID.String(), прежний payload и ru/en
   verify/login/reset/email_change/security_notice content. Ошибка ciphertext/key/proof/DB —
   SERVICE_UNAVAILABLE, ciphertext не теряется. Проверенная Tx commit ДО сетевого вызова.
4. SMTP с прежними sender validation, TLS>=1.2, ServerName/RootCAs, 10s deadline/cancellation,
   auth и MIME. Во время сети нет открытой SQL Tx, session email guard остаётся. При успехе
   короткая Tx на той же connection CompleteMail(ciphertext=NULL, delivered_at) + commit.
   Ошибка сети/commit сохраняет возможность повтора; exactly-once SMTP не обещается.
5. Cleanup session locks: Background timeout1s + pg_advisory_unlock_all; при ошибке закрыть
   physical connection перед Release, как существующий VPN owner. Cancellation не оставляет
   lock в pool. Lost connection/uncertain SMTP сохраняет прежний риск повторной доставки.

Существующие registration resend/verify и credential mutations берут те же xact email
locks; они ждут SMTP guard. Credential lock order account → sorted/compacted recipients →
proof сохраняется. **revokeCredentialProofs** централизованно берёт current valid email +
CredentialRecipients guards перед revoke: SetOperatorRestriction и ImportLegacyApprovals
также проходят через него, раньше их защищал proof row lock через SMTP. Уже удерживаемые
xact email locks повторно брать безопасно; пустой email Telegram-only account не добавляется.
Пакетный ImportLegacyApprovals сначала блокирует все сопоставленные account rows в
существующем UUID-порядке, затем берёт email guards и меняет данные. Нельзя брать
account B после recipient guard аккаунта A: concurrent CancelEmailChange(B) может
образовать цикл. Dry-run остаётся read-only; один commit/replay/identity guards сохранены.
Нельзя оставлять защиту только в одном операторском caller. Account row не удерживается worker
во время SMTP, иначе возможен deadlock с mutation.

## Композиция и переходные facades

App создаёт MailService и hooks, затем accounts owner. Root legacy test composition делает
то же с cfg/clock getters. platform.SendMail — только notificationError mapping;
MailArgs/SendSMTP compatibility facade допустимы до M06d. Root SMTP implementation/worker
удаляются, cmd/server регистрирует notifications.MailWorker с конкретным owner. Существующие
HTTP/browser consumers и mail job ciphertext/ID/job args продолжают работать без миграции.

## Приёмка

- Actual SQL-boundary RED на accounts mail SQL → GREEN; mail больше не входит accountSQL
  matcher, собственный notifications matcher ловит SELECT/JOIN/UPDATE/INSERT/DELETE/quoted public.
  Сохраняется прежнее эксплуатационное исключение post_restore_auth.sql: очистка после
  restore при закрытом ingress и остановленных writers/mail workers, без изменения процедуры.
- Actual real TLS DATA hold показывает SQL Tx RED → GREEN (xact_start отсутствует), recipient
  guard остаётся, account row свободен. Existing password/reset/cancel race и новый restriction
  case проходят; после revoke старое письмо не отправляется/старый proof не принимается.
  TestLegacyApprovalMailLockOrder воспроизводит batch A/B с общим pending target C и
  concurrent cancellation: old account A → C → B RED503 → all accounts first GREEN.
- Pre-seeded legacy AES-GCM/AAD/uppercase payload + old River mail_delivery args/max_attempts:
  direct owner + facade доставляют прежний link/code и очистку; повтор не меняет delivered row.
  Unknown/expired/revoked/confirmed/version-changed proof без SMTP; bad ciphertext не очищается.
- Caller Tx rollback/River INSERT failure не оставляют частичного mail/proof/job; old migrations
  data unchanged. Cancellation освобождает session guard, односоединительный pool не deadlock.
- Existing connected Go race/HTTP/127 browser/Python105/native3XUI3.7/TLS/restart/purchase/restore
  regression; API, all15 migrations и dependency files равны base. Full22-stage matrix at one
  committed product revision, own stack stopped; one fresh Astra/high whole-branch review.
- Exact-source CI → manual SHA-guarded v2 merge → actual parents/source-equal tree → preview
  release/tag/three linux amd64+arm64 images. Общий M06 остаётся OPEN до M06c/M06d acceptance.
