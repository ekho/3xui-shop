# М03 — владелец каталога тарифов

Дата: 2026-10-06. Задача: [М03 #57](https://github.com/ekho/3xui-shop/issues/57).
Решение: `2026-10-06-m03-catalogue-v1`, в границах принятого
[модульного монолита](2026-10-05-modular-monolith-design.md).
Документы и реализация ведутся автономно по разрешению владельца, Native.
Baseline: `c780927659f66b6ec8ae741cdddf310a7e464953` (`v2`, merge М02).

## Результат и граница

`internal/modules/catalogue` становится единственным владельцем текущих
предложений, неизменяемых revisions, валидации условий, коммерческих цен,
legacy import и явного unlimited seed. Поведение
[С09](2026-10-02-s09-catalogue-design.md) сохраняется. HTTP/React-admin,
назначение тарифов С07/С08 и CLI используют операции владельца.

Перенос не меняет HTTP/OpenAPI, web, миграции, таблицы, идентификаторы,
публичные JSON, money precision и правила исполнения подписки.
Новые зависимости, события, общий repository framework и пустые модули
не добавляются. Полный перенос subscriptions/vpn остаётся М04.

## Публичный внутренний контракт

`catalogue.New(pool *pgxpool.Pool, authority *accounts.Service,
now func() time.Time) *Service` использует публичные операции accounts:
`Lookup`, `RequireOperator`, `LockOperatorPair`. `nil` clock означает `time.Now`.
Один accounts owner и один catalogue owner создаются в `app.NewService`.
Совместимый `platform.NewService` для прежних тестов создаёт те же владельцы
и передаёт существующий тестовый clock через callback.

Типы `Terms`, `Price`, `PlanSnapshot`, `OperatorPlan`, `Result`,
`OperatorResult`, `CreateInput`, `RevisionInput`, `ArchiveInput` принадлежат
catalogue. Поля/JSON tags и порядок сериализации соответствуют прежним
wire-моделям, включая пустые массивы, nullable actor/legacy/reason, строковые
minor units и `changed_at`. В них нет Echo, wire, sqlc или pgtype.
Legacy package/result переносятся с прежним строгим JSON parser;
переходные aliases в platform сохраняют CLI и исходные тесты.

CRUD, import и seed сохраняют имена прежних методов. Безопасный `Error`
несёт status/code; переходный фасад переводит его и ошибки accounts
в прежний HTTP error. Фасад содержит только преобразование типов.

Реальные потребители условий используют:

- `CurrentPlanTx(ctx, tx pgx.Tx, id uuid.UUID) (CurrentPlan, error)` для
  назначения тарифов; та же caller transaction и прежний read semantics.
- `UnlimitedPlansTx(ctx, tx pgx.Tx) ([]CurrentPlan, error)` для unlimited grant;
  сохраняет все текущие unlimited rows, включая archived. Правила конкретного
  grant проверяет подписка как прежде.
- `LockCurrentPlan(ctx, tx pgx.Tx, id uuid.UUID) (CurrentPlan, error)` для
  подготовленного С10/PR #5: блокировка `FOR UPDATE OF p` живёт до завершения
  caller transaction. Это сохраняет защиту выбора цены от конкурентной правки.

`CurrentPlan` содержит ID, Revision, Archived, Hidden, Profile и Terms;
поля текущего индекса и JSON terms остаются различимыми. Отсутствие плана —
`ErrNotFound`. Эти методы не открываются HTTP и не подтверждают право покупки.
Подписка/платёж проверяют свою eligibility и фиксируют прежний снимок.

## Сохранение данных и конкурентных правил

SQL/sqlc закрыты в `catalogue/internal/{queries,store}`. Все production
чтения/записи catalogue tables вне владельца удаляются из глобального store.
Общая история schema и test-only fixtures — явные исключения.
Idempotency queries остаются приватными операциями каждого владельца с
прежними principal/operation/key; общий инфраструктурный перенос не входит М03.

Сохраняются account/role → idempotency → catalogue lock order,
advisory key `9060247201`, unique devices, last-visible guard,
optimistic revision, транзакционный replay и rollback import.
Глобальная блокировка каталога остаётся намеренной: частые записи потребуют
отдельной оценки per-plan locks и last-visible guard.

Прежние hash формируются из исходного запроса до сортировки/trim;
сохранённый result возвращается без второй revision. Для revise/archive
анонимный hash wrapper сохраняет поля `ID`, `Expected`, `Reason`, `Terms`.
Новые module DTO должны читать старые persisted records. Редактирование
тарифа не меняет historical revision или уже сохранённую access operation.

## Зависимые задачи

[М04 #58](https://github.com/ekho/3xui-shop/issues/58) использует эти ports при
выделении subscriptions/vpn. [С10 #17](https://github.com/ekho/3xui-shop/issues/17)
и [М05 #59](https://github.com/ekho/3xui-shop/issues/59) при интеграции
[PR #5](https://github.com/ekho/3xui-shop/pull/5) заменяют raw catalogue SQL
на `LockCurrentPlan`, сохраняя money/quote/job atomicity.
PR #5 остаётся на `afaeacf652964453ddd61883aa6da6a0d285783c`; его ранняя
приёмка не доказывает совместимость с новой module boundary.

## Приёмка

1. Прямой module API и прежние HTTP/CLI пути создают/читают/правят/архивируют
   предложения с прежними правами, точными ценами, period limits и unlimited seed.
2. Прежние persisted create/revise/archive records дают тот же JSON result;
   иной body, revoked/restricted actor и конкурентные изменения сохраняют отказы.
3. Назначение и unlimited используют owner ports, прежний snapshot/keys/expiry;
   caller transaction сохраняет lock до commit/rollback.
4. Boundary test с отрицательными fixtures запрещает SQL вне владельца и
   импорты чужой реализации; production composition использует обоих владельцев.
5. Go race/vet/generation, connected browser, Python/Playwright и локальный
   Docker проходят. 3X-UI **3.7.0**, TLS SMTP, simulated Telegram и физический
   рестарт проверяются прежним runbook. Happ и системное доверие не меняются.

Локальная приёмка, PR CI, preview release и production учитываются отдельно.
Слияние PR #62 без CI — отдельное явное решение владельца; оно не объявляет
CI М03 пройденным и не распространяется на production.
