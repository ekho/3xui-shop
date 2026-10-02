# С06: локальная приёмка операторского кабинета

Frozen API и совместимость payload: `1381d2e`. Backend и React-admin реализуются;
общий статус **Pending**, ниже зафиксирован полный объём приёмки.

| AC | Требование | Необходимое доказательство | Статус |
| --- | --- | --- | --- |
| 1 | Прямой HTTP/forged actor без роли отказал; revoke/restricted закрывают действия | Real-PG role races + actual browser/HTTP/CLI | Pending |
| 2 | Search/page/card web/TG-only/states без fake identities/financial data/secrets | Typed SQL/schema cases + actual list/card/history | Pending |
| 3 | Web UUID actor approve/reject/reconsider/reconcile, immutable reject/idempotency | Service/HTTP tests + actual audit rows | Pending |
| 4 | Bot/web concurrency/lost response дают одну неизменную выдачу | Real-PG race, count Grant/Operation/job, native identity | Pending |
| 5 | TG-only NULL credentials, общий worker; duplicate/used/disabled/uncertain protected | Migration/auth/service/HTTP tests + native3.7.0 | Pending |
| 6 | cfg.Operators=[] и adapter stopped не мешают web request/approve/provision/key | Собственный локальный runtime с web ролью | Pending |
| 7 | Operator support text/file/state/ban/receipt вместе с client UI, VPN не меняется | Actual two-actor browser/API, native target/key до/после | Pending |
| 8 | PG restore сохраняет новые данные/роли/actors/identity; session revoke, accessibility/regression/review | Actual dump/restore, full checks, один fresh whole-branch review | Pending |

Предварительно проверены authored9 paths/16 DTO, nullable-email wire contract,
existing client typecheck и12 bot-adapter tests. Эти проверки доказывают только
контракт/совместимость, не готовность операторского runtime.

Роль выдаёт CLI по UUID из private file, саморегистрация её не создаёт.
Telegram IDs новых DTO — decimal string; старые identity/creation timestamps
не подменяются фиктивными значениями. Внешний cutover legacy Telegram клиентов
требует С46 и согласованного прекращения старого writer; production/Happ не входят.
