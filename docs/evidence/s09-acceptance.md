# С09: локальная приёмка каталога

Локальная проверка выполнена на продуктовой ревизии
`401a678703acc582a92bd8a98dcb8420c9c1fd45`. Отдельный host-only
фикс guard в acceptance bridge — `04c6c42`; backend/gateway image от него
не менялись. Точные owned images:
backend `sha256:f7e0604e25b1adab49e37c018e5541ec4efbf6d4fa485f05969172542d58f2dd`,
gateway `sha256:866729104f7e3ba3c38a2b921acf44d899edde6e08e4c6d843b34a5c8c570842`,
3X-UI 3.7.0 `sha256:3b3131f1876e6bf35063a9ec4dd1c594e4525180bfc2e1c477dcc8a3c9550ca1`.
[Runtime proof](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s09.json).

Реальная поверхность — только собственный Docker project `cabinet-s01-local`,
HTTPS `localhost:58443`, Mailpit, PostgreSQL и закреплённая 3X-UI 3.7.0.
Приватные JSONL записи в `.superpowers/sdd/2026-10-02-s09-catalogue/e2e/`
содержат criterion, real target, exact direct command, expected, actual,
verdict и непустую ссылку на артефакт. [Run manifest](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/run-manifest.json)
хранит также точные bounded wrapper команды и логи. Email, cookies,
credentials, raw panel replies и личные данные в evidence не выводятся.

Успешный browser вызов завершился exit0 за25.392s: **13/13 PASS,
0 FAIL/BLOCKED** ([rows](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/browser.jsonl)).
Один исправленный import/restore вызов завершился exit0 за23.512s:
**10/10 PASS** ([append-only rows](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/local.jsonl)).

Выполненные bounded команды из корня
`/Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop`:

```text
node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s09/browser.mjs
node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- python3 deploy/s09/local.py import-acceptance
```

Первый import вызов до host guard фикса `04c6c42` остановился до первой
проверки: exit1 за0.765s, один **BLOCKED** record в append-only
`local.jsonl`. Один read-only диагноз подтвердил, что disposable БД
создана, но не мигрирована. Причина — драйвер ожидал путь основного DSN
`/cabinet_s01`, тогда как authoritative `s01.local.database()`
разрешает собственный `cabinet_s01_restore_<hex>`. Guard исправлен;
только import/restore был запущен повторно по отдельному разрешению root.
Первая пустая disposable БД сохранена без данных; browser не повторялся.

Источник HTTP ожиданий — [OpenAPI](../api/openapi.yaml): create
`{terms,reason}` →201, revise `{terms,expected_revision,reason}` →200,
archive `{expected_revision,reason}` →200; клиентский GET отвечает
`{plans}`, операторский — `{plans,total,page,per_page}`. Полные условия
включают `devices`, `traffic_gb`, `profile`, `hidden`, `periods`
и `prices[{period_days,currency,amount_minor}]`. Источник CLI ожиданий —
`backend/cmd/server/catalogue.go` и
`backend/internal/s01/catalogue_import.go` на продуктовой ревизии выше.
CLI package содержит `version:1`, `durations`, `plans`; цена legacy
передаётся строкой major units и точно переводится в minor units.

