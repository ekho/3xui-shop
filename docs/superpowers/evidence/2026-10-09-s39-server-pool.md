# С39 — локальная приёмка пула серверов

Контракт `2026-10-09-s39-server-pool-v1`, owner #41 / `vpn`.
База ветки: `77e7460d00c5e7ab2f45dbbd7cf1c1c057d0ffef` (`v2`).
Спецификация и план: `docs/superpowers/specs/2026-10-09-s39-server-pool-design.md`
и `docs/superpowers/plans/2026-10-09-s39-server-pool.md`.

## Подтверждённое поведение

- Реестр сохраняет ID/host, tombstone и проверенную HTTPS base. Старые ID/targets
  не переназначаются после изменения конфигурации, удаления или outage панели.
- Первая выдача выбирает минимальную загрузку `assigned + reserved` сначала ниже
  мягкого лимита, затем среди всего доступного пула. Резервация/операция атомарны;
  provider calls не удерживают общую SQL lock. Весь offline не расходует trial period.
- Trial, покупки, paid fulfillment, renewal/change-plan, операторские операции,
  профили, месячный reset, карточка и ключ используют конкретный сервер аккаунта.
  Прежние identity, actor/source, funding, idempotency и uncertain-write guards сохранены.
- Statistics/reminders получают отдельные snapshots. Сбой/чужая identity/unsupported
  bulk read оставляют unknown; здоровая панель сохраняет собственные подтверждённые
  факты. Нет настроенного сервера — report503, согласно прежнему `minItems: 1` API.
- Read-only subscription settings discovery проверяет приоритет subURI, fallback,
  disabled subscription, IPv6 и границы URL. Явная base не переписывается.
- Новый тест блокирует SQL lookup trial/access на четыре интервала watchdog.
  Оба пути были RED с настоящей pgx race; lookup до запуска Ping watchdog дал GREEN.

## Полные проверки перед финальным ревью

Эти результаты относятся к `662bd87cd5d9f2a11ef180eb8326f5c2e0296512`.
Повторная полная проверка исправлений ниже фиксируется отдельно в #41 и PR
на точной исходной версии; результаты до ревью не подменяют её.

| Проверка | Результат | SHA-256 полного private log |
| --- | --- | --- |
| Go `-race`, browser consumers, все 26 пакетов | 1699 tests/subtests, 601 top-level; 16 test packages, 10 packages без test files; 0 failed/skipped tests | `0fcd5cd16ae6800461744d018460b9e9992cbbd8f4006342c69c09e979e1d52e` |
| Native на двух реальных TLS 3X-UI 3.7.0 | 21 tests/subtests, 0 failed/skipped | `9ec9adb477e6db86e58d97d2793b9b9c1b705800a85b93dce3a8d7f18c8b70fb` |
| Отдельный restart/scheduler proof | Тот же operation/grant/key и panel readback; один event/mail enqueue после restart | `d089730093f1726ac977a7ffae3f4acc6cf0dc4b6e9093dab592d4e6ade26ca1` |
| Python connected suite | 112 passed | `7992901203b5b9543ba3ca5c3b38065856621a6b720b3cbff78569ebb0bec02a` |
| Browser E2E | 541 passed | `658ebbc0abd6c7428babfd57f891e006396e142c524a7b1918d8bdd532b567f8` |
| Fresh backend image, vet, HTTPS smoke и native up | PASS | `5e45a1c1184d0003c1341ae115356d4d51078fedbf19004c7456ae4e7896644f` |

Generate, typecheck, web build/runtime-config, Compose config, authored-name check,
CI YAML и `git diff --check` также PASS. Web/bot inputs после их успешных проверок
не менялись. Все runtime/test/build inputs сверены по 749-file manifest:
`9672bbcd172976bb3f28d71086f2e0d28c83364eee94a6bbe6defbd03104fac8`.
После старта Go изменился только CI timeout: local command уже использовал60m;
runtime/test/build hashes остались прежними. Platform job и Go step теперь ограничены60m.

Native использует два экземпляра одного pinned 3.7.0 image
`sha256:3b3131f1876e6bf35063a9ec4dd1c594e4525180bfc2e1c477dcc8a3c9550ca1`
с отдельными DB volumes. Fresh compiled one-process назначает клиентов на разные
панели, сохраняет business snapshot/keys после restart; отключение назначенной
secondary не переносит клиента на primary и не завершает HTTP process.
Локальный текущий backend image (ARM64):
`sha256:a7b5ba9d6c05fde9233ae3b589c5a1405a8fe374723df2c7ebb7c0b9cf9209a5`.

## Границы доказательства

Ранние неуспешные и прерванные runs сохранены как история, не считаются текущим PASS.
Исправлены report без реального сервера, old immutable-host observer fixture,
ожидание запрещённого downgrade backup и реальная watchdog/SQL race.
Down с сохранённой routing history блокируется; пустая migration history имеет
отдельный подтверждённый Down/Up. Новых dependencies/API DTOs нет.

Тестовые PostgreSQL/Redis, оба provider и Mailpit принадлежат локальному стенду;
SMTP проходит TLS, Bot API и платежи симулируются. Production, реальный Telegram,
внешний SMTP, реальные переводы и переключение живого Happ не проверялись.
С38 UI, С40 reconciliation и С45–С47 configuration/import/cutover здесь не закрываются.
Whole-branch review, exact-source CI, merge и prerelease подтверждаются отдельно в #41/PR.

## Один авторский проход после ревью

Свежий Astra/high reviewer сообщил три Important замечания, без Critical/Minor.
Все три воспроизведены новыми тестами до изменения реализации:

- Stars recurring cycle, resume и billing reminders использовали основной ID.
  Теперь они проверяют реальный назначенный сервер, оплаченный source и identity
  immutable target. Неизвестное и чужое назначение не подтверждают оплату.
- Регистрация второго сервера до первого sync могла скрыть или занять ID основной
  панели. Прежний сервер сохраняется под общей lock в той же caller transaction
  перед регистрацией; другой host с его ID вызывает conflict.
- Ранее допустимые текстовые `PANEL_ID` перестали разрешаться. Read-only fallback
  и bootstrap сохраняют их точно; дополнительная миграция36 снимает ограничения
  только с прежних данных, а новые ID/имена валидируются при регистрации.
  Применённая миграция35 не переписана; rollback с routing history блокируется.

Связанный regression/Stars/pool/backup прогон: **181 tests/subtests, PASS**, полный
`-race` JSON log без failed/skipped tests. Python112, generated contract, vet,
новый backend image, HTTPS smoke, повторные migrations и native up также PASS.
Полные Go/browser-consumer и native проверки повторяются на неизменных752
runtime/test/build inputs. Web/bot исходники совпадают с проверенным baseline;
541 browser tests и их прежние image/static proofs сохраняются.

Текущие результаты полной повторной проверки, ответы на замечания, точный SHA,
CI и доставка публикуются в [задаче #41](https://github.com/ekho/3xui-shop/issues/41)
и соответствующем PR. После С39 работа по следующему сценарию остановлена.
