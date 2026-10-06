# С10 — приёмка интеграции первой покупки

Дата: 2026-10-06. Владелец #17, решение `2026-10-06-s10-modular-integration-v1`.
Исходный PR #5 сохранён: afaeacf652964453ddd61883aa6da6a0d285783c включён обычным
merge в ветку от v2 26d4b96733881d89b0538e479c2facdca232f419. Старый dirty checkout
и его история не изменены. [Прежняя функциональная приёмка](s10-acceptance.md)
доказывает прежний source; настоящая запись доказывает адаптацию к модулям.

## Ревизия и результат

Полная локальная матрица: `760fbe8cb84ba842a4c29419374140ffc36c4b22`, **22/22 PASS**, суммарно
406.513 секунд выполнения проверок.
Implementation/local verification завершены; свежий final review и delivery
пока pending. #17 остаётся открытой до отдельных review/CI/merge/preview gates.

| Проверка | Результат |
| --- | --- |
| Генерация и совместимость | `make -C backend generate`, web `api:generate`, no drift; JSON semantic comparison сохраняет все 54 прежних paths/87 schemas; API С10 и migration15 идентичны afaeacf, migrations1–14 и зависимости идентичны v2. |
| Статические проверки | Semantic naming, Go vet, TypeScript, web test build и runtime-config PASS. |
| Go и подключённые потребители | `RUN_BROWSER_TESTS=1 go -C backend test -race ./... -count=1`, настоящие изолированные PostgreSQL/Redis; все 14 пакетов с тестами PASS, platform190.713s, connected tests101.798s. |
| Web/Python | Playwright **126/126**, Python **105/105** PASS. Форма YooMoney перехватывается до внешнего запроса. |
| Контейнеры | Compose config/build backend/gateway/bot, HTTPS/routing/secrets/migration/restore smoke PASS. Legacy image собирается, процесс Python не запускается в native profile. |
| Native process | Go HTTP/jobs и simulated Bot API, TLS SMTP, HTTPS и настоящая 3X-UI **3.7.0**; остановка compiled backend после commit и restart сохраняют operation/grant/keys. Telegram выключен. |
| Покупка | Existing `deploy/purchase/local.py prepare/check`: подписанный localhost callback выдаёт новый доступ и переводит конечный триал без смены идентификаторов/grant, оставшийся срок сохраняется. Test/tampered notice не оплачивает; повтор receipt не увеличивает доступ. |
| Paid restore | Existing `deploy/purchase/local.py restore`: панель и backend временно остановлены; pending paid dump восстановлен в отдельную собственную БД, деньги/receipts/access сравниваются, restored workers не запускаются; source restart выдаёт один native доступ. |
| Cleanup | Собственный native stack остановлен; секреты/dumps/полные диагностические данные остаются в закрытых игнорируемых каталогах. |

## Новые границы и RED→GREEN

Accounts lookup/lock идут через существующие owner facades; quote — через
catalogue.LockCurrentPlan; подготовка и очередь — через VPN physical owner и
public ports. SQL чужих owners не восстановлен. Funding/outcome callbacks
передаются явно через constructor; caller Tx объединяет access/assignment/profile
с payment outcome, включая review/requeue. Деньги временно остаются в platform
до М05; схема, публичные API и денежные правила прежние.

До адаптации фактически наблюдались compile RED на удалённых global queries и
девять purchase/recovery RED из-за отсутствующего исполнения/outcome. Новые
rollback/missing-hooks/preparation tests тоже сначала дали RED. Fixture rollback
исправлен с предположения NULL на явный prior euru; денежный outcome failure
оставляет этот профиль и assignment без ложного commit.

`TestPurchaseOutcomeRollsBackAccessMetadata`, `TestPurchaseMissingHooksFailClosed`
и `TestPurchasePreparationRechecksAccount` проверяют final Tx rollback, отсутствие
native writes при отсутствующем любом purchase hook, повторную account guard
после panel read, отсутствие SQL Tx во время HTTP и отказ при потере той же
physical ownership session. Последний nil-outcome guard: actual RED yYWnyp,
GREEN sXfpzb; затем полная матрица повторена на указанной final product revision.

Старый purchase Docker-helper не сохранял native overlay, а legacy API по
умолчанию включён. Сохранённый runnable `prepare` проверяет итоговый Compose flag:
RED WsQ9in → native overlay исправлен → GREEN Q6w2XL. Prepare/restore/restart
сохраняют `compose.native.yml`; отдельный bot/reconcile процесс отсутствует.
Первая матрица core38763/helper0b72588 22/22 PASS сохранена исторически; она не
выдаётся за доказательство последующего nil-outcome fix.

