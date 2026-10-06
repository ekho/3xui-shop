# С13 — локальная приёмка YooMoney

Дата: 2026-10-06. Владелец [#18](https://github.com/ekho/3xui-shop/issues/18),
контракт [2026-10-06-c13-local-stubs-v2](https://github.com/ekho/3xui-shop/issues/18#issuecomment-6014628265).
[Спецификация](../superpowers/specs/2026-10-06-s13-yoomoney-local-design.md) ·
[Native-план](../superpowers/plans/2026-10-06-s13-yoomoney-local.md).

## Ревизия и уровни проверки

Ветка feature/c13-yoomoney-local создана от fresh v2 `5144fc26b68d9660914e09459849b00303914dbe`.
Product/API/migrations/dependencies равны проверенному С11 `d407ad344e7c540a3946bd6ff1077977d82feff4`;
изменены только документы и дополнительные HTTP-тесты. Прежние два HTTP-теста сохранены без изменения assertions.
SHA256 нового purchase_test.go: `0d2c833db6ce7c2e6f335122a25afc5136b95195d16ea23b7084234c1a1a2538`.
Тест выполнялся на HEAD810fa35 с этим ещё не закоммиченным файлом; связь с последующим committed source проверяется его хешем и отсутствием product diff, без заявления о запуске на будущем commit.

Новая connected проверка заняла **53.748s**: **27 именованных корневых тестов PASS**, включая
два новых HTTP-теста и11 boundary subcases, без skip, с race и собственными PostgreSQL/Redis.
Использованы настоящий Echo handler/состав модулей и httptest recorder; это не новый native HTTPS-запуск.
Команда: `go -C backend test -race -v ./internal/httpapi ./internal/modules/payments -run 'TestYooMoney|TestRegression(Purchase|YooMoney)|TestManualPayment.*CrossMethod' -count=1`.
URL подключений читались только из собственных protected FILE-настроек.

Сохранённые **26/26** стадий С11 повторно не запускались: их source d407ad3,
каждый полный исходный лог и131 SHA256 из Native export проверены.
Это Go13 packages/race, Python105/105, browser139/139, generation/types/build/smoke,
actual HTTPS/River/3X-UI3.7.0 и recovery. Signed YooMoney purchase-check **6.263s**
и purchase-restore **12.794s** — прежние успешные запуски, не новые результаты С13.
Browser provider POST перехватывался; native путь действительно использовал собственную панель.
[С11: результаты и доставка](s11-acceptance.md).

## Критерии

| Критерий | Доказательство и граница |
| --- | --- |
| AC01 | Сохранённые TestRegressionPurchaseOrderQuoteAndReplay/CurrentPrefersPaidOlderOrder, HTTP/session/CSRF/Origin и16 purchase browser cases: server quote, PC/AC, exact form, owner и replay. Provider POST в browser перехвачен. |
| AC02 | OfficialVector и RejectsAmbiguousForm проходят в новом запуске. Новый HTTPReceiptBoundary: SHA1-only/unsigned test403, signed test/unknown UUID/non-UUID200, missing gross400; по два повтора, ноль receipt/job/funding/access. Отключение новых продаж не отменяет обработку существующих уведомлений. |
| AC03 | Новый HTTPReceiptBoundary: signed wrong currency/type, net0/net>gross/unaccepted — один retained dispute, ноль job/funding/access, нет checkout/cancel. WrongThenCorrectStaysInReview/LateAndProtectedStayInReview сохраняют сумму/codepro/late. True protected/held случаи синтетические защитные проверки; текущие документы провайдера описывают false. |
| AC04 | Новый HTTPReceiptConflict: первый валидный callback/replay дают один receipt/job; конфликт net с тем же ID/replay сохраняет исходные gross9007199254740993/net9007199254740900/funding ID и блокирует подготовку доступа. Все selected second-payment/cancel/expiry/funding/manual cross-method regressions проходят. |
| AC05 | Source-equivalent native signed HTTP→River→3X-UI3.7.0: новый доступ, переход с триала, прежние IDs/limits/одна операция, неизменный replay, paid-pending backup/restore. Каждая исходная стадия и full log проверены; actual provider delivery/перевод этим не доказываются. |
| AC06 | Native task, один fresh whole-branch review, exact-source CI, manual v2 merge и actual preview/tag/3indexes/6labels — отдельные delivery gates ниже. #18 остаётся OPEN до их завершения. |

## Отклонения и Native rulings

Первый focused запуск **3.671s** остановился на компиляции: expected payment в таблице
был string вместо generated wire.PurchaseOrderPaymentStatus. Изменён только тип expected;
HTTP поведение до этого не проверялось, это не product defect или искусственный RED.
Ошибочный argv в метаданных первого wrapper сохранён вместе с явным actual command.
Первая форма C10 event ошибочно использовала array вместо required:bool; INVALID_EVENT
исправлен по схеме, ALLOWED получен до дорогостоящего запуска. Это не отказ в авторизации.

Все решения ledger в порядке принятия, со стоимостью ошибки:

- Ruling: Native coordinator with one fresh Astra/high whole-branch reviewer — user mandate preserves Native and authorizes documents/implementation/necessary v2 merges; no implementation delegation — cost if wrong: missing a detail before final review; exact brief/base/ledger retain it.
- Ruling: Add acceptance tests for already delivered behavior without inventing a production RED — C13 is a local acceptance task; no product rewrite required, assertions target HTTP signature-first validation, received facts, queue and funding side effects — cost if wrong: tests could merely mirror code; hand-derived statuses/rows/counts and named realistic breaks prevent that.
- Ruling: Reuse unchanged final C11 native/full regressions after strict product/API/migrations/deps equivalence; run new actual HTTP cases with all related YooMoney/purchase/cross-method regressions — no repeated identical26-stage matrix — cost if wrong: stale evidence could hide drift; record source revisions, SHA256 manifests/full logs and reject any production diff.

## Обзор и доставка

Итоговый обзор, CI/merge и собственный preview С13 ещё не завершены.
С11 отдельно доставлена [PR72](https://github.com/ekho/3xui-shop/pull/72),
[2.0.0-dev.33](https://github.com/ekho/3xui-shop/releases/tag/2.0.0-dev.33), #19 CLOSED/Project Done;
эта доставка не подменяет delivery gates С13. Полные обезличенные записи и логи сохранены в собственном private acceptance export.

## Ограничения

**real_payment=false, provider_delivery=false, live_vpn_changed=false**.
Использованы заглушки/подписанные синтетические уведомления; кошелёк, настоящий перевод,
Telegram, Happ/VPN/macOS trust и production не менялись. Unknown legacy label200 не означает импорт legacy pending.
Настоящие конфигурация/доставка/legacy и итоговый перенос остаются С45–С47;
финансовые споры/возвраты — С19/С20, Python удаляется только С47, promos/referrals — Р7.
