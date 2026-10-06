# С11 — локальная приёмка ручной оплаты

Дата: 2026-10-06. Владелец [#19](https://github.com/ekho/3xui-shop/issues/19), контракт `2026-10-06-s11-manual-payment-v1`.
[Спецификация](../superpowers/specs/2026-10-06-s11-manual-payment-design.md) · [Native-план](../superpowers/plans/2026-10-06-s11-manual-payment.md) · [Общий контракт](https://github.com/ekho/3xui-shop/issues/19#issuecomment-6013365763).

## Результат и ревизия

Product revision `d407ad344e7c540a3946bd6ff1077977d82feff4`, база `c9c075e40f822fbe4f7d058d292f2a14ad07cc3f`.
**26/26 PASS** на одной committed product revision: Go **13 пакетов PASS** с race/подключёнными PostgreSQL и Redis, Python **105/105**, Playwright **139/139**.
Generation/drift, actual composition/import/SQL/API compatibility, vet/types/build/runtime config, Compose/smoke и собственная HTTPS/3X-UI3.7.0 приёмка прошли. Все прежние15 миграций и4 Go/web dependency/lock files (всего19) равны базе; migration16 additive, downgrade блокируется при любой manual history. Собственный native stack остановлен.

| Проверка | Seconds | Результат |
| --- | ---: | --- |
| names | 0.210 | PASS |
| go-generate | 0.926 | PASS |
| web-generate | 0.675 | PASS |
| generated-drift | 0.035 | PASS |
| compatibility | 0.342 | PASS |
| go-vet | 1.038 | PASS |
| web-types | 2.069 | PASS |
| web-build | 2.355 | PASS |
| runtime-config | 0.094 | PASS |
| compose-config | 0.306 | PASS |
| compose-build | 24.885 | PASS |
| smoke | 24.640 | PASS |
| native-up | 8.798 | PASS |
| native-check | 48.123 | PASS |
| purchase-prepare | 0.214 | PASS |
| purchase-overlay | 0.965 | PASS |
| purchase-check | 6.263 | PASS |
| purchase-restore | 12.794 | PASS |
| manual-prepare | 0.260 | PASS |
| manual-overlay | 0.412 | PASS |
| manual-check | 10.768 | PASS |
| manual-restore | 12.898 | PASS |
| go-race-connected | 350.361 | PASS |
| python | 21.347 | PASS |
| playwright | 86.140 | PASS |
| native-down | 2.077 | PASS |

## Критерии приёмки

| Критерий | Фактическое доказательство |
| --- | --- |
| AC01 | TestManualPaymentConfig и ReportDecisionAndSnapshot: default false, protected FILE/inline conflict/UTF-8/NUL/empty/size validation, неизменный snapshot после изменения настройки. Disabled не создаёт новые заказы; уже сделанный перевод можно заявить/решить. Foreign HTTP404 не раскрывает инструкции. Browser проверяет plain text, escaped HTML и disabled-caption без приглашения к новому переводу. |
| AC02 | ReportDecisionAndSnapshot: exact server quote/большая целая сумма, одна active-заявка; report/repeat не создают receipt/job/paid. Истекает только незаявленный заказ; после report cancel/new order запрещены. Native actual HTTP проверяет эти же различия и идемпотентность заявления. |
| AC03 | BoundariesAndInbox/actual HTTPS: текущий operator/verified account, report до approve, exact confirmed_amount_minor и причина. Клиент/неподтверждённый/restricted/foreign target/CSRF/Origin/malformed/oversize не меняют деньги. Reject терминален, причина сохраняется, новый заказ разрешён. Browser требует bank acknowledgement, точную сумму и причину. |
| AC04 | TerminalRacesAndRollback: approve/approve и approve/reject, original/different keys, atomic audit/queue rollback, revoked role до replay. Один terminal decision/receipt/job/access target. Browser потерянного ответа сохраняет key/body, не отправляет второе решение; actual native replay не изменяет issued state. |
| AC05 | CrossMethodFunding/TerminalCrossMethodReceipt/ImmutabilityAndDowngrade + старые YooMoney regressions: подписанный provider callback с manual label сохранён needs_review без auto-approve, включая rejected/canceled/approved/expired и replay; manual status/actor/report/decision/funding неизменны. Forged manual receipt не является funding proof. Прежние YooMoney суммы/held/protected/повторы/поздние события/отмена/неоднозначные формы сохраняются. |
| AC06 | Собственный actual HTTP/River/3X-UI3.7.0: новый доступ и переход с триала, прежние native IDs/лимиты и одна access operation; повтор неизменен. Panel outage сохраняет paid; restore-manual сверяет полный финансовый digest/actor/report/receipt, очищает auth/maintenance идемпотентно, restored writers не запускает, исходный backend после рестарта выдаёт один доступ. Старый signed YooMoney check/restore тоже PASS. |
| AC07 | Все139 browser cases, включая12 manual и сохранённые purchase/catalogue/runtime suites: ru/en/375px/keyboard/queue50+1/empty/error/retry/role revoke/persisted status/poll/reload. Реальные HTTP/security/DB и native TLS проверены отдельно; full browser→real backend путь не заявлен. Generation и owner boundaries стабильны. |

## Воспроизводимые проверки

`go -C backend test -race ./... -count=1` с собственными `TEST_DATABASE_URL_FILE`/`TEST_REDIS_URL_FILE`; `npm --prefix web run test:e2e`; `poetry run python -m unittest discover -s tests -v`.
Native: инструкции [deploy/purchase/README.md](../../deploy/purchase/README.md), собственный localhost Docker project и `prepare-manual` → manual overlay → `check-manual` → `restore-manual` → собственный teardown. Эти команды не подтверждают банковский перевод.
Полные26 логов, JSON этапов, private fixture/dump и доказательства RED/GREEN остаются в own private acceptance export (directories0700/files0600). Опубликованы только обезличенные результаты.

## RED/GREEN и найденные ошибки

Task1 missing public operations/config/types RED0.612s → focused connected Go GREEN47.859s; committed task-done49.260s/все3 пакета PASS. Сначала fixture вызывал только purchase worker, который готовит target; исправлен вызов второго существующего access worker без изменения assertions/доменной логики.
Task2 absent manual radio RED15.225s → manual/purchase/runtime31/31 GREEN18.973s; committed task-done27/27 PASS18.818s. Disabled old-transfer caption RED14.477s → GREEN4.893s. Existing queue button называется Retry; исправлена только ошибочная метка теста.
Task3 первая26 matrix остановилась на manual fixture HTTP409: общий helper корректно raises HTTPError для non-2xx. Manual adapter теперь возвращает status, каждая ветка по-прежнему assert exact200/202/403/404/409.20 предыдущих этапов PASS, teardown PASS; product HTTP не изменялся.
Следующая committed matrix прошла native/recovery/Go/Python, но browser135/138 выявил3 catalogue failures: новый React updater использовал unguarded methods и при ответе{} ломал render. Специальный malformed-methods test RED34.489s; shared array guard исправлен → manual/catalogue23/23 GREEN22.221s. Финальная26 matrix выше проверяет исправленный committed product; прежние failure records/logs сохранены. Повтор не считается transient retry и не скрывает дефекты.

## Native rulings

Все решения ledger в порядке принятия, со стоимостью ошибки:

- Ruling: Native coordinator and one fresh Astra/high final review under existing autonomous mandate — no implementation delegation or new approvals — cost if wrong: coordinator context could lose details; task briefs/BASE/ledger preserve exact boundaries.
- Ruling: Persisted cabinet decision/operator queue notify inside web; outbound non-TG channels remain C27/28 and Telegram UI C32 as accepted roadmap — no generic delivery rewrite — cost if wrong: leaving the cabinet will not deliver a new out-of-band notice until those channel scenarios, which remain OPEN.
- Ruling: Reuse fully delivered M06 v2 base and C10 money/access logic instead of rerunning an unchanged baseline — exact whole-M06 c9 tree and dev31 were verified before task start; the changed final C11 product gets its own full suite — cost if wrong: inherited tests can miss a new cross-method change; focused C11 boundary/funding/race tests and final regression suite cover the actual changed source.
- Task1: Ruling: Invoke both existing workers in the manual fixture — FulfillPurchase prepares and queues one access target; existing AccessWorker/ApplyAccess applies it, as the preserved C10 fixture does. First manual run returned running with a valid target, not applied; product code was correct. Focused test now invokes/replays ApplyAccess and keeps the applied assertion — cost if wrong: a test shortcut could hide a missing runtime link; Task3 uses actual River/3X-UI. Failed run16877ms RpV3Ga preserved; focused GREEN7827ms6EjnqG.
- Task2 BASE196ceff07e1fb9fead8c9c1df4067a2b6ffce7d9; brief read. Ruling: Add manual-payment.spec.ts to the existing Playwright testMatch whitelist — the planned new suite would otherwise never run — cost if wrong: broader suite discovery includes only this accepted scenario. Task1 committed task-done GREEN49260msSThTrW.
- Task2: Ruling: Queue fixture uses existing English Retry, not invented Try again — actual i18n and failure accessibility snapshot show Retry — cost if wrong: assertion could target the wrong action; scoped real queue retry/error/403 assertion remains. First UI run41437ms L6Ycgj passed9manual cases; failed only label, preserved full log.
- Task3: Ruling: Run own native slice/recovery before connected full regressions in one26-stage committed-product matrix — reuses the22-stage owner driver and adds4manual stages, avoiding an identical separate thin/native repeat — cost if wrong: early fixture failure postpones broad checks; driver stops at exact failure and always tears down cabinet-c11. Accepted Task1 actual HTTP was the earlier shared-contract slice.
- Task3: Ruling: Normalize HTTPError only in the manual acceptance adapter — shared local.api intentionally raises for non-2xx; actual first denial409 correctly stopped helper1836msodnAFP. Trace/callers inspected; no product/server API change — cost if wrong: swallowing unexpected code could hide failures; every command asserts exact 200/202/403/404/409. First matrix142342msbkuZVI:20 prior stages PASS incl native/YooMoney/restore; own teardown PASS. Full matrix repeats after changed helper commit to keep one exact committed revision, not a transient retry; before-fix records/logs retained.

## Итоговый обзор и единственный fix pass

Один fresh Astra/high обзор всего range c9c075e..43b93d1/34files: **Critical0/Important1/Minor0**, With fixes. I1: подписанный callback после отказа в manual менял canceled→pending, constraint16 откатывал receipt и возвращал503. Новый TestManualPaymentTerminalCrossMethodReceipt RED11.341s (IgoxoV) подтвердил и503 после rejected, и оживление canceled-заказа; approved/expired siblings прошли.
Минимальное исправление в shared locked UPDATE сохраняет payment_status non-YooMoney заказа; decision constraint не ослаблен, прежняя YooMoney-ветка/подпись/receipt/очередь не меняются. Все4 состояния/replay/one receipt/immutable actor-claim-funding/no new issuance прошли focused GREEN; итоговая26 матрица выше проверяет committed исправление. Повторного ревью не было. Deferred minors отсутствуют.

Все шесть Declined to judge оценены координатором; решения со стоимостью ошибки:

- Final: Ruling: Real banking/provider delivery/legacy wallet cutover/production/Telegram/Happ-VPN-trust/public SMTP/target-host performance remain outside local C11 — the accepted scope and latest C13 stub decision exclude these actual effects, synthetic/native proof is stated accurately — cost if wrong: external readiness still needs exact resources/authority and cannot be inferred from green local tests.
- Final: Ruling: A user leaving the cabinet receives no new outbound manual-payment notice in C11 — accepted C27/C28/C32 own channel delivery, persisted web decision/reason and operator queue are implemented and those tasks remain OPEN — cost if wrong: the user must return or contact support until those channels ship.
- Final: Ruling: Resolution/refund of a retained cross-method needs_review dispute remains C19/C20 — C11 preserves the fact and blocks approval/automatic issuance, no new refund or dispute-resolution API is promised; I1 retention failure enters the fix pass — cost if wrong: support needs a later resolution workflow, and retained money must not be called resolved.
- Final: Ruling: First manual report after the30-minute window stays denied under the accepted contract — report time is explicit and does not invent bank time; support contact remains available, no late-payment override is introduced — cost if wrong: an earlier transfer reported late requires support/reconciliation, the current manual decision API cannot bypass that window.
- Final: Ruling: Current M06/C13 shared authority is coordinator-owned — own PR71/dev31/#60 delivery and C13 canonical6014628265 were actually checked/recorded, C13 stays separate pending and local stubs do not prove real provider delivery — cost if wrong: stale or inherited claims could conceal an external gap; reread remote completion sources before dependent closure.
- Final: Ruling: Code review does not complete CI/manual v2 delivery — exact final source, all required jobs, checked target/actual parents/source-equal tree, actual preview/tag/3public multiarch indexes/6labels and #19 closure remain coordinator gates — cost if wrong: an unverified artifact could be called delivered; #19 stays OPEN until these proofs.

## Границы и следующие проверки

Реквизиты и подтверждение поступления в fixture синтетические: **real_payment=false, live_vpn_changed=false**. Нет реального банковского/provider перевода, настоящей Telegram-доставки, production/cutover, Happ/VPN/macOS trust изменений. Настоящая SMTP-доставляемость и benchmark целевого сервера остаются вне локальной приёмки.
Решение и причина доступны в кабинете/React-admin; outbound channels остаются С27/С28/С32. Python удаляется только С47, promos/referrals — Р7.
По [новому уточнению С13](https://github.com/ekho/3xui-shop/issues/18#issuecomment-6014628265) отдельная локальная приёмка YooMoney будет завершена с заглушками после текущей С11. Проверка настоящей доставки провайдера и legacy/production readiness этим не доказывается; договорённость о real-transfer prerequisite больше не удерживает локальную С13. Остальные provider/renew/history контракты не меняются.

## Доставка в v2

[PR72](https://github.com/ekho/3xui-shop/pull/72) вручную слит в v2. Source `c8f4c28c9a5f10e0a1fc6b6c919001702a9956a2`; merge `5144fc26b68d9660914e09459849b00303914dbe`; actual parents `c9c075e`/`c8f4c28` и source-equal tree `9ecbd0b2671485e5fdcf8e7c49230036c4fd0eb1` проверены SSH fetch. Итоговый source меняет только docs относительно product d407ad3; auto-merge не использован.
Exact-source [PR Platform](https://github.com/ekho/3xui-shop/actions/runs/37456900745), [push Platform](https://github.com/ekho/3xui-shop/actions/runs/37456828726), [PR images](https://github.com/ekho/3xui-shop/actions/runs/37456900691) — SUCCESS.
[Preview37458619616](https://github.com/ekho/3xui-shop/actions/runs/37458619616) —4/4 SUCCESS; [2.0.0-dev.33](https://github.com/ekho/3xui-shop/releases/tag/2.0.0-dev.33) prerelease/non-draft, tag peeled exact merge. Anonymous GHCR чтение подтвердило3 публичных indexes и6 linux/amd64+arm64 config revision/version/source labels:

| Компонент | Index digest |
| --- | --- |
| bot | sha256:08a015638113247a52536a921938cf0ff0ee827e43fa1a5e29625a0e8b6884a4 |
| backend | sha256:b53a0467f13b2c0a7390b435064a3d5c845ad8cdacb014ac41986ea602f7fddf |
| web | sha256:fadd64c4f465ea93d96b69a4454f59fcfd52b4a49e444971b69f43915339669d |

После собственных AC01–07 и этой доставки [#19 CLOSED/Project Done](https://github.com/ekho/3xui-shop/issues/19#issuecomment-6015652047), parent=null; состояние перечитано. Ранее ledgered pending gates теперь выполнены; все8 Native/6 Final rulings и их внешние ограничения сохранены, deferred minors отсутствуют. Это локальная приёмка и предварительный v2 release; production/provider delivery не подтверждены.
