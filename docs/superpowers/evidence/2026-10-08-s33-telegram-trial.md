# С33.Р4 — обычный Telegram-триал

Реализация и локальная приёмка завершены; независимое ревью и доставка учитываются отдельно.
Владелец #31, контракт 2026-10-08-s33-telegram-trial-v1.
База 71bc325bf1153f1a1d40b2ff9d8f6749038db29b; один Go-процесс HTTP/River/Telegram.

## Проверено

| Проверка | Результат |
| --- | --- |
| Полный Go/race, TestKit, native Docker и real browser | 1077 тестов / 13 пакетов PASS, 0 FAIL / 0 actual SKIP |
| Полный Playwright | 372 PASS |
| Полный Python с реальным Go-потребителем старого adapter | 110 PASS |
| go vet | PASS |
| Генерация Go/SQL/TS | Все 39 действительных выходных файлов идентичны до/после генераторов |
| Native автоматический trial и восстановление | 2.38 s PASS |
| Native signed Mini App в браузере | 10.27 s PASS |

Новый Telegram-origin account принимает оба документа, получает capability и
активирует триал без оператора. Cookie и подписанный Mini bearer используют
тот же owner, account lock, единственный grant, захваченные лимиты и прежний
River/VPN pipeline. Повтор ключа возвращает тот же request/operation; новый
ключ после выдачи отказан. Проверены rollback вставки задания и повтор после
rollback, конкурентные попытки, restricted/banned/disabled/used/legacy/source,
строгий JSON/Origin/CSRF/keys и старый ручной web-путь.

Native собирает текущие исходники Go против настоящей локальной TLS
3X-UI 3.7.0 и owned PostgreSQL/Redis. Telegram runtime выключен. После постановки
выдачи останавливается весь graph, новая сборка graph повторяет тот же request.
Контролируемая ошибка commit после фактической записи panel оставляет
needs_review; reconcile применяет тот же operation/target. Сохранены VPN UUID,
sub_id, panel_key; один grant, request и operation, ноль approval cards.

Браузер открывает существующий mini-app.html, принимает документы, активирует
клавиатурой, ожидает реальную выдачу, явно показывает и скрывает подключение.
Все API настоящие; только Telegram SDK и внешняя подпись используют собственные
fixtures. Полные mock-browser проверки покрывают ru/en, disabled busy,
потерянный ответ с сохранением mode/key, старый manual UI и needs_review/support.

## RED и исправления

- DB тест действительно отверг отсутствующую automatic provenance до миграции;
  domain API отсутствовал. После T1 реальная БД проверяет отсутствие фиктивного
  оператора, actor constraints и безопасный отказ Down после auto-фактов.
- HTTP тесты получили отсутствие capability/404 до T2. Старые web DTO не
  изменены; добавлены только optional trial_mode и отдельный endpoint.
- Четыре browser CTA проверки получили RED до T3, две прежние проверки PASS.
  Первый GREEN выявил ошибочный русский текст в тесте; он заменён на реально
  существующий provisioning status, без изменения продукта.
- Native integration после T1–T3 сразу GREEN для backend/restart/reconcile.
  Первый browser timeout локализован safe-checkpoint до consent без session
  request: fixture отдавал index.html с запрещающим Telegram SDK CSP.
  Центральный fixture теперь выбирает существующий mini-app.html для всех
  Mini routes. Production CSP и настройки доверия Mac не изменены.
- Первый full Python не передал TestKit env вложенному Go: prerequisite
  остановил его до БД. Правильное file-only окружение дало 110 PASS.
- Первый full Go выявил два падения strict SELECT*/RowToStructByName в старом
  общем test-row: отсутствовал decision_source. Добавлено поле и проверка
  operator в обоих потребителях; настоящий RED→race GREEN5.433s. Domain и DB
  constraints не ослаблялись.
- Первая generation fingerprint ошибочно выбрала SQL-input directories;
  исправленная проверка всех 39 выходных файлов доказала их детерминизм.

## Rulings

1. Initial web policy uses immutable SourceKind, TG credentials do not erase original source — agreed account contract — wrong source would misclassify trial eligibility.

2. LegacyUserID stays on existing manual path until trial-used import #53 — partial imports lack that fact — legitimate legacy user cannot auto-activate yet.

3. Ordinary limits ignore source metadata until referred owner #52 — no referral registry/validated relation exists yet and legacy referred flag defaults false — enabled legacy benefit is not migrated until R7/config/import owners.

4. Optional capability only emitted for automatic mode — old web response shape remains intact — clients unaware of mode retain manual UI until upgraded.

5. Native inline, one final Astra/high review — explicit accepted executor and runtime delegation restriction — author has no fresh per-task reviewer.

6. task-done verification precedes the commit to avoid committing a failing check — post-run exact source is recorded here — the helper initial commit range alone omits the just-tested uncommitted diff.

7. register telegram-trial.spec.ts in the existing explicit Playwright testMatch — otherwise the planned runnable test is undiscoverable — missing registration would silently omit coverage.

8. native tests use TestNativeTrialTelegramActivation prefix — existing local.py/CI selector is TestNativeTrial — another prefix would silently omit the actual Docker boundary. Add real.spec.ts Telegram-only grep scenario using its existing safe reporter and file-only fixture.

9. fix the central test-fixture entry selection for all Mini routes — actual shipped entries already differ in CSP — leaving it wrong would block real Mini acceptance; production CSP remains intact.

10. Safe reporter prints only static milestone/status on failure — keep proof/cookie/VPN secrets hidden — richer failure details still require bounded private diagnosis.

11. full Python inherits file-only TestKit environment for its real Go consumer — absent parent env failed prerequisite before DB access — omitting it leaves cross-language boundary unverified. Corrected command only, no product change.

12. deterministic generation fingerprints all actual internal/store outputs plus OpenAPI Go/TS — first fingerprint selected query inputs and captured only2 files — omitting store outputs could miss generated SQL drift. Corrected39-file fingerprint is identical before/after generators.

13. extend the shared owned test row rather than use lax mapping or weaken DB constraints — new provenance column is part of the persisted contract — ignoring it would hide incorrect manual actor provenance.

14. Task4 helper completion denotes implementation/local verification only; final review and delivery retain explicit pending plan gates — Native final review follows task completion by design — conflating those states could close #31 before reviewed merge/image receipts.

## Границы приёмки

Реальных Telegram/BotFather, денег, публичного webhook, внешнего SMTP и
production эта приёмка не проверяет. Live Happ/VPN и доверие Mac не менялись.
Старые Docker backend/gateway не выдаются за текущую реализацию: native тест
собирает весь Go graph из проверенных исходников против Docker panel.

LegacyUserID остаётся на ручном пути до импорта trial-used в С46.
Реферальная льгота остаётся #52/Р7; enabled legacy referred policy не объявлена
перенесённой. Stars, recurring и external Telegram payments остаются С34–С36.
Автоматический decision_source не подменяет operator, исходный SourceKind
сохраняет политику после привязки email/Telegram.

Rollback приложения сохраняет schema/grants; Down после auto-фактов запрещён.
Private proofs, URL-file credentials, Mini sessions, cookies и VPN links в Git
не включены. Delivery Done требует отдельного exact-source CI/manual merge и
проверенных preview tag/OCI/configs; local PASS их не доказывает.

