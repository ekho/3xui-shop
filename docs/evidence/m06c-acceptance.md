# М06c — локальная приёмка audit owner

Дата: 2026-10-06. Владелец [#60](https://github.com/ekho/3xui-shop/issues/60), контракт `2026-10-06-m06c-audit-v1`.
[Спецификация](../superpowers/specs/2026-10-06-m06c-audit-design.md) · [Native-план](../superpowers/plans/2026-10-06-m06c-audit.md) · [Общее решение](https://github.com/ekho/3xui-shop/issues/60#issuecomment-6010685854).

## Ревизия и результат

Product revision `73668ab42d95c782a3d04fd8d7098381c3f219ba`.
Полная матрица **22/22 PASS**,441.224s на одной committed product revision. Go **14 пакетов PASS** с `RUN_BROWSER_TESTS=1 go -C backend test -race ./... -count=1` (platform208.700s, connected consumers102.927s); Python **105/105**, Playwright **127/127**. Собственный native stack остановлен; API/all15 migrations/dependencies/generation сохранены.
Единственный fresh whole-branch review Astra/high на d23c848..9411983 завершён: Critical0/Important0/Minor0, Ready to merge после обязательных проверок доставки. Exact-source Platform checks push37427279222/PR37427329921 и images37427329731 SUCCESS. PR70 вручную слит: source d4f9e599e4a8990b1409a018e6de39371c45306c, base d23c848354ac663b953d9faa9b49b09d93e04303, merge d796e3a076785ce0f5b933fcd2a22c0f4baf4e26; actual parents совпали, tree source=merge c0f286ee27bb8d6da3995056554432bb31180b04. Preview37428455436 все4 jobs SUCCESS, [2.0.0-dev.29](https://github.com/ekho/3xui-shop/releases/tag/2.0.0-dev.29) prerelease/non-draft, peeled tag exact merge. Bot/backend/web public indexes и6linux amd64/arm64 configs/revision/version/source labels проверены. [Общее завершение](https://github.com/ekho/3xui-shop/issues/60#issuecomment-6011429496).
#60 OPEN/In progress до удаления shared platform/store М06d и собственной архитектурной приёмки всего М06.

| Этап | Seconds | Result |
| --- | ---: | --- |
| names | 0.208 | PASS |
| go-generate | 0.872 | PASS |
| web-generate | 0.647 | PASS |
| generated-drift | 0.026 | PASS |
| compatibility | 0.343 | PASS |
| go-vet | 1.634 | PASS |
| web-types | 2.044 | PASS |
| web-build | 2.255 | PASS |
| runtime-config | 0.239 | PASS |
| go-race-connected | 211.532 | PASS |
| python | 21.101 | PASS |
| playwright | 80.237 | PASS |
| compose-config | 0.240 | PASS |
| compose-build | 22.113 | PASS |
| smoke | 23.208 | PASS |
| native-up | 7.603 | PASS |
| native-check | 46.286 | PASS |
| purchase-prepare | 0.168 | PASS |
| purchase-overlay | 0.847 | PASS |
| purchase-check | 6.145 | PASS |
| purchase-restore | 12.031 | PASS |
| native-down | 1.445 | PASS |

## Поведение и доказательства

Audit_reports владеет всем runtime SQL audit_events. RecordTx сохраняет caller Tx, UUID/time/action и тринадцать nullable полей. Accounts/subscriptions/support/vpn/payments не записывают таблицу напрямую. Legacy identity history остаётся accounts со своим source_id cursor. Concrete reader скрывает pool/store; app и legacy fixture constructor создают одинакового владельца. Два защищённых чтения card/history сохраняют прежние role/target/cursor/error checks. Page возвращает до50 строк и more из прежнего LIMIT51/strict tuple DESC.

Actual SQL-boundary RED3.074s (gLQ4J3) перечислил пять producers, private/root/generated SQL вне владельца. TestAuditReportsPersistedCompatibility сначала отсутствует owner: actual RED0.128s (kQN4ZL); продукт на8d94215 ещё не менялся. Independent raw13-column rows сохраняют NULL/empty/false/int64 max, UUID/FK/время;52 одинаковых timestamps дают50+2 без потерь/повторов, другой account даёт пустой массив. Actual app/card/history совпадают, revoked operator отвергается. Caller-Tx INSERT виден только в собственной Tx, rollback/commit и старый single-actor constraint сохраняются. New checks GREEN5.215s (02NpCt), app2.845s.

Первый compile1.189s (kRkk9m) показал, что access operation reason — schema NOT NULL/generated string; адаптер исправлен на &op.Reason. Fixture assertion5.677s (uGbcQQ), diagnostic3.518s (3jba63): timestamptz вернулся с тем же instant в time.Local вместо fixture UTC. Только test assertions сравнивают UTC instants; production representation не менялась. Эти ошибки не считаются product behavior RED. C06 ALLOWED перед исправлением timestamp assertion; все nullable значения/precision/order сравниваются независимо.

Task-done выполнил широкую focused real PG/Redis race на committed product: PASS154.648s (jcobxO), app10.523s/platform152.049s/httpapi19.172s. Существующие trial/approval/reconsider/reconcile/access/monthly/purchase/support/restriction/credential/legacy/role/rollback/replay проверки сохранены. Caller failure по-прежнему отменяет всю Tx, нет new queues/ON CONFLICT/dedup или второй копии правил. Foreign/root query методы и пустой stale operators generated file удалены. HTTP API, все15 миграций и зависимости равны fresh v2 d23c848; генерация стабильна. C03 реальных persisted/producers/protected-reader boundaries ALLOWED.

## Границы

М06b2 [PR69/dev27](https://github.com/ekho/3xui-shop/pull/69) доставлен и является fresh base. М06d удаляет transitional platform.Service/store; М06 пока не завершён. C26/#36 reports, C29/#39 retention/history/mirror остаются OPEN. Python retirement — С47. Production, реальные provider/Telegram, установленный Happ/VPN/macOS trust исключены. Локальный TLS SMTP не подтверждает внешнюю доставляемость; inherited uncertain-send retry остаётся. Browser interception и real connected/native evidence разделены; новый полный browser-to-real-backend trial/support proof не заявлен.

## Native rulings

Все решения ledger в порядке принятия, со стоимостью ошибки:

- Ruling: Preserve user-authorized autonomous documents/Native/manual v2 merge/preview — accepted #55 mandate covers necessary in-scope stages; no repeated approval menu or implementation delegation — cost if wrong: bounded change would need revision, no production authority inferred.
- Ruling: Reuse fully delivered M06b2 PR69/dev27 source-equal fresh v2 d23c848 without baseline full rerun — current refs/checks/preview and healthy loopback prerequisites verified — cost if wrong: stale baseline/resources; exact refs/health/permissions checked before dependent test.
- Ruling: Free RecordTx and concrete read Service with hidden pool, no generic bus/config/interface — writer already has caller Tx; reader needs owned pool, five producer constructors remain unchanged and no cycle — cost if wrong: trusted read-port caller could omit authorization; existing actual app/HTTP role/target/cursor gates and negative tests retained.
- Ruling: Keep legacy identity events in accounts and defer reports/retention/mirror to C26/C29 — actual history has separate source cursor, open #36/#39 scope verified, no incompatible proposals — cost if wrong: future full history might need another read model; no loss or premature task closure.
- Task 1: Ruling: Include subscriptions/reconcile.go in the owned edit list — global caller inventory found trialActorAudit uses the same caller Tx there; the contract is unchanged — cost if wrong: one extra mechanical caller edit, covered by preserved reconcile tests.
- Task 1: Ruling: Compare timestamp instants in UTC only in independent test assertions — PostgreSQL timestamptz stores an instant and pgx returns time.Local by default; production must preserve old representation — cost if wrong: timezone presentation differences require HTTP acceptance; all other thirteen-field values/precision/order still compared independently.
- Task 1: Ruling: Run the broad focused command once through task-done on the committed product before push — helper already executes and records that exact required command; duplicate pre-commit execution would add no evidence — cost if wrong: a focused failure needs a corrective local commit before publication; task cannot complete or push until green.
- Task 1: Ruling: Defer product branch push until the final reviewed source — contract docs already published, no dependent implementation starts before delivery, and a product push would start another identical costly Platform CI — cost if wrong: intermediate code is local until final push; committed Native/full-matrix evidence remains available and exact final source must pass remote CI before merge.

## Final rulings

Все шесть пунктов Declined to judge оценены координатором по фактическому поведению. Findings и deferred minors отсутствуют; fix pass/re-review не требуются.

- Final: Ruling: New reports, retention and metadata mirror remain C26/C29 — customers keep the existing audit history unchanged; open #36/#39 own the future features — cost if wrong: a later report/history read model must be designed, neither task is declared complete.
- Final: Ruling: Shared platform.Service/store removal and whole-M06 architecture acceptance remain M06d — existing consumers use tested transitional composition and #60 stays OPEN/In progress — cost if wrong: a remaining facade could hide unwanted coupling; M06d has an explicit deletion/import/SQL acceptance gate.
- Final: Ruling: Python retirement remains C47 — current Python runtime is preserved until the roadmap covers its features and controlled cutover is accepted — cost if wrong: temporary dual-language maintenance continues; no second handler is activated in production.
- Final: Ruling: Production, real Telegram/payment-provider, installed Happ/VPN/macOS trust and external SMTP delivery are excluded — local controlled adapters/TLS tests are the authorized proof; users receive no claim of production or deliverability readiness — cost if wrong: external acceptance still requires resources and separate exact-target authority.
- Final: Ruling: No new browser-to-real-backend trial/support run for this ownership transfer — unchanged API and actual browser, connected app/HTTP and native replay/restore proofs cover their stated boundaries separately; a new end-to-end result is not claimed — cost if wrong: integration between those boundaries could still need a dedicated future surface proof; existing mandatory full22 remains green.
- Final: Ruling: Historical RED/GREEN and M06b2 publication use coordinator-owned evidence; current GitHub must be checked by coordinator — actual failure/pass logs and live source-equal base were read, remote canonical owner/authority and v2 re-fetched; exact final CI/merge/release/image checks remain mandatory — cost if wrong: stale or unverifiable remote evidence blocks delivery, not a reason to infer success from reviewer approval.

Ревью проверило полный diff41 файлов, доступность22 логов и результаты матрицы, самостоятельно повторило сравнение API/all15 migrations/dependencies, product-equivalence и whitespace. Полную матрицу повторно не запускало; remote delivery проверяет координатор.

## Публикация М06c

Проверенные index digests dev.29: bot sha256:c0016596db48ad55d50d738c0f18046104433507de8799665035efe92eb2dea8; backend sha256:a7766ef61512ab7ae5bf0c1e74e48b890c05cb6270b76eb6ce690adbb4d9e9ab; web sha256:ede54693f4f3209e56cb1ec9fa1867977d59e217f4b33e79a4ba5f50373b4201. Публичные manifests/configs прочитаны и проверены без GitHub auth. C09 exact merge completion ALLOWED; все14 rulings/review/22 logs сохранены в private acceptance export, удалён только собственный Native scratch. Доставка этой части завершена; М06d и общая #60 acceptance остаются.
