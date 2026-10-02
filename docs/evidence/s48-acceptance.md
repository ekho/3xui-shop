# С48: локальная приёмка ограничений аккаунта

Проверка выполнена на собственном Docker project `cabinet-s01-local`:
`https://localhost:58443`, PostgreSQL, Mailpit и закреплённая 3X-UI 3.7.0.
Продуктовый исходник backend/gateway — `208f3e4cffb7b180947a9d80b8241623f1313255`;
отдельный последующий host-side фикс экспортёра времени — `aff5f74771980356bf709047d6dbe211487b1923`.
Образы: backend `sha256:ebd67befc6907adff3c6b5077686489f620e220b335c3c61a7f31aea63a45a66`,
gateway `sha256:d323ec49f85099b064a14b98132cbcbb157355170c97ffe6e04621248a6d6986`,
panel `ghcr.io/mhsanaei/3x-ui:3.7.0@sha256:3b3131f1876e6bf35063a9ec4dd1c594e4525180bfc2e1c477dcc8a3c9550ca1`.
Rollback tags: `cabinet-s48-backend:rollback-208f3e4` и
`cabinet-s48-gateway:rollback-208f3e4`. [Приватный runtime manifest](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/manifest.json).

Ниже обозначены **точные вызовы** из корня worktree. Буквы в таблице ссылаются
на эти вызовы, а каждый отдельный JSONL record содержит прямую команду,
ожидание, факт, verdict и непустой путь к артефакту.

```text
I = node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- python3 deploy/s48/local.py import-acceptance
B = node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 360 --lines 12 -- node deploy/s48/browser.mjs
P = node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 90 --lines 10 -- python3 deploy/s48/local.py native-probe
F = node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 180 --lines 12 -- python3 deploy/s48/local.py focused-invariants
R = node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 90 --lines 12 -- python3 deploy/s48/local.py registration-probe
```

`I` exit 0, **10/10 PASS** ([rows](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/import.jsonl));
`B` exit 0, **16/16 PASS** в успешном сегменте ([rows](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/browser.jsonl));
`P` exit 0, **1/1 PASS** ([row](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/native-probe.jsonl));
`F` выполнен ровно один раз после согласования, exit 0, **3/3 PASS**
([rows](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/focused-invariants.jsonl));
`R` — один read-only SQL по уже созданным аккаунтам, exit 0, **1/1 PASS**
([row](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/registration-probe.jsonl)).