| AC | Реальный target; точная команда выше | Ожидание | Факт | Verdict и артефакт |
| --- | --- | --- | --- | --- |
| 1 | HTTPS cabinet/operator API, Chromium, own PG; browser command | Пустой каталог не выдумывает цены; оператор создаёт, читает, меняет и архивирует; клиент выбирает текущие видимые условия | До создания 0 планов/редакций; nonoperator GET 403; create/replay/conflict/second 201/201/409/201; client GET два видимых плана, operator GET page1/per_page1 дал 1 из2; реальный admin UI create/revise/archive 201/200/200 | **PASS локальных действий и выбора**; [browser rows](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/browser.jsonl) |
| 2 | HTTPS client/API и disposable PG CLI; browser и import команды | Полная матрица периодов/RUB/USD/XTR, точная строка >2^53, нулевая цена/traffic; invalid fields/profile/period/price отклонены | Клиент получил точную minor строку `9007199254740993`, traffic0 и XTR0; EN browser показал `90,071,992,547,409.93 RUB` и `0 XTR`. Десять invalid API payload получили400 без изменения digest/revision; importer сохранил ту же точную цену, шесть malformed packages вернули `IMPORT_INVALID_PACKAGE` без записи | **PASS проверенных границ**; [browser](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/browser.jsonl), [import](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/local.jsonl) |
| 3 | Own PG immutable revisions; browser и import команды | Старые revision и actor metadata сохраняются при edit/archive; большие цены не округляются; restore оставляет историю | Edit 200, replay200, stale/body409/409; revision1 digest неизменен, новая revision2 с web actor/reason; после archive старые digest сохранены. Legacy import дал 2 snapshots с NULL actor/reason и exact price; backup/restore сохранил 5 планов/9 revisions и digest | **PASS immutable catalogue history**; S09 ещё не создаёт order или plan-bound subscription, поэтому сохранность такого executed snapshot будет проверяться в С07/С10. [browser](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/browser.jsonl), [import](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/local.jsonl) |
| 4 | HTTPS concurrent POST и own PG; browser и import команды | Один из двух concurrent archive откажет; последний visible и active devices slot защищены; replay/optimistic/body конфликт не дублируют revision | Concurrent archives 200/409, last-visible archive409, visible1 и неизменный digest; occupied devices409, архивный слот переиспользован201, visible2. Изменённый legacy ID `IMPORT_CONFLICT` и occupied devices `CATALOGUE_DEVICES_CONFLICT` откатили целый пакет | **PASS**; [browser](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/browser.jsonl), [import](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/local.jsonl) |
| 5 | HTTPS operator API, реальные session/CSRF/Origin; browser command и focused real-PG test | Неоператор/revoked/restricted actor и forged actor/CSRF/Origin не меняют каталог; клиент не видит hidden | Nonoperator403, forged400, CSRF403, Origin403, revoked403; каталог digest прежний. После seed client count2, operator total4: hidden отсутствует у клиента. `TestCatalogueRestrictedReaderAndLiveOperator` запретил оба чтения и create/revise ограниченному аккаунту/оператору; одна исходная revision сохранена | **PASS live guards и focused source guard**; [browser](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/browser.jsonl), [focused proof](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/catalogue-restricted-check.json) |
| 6 | Fresh disposable DB in own PG и CLI; browser seed | Dry-run без committed change, apply/replay idempotent; invalid package/occupied slot/ambiguous unlimited без частичной записи; seed hidden7/100 не перезаписывает operator plan | Dry-run created2, DB unchanged; apply created2, replay replayed2/no new revision; шесть malformed пакетов invalid; occupied7 seed conflict, два active unlimited seed ambiguous, оба DB unchanged. Main seed created true/false при replay, revision1, actor/reason NULL, devices7/traffic100, hidden клиенту | **PASS**; [import](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/local.jsonl), [browser](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/browser.jsonl) |
| 7 | Real RU/EN Chromium375px, own runtime, source suites; browser/import commands и перечисленные ниже source commands | Client/admin keyboard/focus, no payment promise, local regression S01–S06/S48 и VPN baseline | RU/EN client выбор с Enter, no overflow, цена exact/0, no payment button; admin RU Enter открыл labelled form, no overflow; admin create/revise/archive 201/200/200. Postflight bot/reconcile stopped, VPN connected/config same. Restore auth cleanup second/residual zero, catalogue unchanged. Backend Go race/vet, frontend web82/82 и root web build exit0; root сверил артефакты | **PASS в пределах локальной приёмки**; [browser](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/browser.jsonl), [import](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/local.jsonl), [source-check manifest](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/regression-checks.json), [runtime](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s09.json) |

На product source `401a678` backend-проверки выполнили из `backend/`
`go test -race ./... -count=1` (exit0, 6 tested и2 no-test packages) и
`go vet ./...` (exit0); frontend-проверка из корня выполнила
`npm --prefix web run test:e2e` (82/82). Root выполнил
`npm --prefix web run build` (exit0) и напрямую сверил все артефакты. Точные bounded логи
связаны в [приватном source-check manifest](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/regression-checks.json).
UI fault/error/retry и stale draft также покрыты
[web S09 tests](../../web/tests/s09.spec.ts); real browser проверил happy path,
мобильную ширину и keyboard, но не вводил fault injection.

Дополнительный restricted-actor тест native backend выполнил на real-PG testkit
за2.319s: `go test ./internal/s01 -run '^TestCatalogueRestrictedReaderAndLiveOperator$' -count=1`.
Для этого использован committed С09 wire; незавершённый С07 RED scaffold
временно исключён, оба файла восстановлены в `finally`. Root сверил исходник
теста и handoff; [focused proof](../../.superpowers/sdd/2026-10-02-s09-catalogue/e2e/catalogue-restricted-check.json)
сохраняет команду, результат, исполнителя и source hashes. Продуктовый код С09
после основной проверки не менялся.

Production, real-data import, Telegram cutover, Happ, установленный Mac VPN,
trust/clipboard и внешняя публикация не проверялись. С09 не обещает
оплату или выдачу по выбранному тарифу: это задачи С10 и С07.
