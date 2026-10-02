# Имена приложения и совместимость переименования

Решение v1 от 2026-10-02: пользователь потребовал оставить номера сценариев
только в документации. Реализация и проверка выполняются автономно в текущей
ветке. Перенос не меняет API, правила доступа и сохранённые идентификаторы клиентов.

| Прежнее имя | Семантическое имя |
| --- | --- |
| `backend/internal/s01` | `backend/internal/platform` |
| `deploy/s01` | `deploy/acceptance` |
| `deploy/s02`, `deploy/s04`, `deploy/s06` | `deploy/account-security`, `deploy/device-connection`, `deploy/operator-cabinet` |
| `deploy/s07`, `deploy/s08`, `deploy/s09`, `deploy/s48` | `deploy/subscription-operations`, `deploy/access-profiles`, `deploy/catalogue`, `deploy/account-restrictions` |
| integration tests С01/С02 | `web_trial_integration_test.go`, `account_security_integration_test.go` |
| web tests С01–С09/С48 | `web-trial`, `account-security`, `subscription-profile`, `device-connection`, `support`, `operator-cabinet`, `subscription-operations`, `access-profiles`, `catalogue`, `account-restrictions` |
| `.github/workflows/s01-checks.yml` | `.github/workflows/platform-checks.yml` |
| River `s01_mail`, `s01_provision`, `s07_access`, `s41_monthly_reset` | `mail_delivery`, `trial_provision`, `access_operation`, `monthly_traffic_reset` |

Общие env: `TEST_DATABASE_URL_FILE`, `TEST_REDIS_URL_FILE`, `TRIAL_CONTRACT_*`,
`RUN_BROWSER_TESTS`, `E2E_MODE`, `TEST_ORIGIN`, `TEST_MAIL_FILE`, `SCREENSHOT_DIR`.
Env стенда: `APP_RUNTIME_UID/GID`, `APP_NETWORK_SUBNET`, `APP_GATEWAY_IP`,
`LOCAL_STATE_DIR`, `ACCEPTANCE_RUNTIME_MANIFEST`, `FAULT_*`, `RESTART_PROOF`,
`PROBE_CONFIG`. Фиксированные тестовые DB/role — `platform_test`;
runtime namespace Redis — `platform`. Новые state/evidence folders именуются
по назначению. API title — `Cabinet API`.

Перед запуском новых workers миграция классифицирует существующие River jobs
по JSON args и связанным domain rows. Она сохраняет job ID, состояние,
аргументы, попытки и историю; неоднозначный или неизвестный job блокирует
миграцию. Новая БД без River table обрабатывается отдельно. Queues, ключи
идемпотентности, advisory locks, криптографические ключи/AAD и panel identity
сохраняются. Проверка охватывает существующие задания и их выполнение новым worker.

Root переводит принадлежащий этой задаче локальный стенд в окне обслуживания:
останавливает writers, сохраняет backup и throttling TTL, обновляет env и images,
применяет миграцию и проверяет API, историю очереди, native identity и VPN.
Имена уже созданных Docker/DB ресурсов остаются историческими data identities;
они читаются из private runtime metadata, а не зашиваются в application source.
Секреты, клиентские идентификаторы и сертификаты не перевыпускаются.
Тестовый admin DB/role создаётся отдельно с точной защитной проверкой testkit.

Накопленные JSONL, screenshots, manifests и старые exact command receipts
остаются неизменными. Их прежние пути и команды — историческая документация,
не текущий способ запуска. Актуальные runbooks/ссылки указывают новые файлы.
Функциональные доказательства С48/С09/С07/С08 до переименования сохраняют силу
для прежних ревизий. После изменения повторяются source checks и узкая проверка
совместимости runtime; одноразовые native сценарии на старых fixtures не повторяются.

Приёмка: в authored source, tests, scripts, configs, identifiers и путях
приложения нет номеров сценариев; зависимости и непрозрачные checksum/spec bytes
не переписываются по случайному совпадению текста. Проверяются новые команды,
generated outputs, сохранённые jobs/история, restore и работа локального стенда.
