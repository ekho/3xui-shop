# С47: поэтапное переключение и rollback

Задача [#54](https://github.com/ekho/3xui-shop/issues/54), контракт
`2026-10-10-cutover-v2`. Это порядок для разрешённого окружения и локальной
репетиции. Production требует отдельного разрешения на конкретное переключение.

## Владелец и условия

После передачи один `server serve` исполняет HTTP, River, main/support Telegram,
schedulers и записи в 3X-UI. Web/Caddy отдельны. `serve` и `reconcile` используют
одну session advisory lock PostgreSQL: конкурент отказывает до побочных эффектов,
потеря lock-соединения завершает runtime, graceful shutdown удерживает владение
до завершения исполнителей. Новый владелец выдерживает три секунды с мониторингом
сессии до запуска эффектов. Эта локальная задержка не ограждает зависший/SIGSTOP
процесс или сетевую изоляцию: оператор обязан подтвердить остановку прежнего
процесса и его restart supervisor перед takeover. Lock также не останавливает
старые Python/external writers.

До окна обслуживания зафиксируйте разрешённый project, source/target DB, image
revision/digests, panel/server identity, main/support bot IDs, поддержку и provider
routes. Храните deployment/env/secrets/certs вне Git с доступом runtime UID;
не выводите URI, IDs плательщика, payloads, VPN keys или `docker compose config`
в журнал. Образы/CLI rollback должны поддерживать нынешнюю schema и late receipts;
не назначайте старый image совместимым только по успешному healthcheck.

[С45](../../docs/runbooks/s45-product-configuration.md) владеет настройками,
[С46](../data-migration/README.md) — source packet, [С43](../../docs/runbooks/s43-backup-restore.md)
— backup/restore, [С44](../../docs/runbooks/s44-process-operations.md) — lifecycle.

| Этап | Активный владелец | Проверяемое условие перед продолжением |
| --- | --- | --- |
| Подготовка | Единственный текущий runtime | Версия/ресурсы подтверждены; compatible recovery artifact и private configuration сохранены; пробное restore успешно |
| Обслуживание | Текущий runtime заканчивает уже принятые операции | Persisted maintenance закрывает новые команды; callbacks остаются доступными; известны pending/running и неоднозначные panel writes |
| Остановка старого | Никто не пишет SQLite/панель и не polling Telegram | Старые main/support, recurring/reminders/reset/audit schedulers и их restart supervisors остановлены; активные операции закончены либо зафиксированы для сверки |
| Snapshot/import | Go offline exporter/importer | Законченный SQLite snapshot private/read-only; dry-run без записей; source digest неизменен; operator проверен; apply одной транзакцией |
| Запуск Go | Один `serve` | Сохраняются credentials/merchant/bot/server identity, old callback paths и public subscription origin; второй исполнитель отказал; `/readyz` и module states согласованы |
| Проверка перед открытием | Go под persisted maintenance | Источник и текущие деньги/access/jobs сохранены; panel readback совпал без перевыдачи keys; выполнены own callback/replay и актуальный backup/restore |
| Открытие | Тот же Go runtime | Явно выключено business maintenance с current revision; подтверждён разрешённый пользовательский сценарий и audit |

`CABINET_MAINTENANCE=true` дополнительно закрывает gateway UI/API. Это не
заменяет persisted maintenance, drain или остановку writers. Он пропускает
**точные** paths `/webhooks/yoomoney`, `/webhooks/yookassa`, `/webhooks/cryptomus`,
`/webhooks/heleket` и прежние `/yoomoney`, `/yookassa`, `/cryptomus`, `/heleket` к
тем же Go handlers. Их signature/provider verification остаётся обязательной.
Во время остановки backend gateway возвращает ошибку, а не подтверждает событие;
после запуска нужны сверка и повторная доставка провайдера. Для провайдера без
доказанного retry/reconciliation нельзя считать такое окно принятым.

## Совместимость и деньги

Импорт сохраняет legacy/Telegram IDs, VPN UUID, subId, panel key, server IDs,
registration time, source links, payment IDs и recurring charges. Он не меняет
3X-UI/Xray и не выпускает keys. Legacy Telegram `subscription:...` decoder и
source-history links остаются в Go. Подписанные native events обслуживаются своим
payment flow; archived quote/status сам по себе не доказывает money/entitlement.

Подтверждённые поздние YooMoney/YooKassa/Cryptomus/Heleket и Stars paid/recurring/
refunded события сохраняются в `legacy_payment_receipts` (migration **00044**).
Provider/Bot API authentication и verified amount/currency/IDs определяются
реальным контрактом; неподписанные суммы не получают trusted meaning. Journal
не создаёт native order, access, bonus или job. Повтор proof идемпотентен;
противоречие оставляет первый proof и переводит запись в `conflict`.
Сопоставление с импортом — только по точному исходному payment ID.

YooKassa `refund.succeeded` проверяет отдельный refund и исходный payment через
authenticated API; `refunded` хранится по refund ID. Накопительное
`payment.refunded_amount` — отдельный `refund_observed` по payment ID. Это разные
виды наблюдения, их суммы нельзя складывать как два возврата. Legacy Stars
сохраняет фактически подтверждённую Telegram сумму, даже если old invoice quote
отличается; такая запись остаётся на сверку и не выдаёт доступ.

Offline operator report читает role-guarded публичный порт payments без HTTP,
Redis, poller или worker:

```sh
umask 077
DATABASE_URL_FILE=/absolute/private/database-url \
  server legacy-payments --operator-file /absolute/private/operator \
  > /absolute/private/late-receipts.json
```

Проверьте exit code. Report содержит opaque journal ID, provider/kind,
known amount/currency, state и nullable account ID; raw provider ID/reference/
proof отсутствуют. `review/conflict` требует сверки у разрешённого провайдера и
действующего operator payment/access сценария. Нельзя автоматически назначить
старый платёж новому order или повторно выдать доступ из packed quote.

## Rollback с текущими данными

1. До остановки проверьте **конкретный разрешённый checkpoint**, а не произвольную
   старую версию. Выполните текущий и кандидатный `server cutover-check` с тем же
   private `DATABASE_URL_FILE`; команда только читает consistent schema snapshot
   в read-only transaction и не запускает HTTP/Redis/workers. Оба exit code должны
   быть нулевыми; `source_revision` должен совпасть с выбранным проверенным commit,
   `legacy_receipts_version` быть `1`, а `schema` — совпасть полностью. Зафиксируйте
   SHA256 binary либо immutable image digest вместе с commit. Без успешной проверки
   отклоните кандидата **до остановки текущего процесса**.
2. Снова закройте business admission, оставляя money callbacks принимаемыми до
   остановки. Зафиксируйте current payment/order/access/job states без secrets.
3. Остановите `serve` с SIGTERM и дождитесь завершения pollers/workers/schedulers
   и освобождения владения. При timeout или непроверенном panel result не открывайте
   второй writer; следующий шаг — bounded reconcile текущей операции.
4. Для code rollback запустите **проверенный совместимый Go image** с прежними
   credentials/origin и **той же актуальной PostgreSQL**. Не запускайте Python,
   не применяйте старый SQLite snapshot поверх target и не выполняйте schema down.
5. Для DB recovery используйте С43 backup актуальных данных в **новой БД** с
   NOSUPERUSER CREATEDB restore-role. Остановите writers на время snapshot/switch;
   проверьте всю schema/counts/sequences/attachments и текущие financial facts.
   События после точки backup должны быть сохранены и сверены/replayed до открытия;
   snapshot сам по себе не является восстановлением более поздних денег.
6. Запускайте только `reconcile`, если требуется provision-only recovery, либо
   только `serve` после завершённой сверки. Проверяйте retained keys/IDs, pending и
   late receipts, source replay, duplicate callbacks и отсутствие второго grant.
7. Открывайте admission только после согласованной сверки. При отсутствии
   compatible artifact или проверенных данных оставайтесь в maintenance.

`e53746c4a13d4209438012a33390ec84eb38539c` **отклоняется**: он ACKs старые деньги
без late journal и не поддерживает `cutover-check`. Более ранняя новая совместимая
ревизия указана в [checkpoint.txt](checkpoint.txt). Сборка Docker должна получить
`--build-arg SOURCE_REVISION=<exact-archive-commit>` из проверенного source archive;
CI preview передаёт SHA своего checkout. Пустой/некорректный revision, native dirty
VCS build или неподдерживаемая schema отклоняются. Image без такой provenance
может быть disposable fixture, но не согласованным rollback target.

Manifest связывает проверку с выбранным artifact и миграциями. Schema fingerprint
описывает текущую БД и сам по себе не доказывает отсутствие drift или безопасность
произвольной старой business logic; нужен успешно проверенный checkpoint-сценарий.

Down 00044 блокируется при retained receipts; guards 00042/00043 сохраняют
source history и старые identities. Повтор `import-legacy` с тем же source/digest
не перезаписывает новые accounts/orders/receipts/grants/rewards/jobs; иной source
под прежним именем отклоняется. Исходный SQLite хранится как private provenance,
а не как authoritative DB после cutover.

## Репетиция и удалённые потребители

[Local acceptance](../../docs/runbooks/s01-test-rollout.md) и Platform CI сохраняют
полные Go race/static/generated/security, browser, native 3X-UI/TLS SMTP,
server/group reconciliation и backup scopes. `TestRuntimeOwnerTwoProcessesServeReconcileAndRestart` проверяет
реальные конкурирующие процессы, SIGTERM takeover и потерю lock-сессии.
`TestPopulatedLegacyMigrationPreservesNativeFacts` проходит export/import,
late callback под maintenance, replay/restart и populated backup/restore полного
public-table digest. `TestCutoverArtifactRollback` собирает два действительных Git
commit с разными binary SHA256, проверяет preflight, отвергает исходный `e53746c`
при продолжающем работать текущем PID, затем SIGTERM/wait и запуск checkpoint.
После смены процесса он повторяет прежний и принимает новый подписанный callback,
проверяя весь retained account/order/receipt/access/source state и первый proof.
У двух проверенных commit одинаковая business implementation: это доказательство
смены артефакта, а не совместимости старой платёжной логики. PSP и Bot API fixtures
синтетические, внешних платежей нет.

[Матрица потребителей](../../docs/superpowers/specs/2026-10-10-s47-cutover-rollback-design.md)
связывает старые runtime/adapter/config/deps/scripts/images/tests с Go-владельцами.
Небольшие stdlib exporters approvals/payments/campaigns сохранены как независимые
производители исторических **частичных** пакетов. Docker panel traffic fixture
использует test-only Go binary в backend image с отключённой сетью. Ни один
оставшийся acceptance tool не импортирует `app`, aiogram, SQLAlchemy или py3xui.

Исторический `backfill_panel_limit_ip.py` удалён как разовая legacy repair utility;
он не выполняется при переносе. Импорт сохраняет panel limits. Непроверенный
packed quote не разрешает автоматическое изменение существующего entitlement.
Операторские текущие access operations применяются только к доказанным условиям.