## Доставка М04 и оставшиеся границы

М04b: PR #65, source d73c4f1c4d2078ab6dc7b63922ae7a1b6e2ec323, merge
26d4b96733881d89b0538e479c2facdca232f419, source-equal tree. Оба exact-source CI
SUCCESS; push-run37395227843 SUCCESS, preliminary
[2.0.0-dev.17](https://github.com/ekho/3xui-shop/releases/tag/2.0.0-dev.17).
Tag peeled to merge; все три GHCR images имеют linux/amd64 и linux/arm64 и OCI
source/version, проверенные по digest. #58 CLOSED/Project Done; это собственная
приёмка М04, не автоматическое закрытие С10.

Настоящие деньги YooMoney, URL уведомлений кошелька, production, установленный
Happ, VPN/trust macOS не проверялись и не менялись. Внешняя приёмка провайдера и
перенос старых платежей остаются отдельными С13/С47. Секреты, cookies, email,
подписи и ключи в публичные доказательства не включаются.

## Native rulings

Полный список решений из ledger в порядке принятия; стоимость ошибки сохранена.
- Ruling: preserve Native coordinator execution and one final Astra/high reviewer — accepted autonomous mandate and Native choice — cost if wrong: independent review arrives only at branch completion.
- Ruling: reuse the attached clean linked worktree and installed dependencies; baseline is the just-delivered source-equal v2 merge26d4 with exact-source CI/full local proof — avoid reinstall or repeating unchanged checks — cost if wrong: adaptation needs its own full validation, old evidence is not sufficient.
- Ruling: fresh integration branch from origin/v2 merges preserved afaeacf; remote PR5 source changes only by verified fast-forward after validation — preserves dirty old checkout and PR history — cost if wrong: unexpected remote movement blocks publication.
- Ruling: use existing account facades and VPN physical-session owner rather than new read interfaces — preserves nullable value comparisons and owner/data boundary — cost if wrong: root facades remain temporary until M05/M06.
- Task 1: Ruling: retain current platform facades and direct VPN worker registration; port only preserved purchase behavior into its current owner — four conflicts inspected, roadmap keeps current architectural history — cost if wrong: lost purchase reconcile/outcome is caught by existing recovery tests.
- Task 1: Ruling: rollback fixture must preserve a known prior profile, not assume NULL — first integrated run showed default regular already present; targeted diagnostic lS8B1Y confirms only the profile assumption failed — set prior euru and assert that literal after failed regular purchase — cost if wrong: an unchanged-profile fixture would not detect an accidental metadata commit.
- Task 2: Ruling: API file is verified JSON, so stdlib json compares semantics; actual migration sources replace the old driver nonexistent schema.sql path — no new YAML dependency or phantom check — cost if wrong: migration compatibility check must catch any real schema change.
- Task 2: Ruling: environment reference deploy/acceptance/README.md does not exist; use the actual deploy/acceptance/local.py and purchase README, already read — readiness is the observed owned resources, not a helper accepting a reference — cost if wrong: unsupported preflight would block native acceptance.
- Task 2: Ruling: current helper must keep native compose settings during payment restart/restore — preserved source predated M01 and omitted compose.native.yml — adapt the existing conditional, no new runner/dependency — cost if wrong: local acceptance could accidentally reopen compatibility bot API.
- Task 2: Ruling: fail closed for a missing Outcome before panel writes as well as before DB commit — Review Focus requires no paid access with missing hooks; the first nil-outcome test allowed one native add despite rollback — change the expected counter to zero, RED yYWnyp5177ms, one shared check guards every purchase write — cost if wrong: a miswired money outcome would leave a native effect without local fulfillment.
- Task 2: Ruling: final review/publication bullets are Finish gates after Native task completion, not prerequisites to dispatch the final reviewer — the original placement formed a cycle — task completion records implementation/local proof only; issue remains OPEN until independent review, exact-source CI, merge and preview proof — cost if wrong: premature delivery claim; C09 still separates the states.
- Task 2: Ruling: final task command validates the completed full matrix and unchanged product at the documentation revision, then regenerates/checks compatibility — full suite already ran once on the final product, avoid a duplicate merely for docs — cost if wrong: changed product requires fresh relevant/full verification and cannot pass the source-identity check.
