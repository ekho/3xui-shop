# С06: локальная приёмка операторского кабинета

Frozen API и совместимость payload: `1381d2e`; backend `70a0294`, UI `5c21d94`.
Actual native/browser/restore run на `d6ae035` завершён: 30 PASS, 0 FAIL/BLOCKED,
exit0. Общий regression завершён. Окончательный статус **Pending fresh review**;
он остаётся последней непроверенной частью AC8. Команды/ревизии — в
[сводном evidence](s03-s06-progress.md#итоговые-проверки-перед-review).

| AC | Требование | Необходимое доказательство | Статус |
| --- | --- | --- | --- |
| 1 | Прямой HTTP/forged actor без роли отказал; revoke/restricted закрывают действия | `TestOperatorHTTPAuthorityAndActions`, `TestOperatorRevocationAndRestrictionWinQueuedDecision`; actual customer403, forged400, CLI revoke403, restricted403/key clear/logout401 | PASS; write races — real-PG |
| 2 | Search/page/card web/TG-only/states без fake identities/financial data/secrets | `TestOperatorSearchHistoryAndKeyDenial`, `TestOperatorHistoryStablePages`, migration unknown created_at; actual RU/EN body-only search/page1/2/card, true TG-only/NULL credentials; browser none/finite/stale/limits | PASS; history>50 и части states — fixtures |
| 3 | Web UUID actor approve/reject/reconsider/reconcile, immutable reject/idempotency | Actual reject/card required-null TG actor; immutable409, linked reconsider/reason, approve/replay200; controlled needs_review→web reconcile applied с audit UUID | PASS; failure fixture удалён до reconcile |
| 4 | Bot/web concurrency/lost response дают одну неизменную выдачу | `TestOperatorConcurrentBotWebDecisionOneGrant`; actual replay и одна Grant/job, native UUID/sub/target readback; browser lost response сохраняет idempotency | PASS; bot/web race — real-PG, транспорт бота остановлен |
| 5 | TG-only NULL credentials, общий worker; duplicate/used/disabled/uncertain protected | `TestOperatorTelegramOriginHasNoWebCredentials`, `TestOperatorTelegramProvisionAndUncertainty`, migration/auth tests; actual true owner ID, worker applied, duplicate409, one Grant/native identity | PASS; ID не публикуется, нет fake web credentials |
| 6 | cfg.Operators=[] и adapter stopped не мешают web request/approve/provision/key | Actual web-only request/decision/shared worker/key200 при пустом BOT_OPERATOR_IDS и остановленном adapter; postflight подтвердил режим | PASS, own native runtime |
| 7 | Operator support text/file/state/ban/receipt вместе с client UI, VPN не меняется | Actual два клиента/operator, byte-exact download, page50, receipts/state/ban, native target/key/VPN сохранены | PASS; [С05](s05-acceptance.md) |
| 8 | PG restore сохраняет новые данные/роли/actors/identity; session revoke, accessibility/regression/review | Actual dump/restore digests равны, old session401, fresh login/key/file200; full Go-race, web59, Python103; actual RU mobile/Enter | Runtime/regression PASS; свежий whole-branch review Pending |

Authored9 paths/16 DTO, nullable-email wire contract и12 bot-adapter tests
дополнены полным regression и actual runtime. Секреты, личные ID, тела сообщений,
cookies и subscription URLs не копируются в публичное evidence. Полные
redacted records хранятся локально в `s06-native-final-acceptance.md` и
`s06-native-final-rows.jsonl` внутри `.superpowers/sdd/2026-10-02-s03-s06/`.

Роль выдаёт CLI по UUID из private file, саморегистрация её не создаёт.
Telegram IDs новых DTO — decimal string; старые identity/creation timestamps
не подменяются фиктивными значениями. Внешний cutover legacy Telegram клиентов
требует С46 и согласованного прекращения старого writer; production/Happ не входят.