| AC | Реальная поверхность; команда | Ожидание | Наблюдаемый результат | Verdict; артефакт |
| --- | --- | --- | --- | --- |
| 1 | HTTPS cabinet, Mailpit, trial API; `B`; read-only own PG `R` | Подтверждённый новый web-account входит без approval, отказ в триале не ограничивает его; нет registration-approval/reminder jobs | Три новые browser-сессии, `kind=web`, `restricted=false`; trial request/reject 201/200, затем `restricted=false`; `R` нашёл 17 synthetic web accounts без TG/legacy identity, 0 legacy approval snapshots/events и 0 matching registration/approval/reminder River jobs. Старый bot scheduler остановлен | **PASS для локального состояния**; [B rows](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/browser.jsonl), [R row](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/registration-probe.jsonl) |
| 2 | HTTPS login/API/key/reset, PG, 3X-UI; `B`, `P`, `F` | Настоящий actor/reason; restrict отзывает sessions/proofs и блокирует доступ, release требует новый вход; support/VPN ban, native identity/expiry/limits/traffic/operations сохраняются | Restrict/replay/conflict/new-key noop 200/200/409/200, audit 1, sessions 0; старые me/key 401/401, новый login 403, proof 400; release 200, old cookie 401, new login 200. `P` подтвердил root `usedTraffic`; `F` сравнил true bans, support digest, native identity/expiry/limits/enable/counter и operations до/во время/после | **PASS**; [B](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/browser.jsonl), [P](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/native-probe.jsonl), [F](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/focused-invariants.jsonl) |
| 3 | HTTPS operator API, реальные web cookies; `B` | Неоператор/отозванная роль/protected target/forged actor/CSRF/Origin/invalid reason отклонены без записи | Неоператор 403, отозванная роль 403 `INVALID_CREDENTIALS`, self/protected 403 `OPERATOR_ACCOUNT_PROTECTED`; forged actor 400, пустая/длинная/NUL причина 400, CSRF/Origin отклонены, target unchanged | **PASS для названных live guards**; отдельная чужая сессия не создавалась. [B rows](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/browser.jsonl) |
| 4 | HTTPS restriction API и PG audit; `B` | Replay/lost response и body conflict не дают двойной аудит; противоположные переходы согласованы | 200/200/409/200 с одним аудитом; concurrent opposite 200/200, итог unrestricted, audit deltas 1/1 | **PASS**; [B rows](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/browser.jsonl) |
| 5 | Read-only synthetic SQLite export → stdin importer → own PG; `I`; HTTPS history/reimport `B` | Dry-run без записи; pending/rejected/approved, NULL/actors/IDs сохранены; replay no-op; invalid/conflict/identity rollback; ручной release переживает reimport; SQLite byte-identical | 3 users/52 events; states false/true/false; NULL/actors/source IDs сохранены; dry-run PG unchanged, replay 0/0/0; malformed package, changed snapshot, unknown account и NULL/wrong legacy_user_id отвергнуты без записи; после release/reimport unrestricted; SQLite SHA равен | **PASS**; [I](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/import.jsonl), [B](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/browser.jsonl) |
| 6 | Реальный React-admin Chromium, RU/EN, 375px, keyboard; `B`; Playwright component `npm --prefix web run test:e2e` | Два действия и отдельная read-only legacy history; mobile/keyboard/pagination; pending/error/retry | EN restrict и RU Enter release 200/200, focus true, overflow false; rejection card, 50+2 events через cursor, кнопки approval нет; imported no-op показывает NULL metadata. [S48 component test](../../web/tests/account-restrictions.spec.ts) проверяет confirmation, pending disabled, aborted POST/retry с тем же ключом и сохранённой причиной; live fault injection не проводился | **PASS, с разделением live/component**; [B rows](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/browser.jsonl), [suite manifest](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/regression-checks.json) |
| 7 | `pg_dump`/`pg_restore` в disposable DB own project; `I` | Restriction/snapshots/events сохранены, существующая C02/C06 auth cleanup повторяема | Restored row state equal; первая cleanup counts 6/0/0, вторая 0/0/0, residual 0/0/0 | **PASS для disposable restore**; live app DB не заменялась. [I rows](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/import.jsonl) |
| 8 | Own runtime, regression suites, runbook; `B`, `F`, команды ниже | С01–С06/API consumers работают; future cutover/reminders обозначены | Browser/focused postflight: bot/reconcile stopped, no Telegram operators, Docker VPN connected и config baseline equal; root suites green. [Spec boundary](../superpowers/specs/2026-10-02-s48-account-restrictions-design.md#react-admin-и-старый-бот) сохраняет единое выключение legacy middleware/scheduler/reminders позже | **PASS в пределах local checks**; [B](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/browser.jsonl), [F](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/focused-invariants.jsonl), [suite manifest](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/regression-checks.json) |

Root отдельно выполнил на продуктовой ревизии `208f3e4` в `backend/`
`go test -race ./... -count=1` (exit 0, шесть tested packages),
`go vet ./...` (exit 0) и из корня `npm --prefix web run test:e2e`
(71/71, включая С48 7/7). После фикса экспортёра `aff5f74` выполнено
`poetry run python -m unittest discover -s tests -v` (105/105, exit 0).
Для Go/Python использованы собственные file-backed
`S01_TEST_DATABASE_URL_FILE` и `S01_TEST_REDIS_URL_FILE`; пути логов и
точные обёртки находятся в [приватном suite manifest](../../.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/regression-checks.json).
Граничные сценарии, не вызванные повторно на live surface, проверены исходными
Go тестами этой race-команды: [replay, protected target, no-op metadata,
role change и rollback](../../backend/internal/platform/restrictions_test.go),
[HTTP restricted logout и cookie context](../../backend/internal/httpapi/account_security_test.go),
[role CLI и revoke](../../backend/cmd/server/operator_test.go).
Это тестовое покрытие, а не дополнительный live-прогон.

Append-only `browser.jsonl` после всех попыток содержит 37 PASS, 3 FAIL и
5 BLOCKED. Успешный сегмент `B` — именно 16 последовательных PASS, не
последний расширенный прогон. Ранние сбои драйвера: selector регистрации и
ожидание ciphertext после Mailpit delivery, который уже обнулялся доставкой.
Два поздних расширенных прогона ошиблись в ожиданиях драйвера:
`up/down` вместо корневого `usedTraffic` pinned panel и HTTP 200 вместо
фактического 204 для support-ban. `P` сузил mapping, `F` проверил недостающие
инварианты. Полного browser повтора после этих ошибок не было. Exporter fix
`aff5f74` проверен Python suite; `I` был до него и не повторялся.

Граница будущего переключения: legacy registration approval middleware,
scheduler и reminders останавливаются вместе при cutover С32/С46/С47.
Триальный approval С01 продолжает работать; legacy-код и данные сохраняются.
Production, real-data import, Telegram cutover, Happ, установленный Mac VPN,
trust/clipboard и внешняя публикация не проверялись. В документе и логах
нет email, Telegram ID, cookie, key, SMTP token или сырого panel ответа.
