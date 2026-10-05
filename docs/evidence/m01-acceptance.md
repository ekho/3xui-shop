# М01: локальная приёмка встроенного Telegram

Дата: 2026-10-05. Ветка: `feature/m01-embedded-telegram`, baseline
`157fb918c360a4c0f4f924dee34a38574a7d032f` (`v2`).
Задача: [М01 #55](https://github.com/ekho/3xui-shop/issues/55).
Архитектурное решение: `2026-10-05-modular-monolith-v1`.
Проверки ниже относятся к реализации М01; финальная ревизия и результаты CI
фиксируются в PR. Merge, prerelease и production в эту приёмку не входят.

Один Go backend запускает HTTP, River, scheduler и Telegram. Новый модуль
не импортирует platform/store/wire/httpapi/app. Временный app bridge сохраняет
существующие транзакции и схему. Legacy bot/API ещё нужны неперенесённым
сценариям; их удаление входит в С47. М02–М06 остаются отдельными задачами.

## Проверки

Исполняемые проверки:
[native integration](../../backend/tests/native_trial_integration_test.go),
[границы импортов](../../backend/internal/app/boundaries_test.go),
[approval](../../backend/internal/modules/telegram/approval_test.go),
[runtime](../../backend/internal/modules/telegram/runtime_test.go),
[Bot API](../../backend/internal/modules/telegram/internal/botapi/client_test.go),
[lifecycle](../../backend/internal/app/lifecycle_test.go).
Команды окружения — в [runbook](../runbooks/m01-embedded-telegram.md).

| Проверка | Результат и предел доказательства |
| --- | --- |
| `go test -race ./... -count=1` | PASS: 9 пакетов с тестами, 2 без тестов; 169.470s. PostgreSQL/Redis — собственные Docker, Telegram/панель/SMTP — локальные fixtures. |
| `go vet ./...`, Go/web code generation | PASS; generated drift отсутствует. HTTP/OpenAPI/схема не менялись. |
| Web typecheck/build, runtime config | PASS; значения юридических документов/поддержки остаются конфигурацией запуска. |
| Python unittest | PASS: 105 тестов, включая настоящий legacy consumer. Это regression прежнего пути, а не доказательство native transport. |
| Playwright | PASS: 110 isolated проверок; отдельный connected `TestWebTrialFlowAndFailures` с настоящим HTTP/SMTP/Python adapter тоже PASS. |
| Native Compose parser и compiled backend/web images | PASS; native profile запрещает постоянно работающие bot/reconcile контейнеры. |
| Go native integration с Docker SMTP/3X-UI | PASS: регистрация, TLS/auth письмо, login, две карточки, конкурирующие решения/replay, отказ, пересмотр, reconcile, actor/audit, owner-only/no-store key, outage/disabled Telegram, реконструкция lifecycle. Bot API simulated. |
| Физическая остановка compiled backend после commit | PASS: публичное web-решение, reserved Grant и scheduled job, остановка процесса, запуск с той же DB, исходная Operation applied; один Grant/Operation, прежние ключи, настоящий panel readback/expiry/limits/membership, owner session. Telegram в этой проверке выключен. |
| Container smoke прежнего transport | PASS: HTTPS, runtime config, private/public routing, secret files, repeated migrations, rollback и provision-only restore runtime; 21.337s. |
| Свежее whole-branch review | Ожидает завершения проверки перед PR. |

Telegram actor/allowlist/private chat проверяются до действий. Callback ID
и решения остаются устойчивыми в БД. Offset продвигается после завершённого
или безопасно отклонённого действия. Просроченный/чужой lease не подтверждается;
неоднозначный send не считается успешным. 429 соблюдает retry_after;
401/409/webhook/unsupported payment останавливают канал с безопасным кодом.
HTTP и принятые jobs продолжаются. Drain и отмена poll/send проверены.

Native Go test восстанавливает сервисы в одном test process; физический
stop/start отдельно проверен с Telegram disabled. Это не проверка живого
Telegram при рестарте ОС-процесса. Неопределённая panel failure проверена
существующими provisioning tests; native reconcile дополнительно использует
контролируемый `needs_review` checkpoint исходной Operation.

Стенд использует pinned 3X-UI **3.7.0**, TLS Mailpit и собственные базы.
Native preflight подтвердил: duplicate panel key/UUID может приниматься,
attach сохраняет параметры. `PANEL_DUPLICATE_GUARD_VERIFIED=false`;
неоднозначный create автоматически не повторяется.

## Решения исполнителя

| Решение | Причина | Цена ошибки |
| --- | --- | --- |
| Отдельный worktree от `v2` | Сохраняет открытый PR #5/С10 и его ревизию. | Дополнительный checkout. |
| Offset test перенесён из transport в runtime | Acknowledgement происходит в polling loop; второй fake loop не нужен. | Ошибка границы потребует перемещения теста. |
| Bridge fixture делает delivery доступной по DB clock | Убирает субсекундную разницу host/DB clock; production lease rules сохранены. | Может скрыть отличие timing fixture от production; expiry отдельно проверен. |
| Task commits накоплены для одного code push | Shared docs опубликованы раньше для ссылок GitHub; сокращает повторные CI. | Удалённый code backup появляется в конце этапа. |
| Native integration использует existing backend/tests fixtures | Сохраняет один HTTP/SMTP/panel harness. | При дальнейшем расхождении понадобятся отдельные fixtures. |
| Реконструкция Go lifecycle + отдельный compiled stop/start | Покрывает replay и физический рестарт без production test endpoint. | Живой Telegram stop/start требует дополнительного тестового бота. |
| Reconcile seeded на исходном target | Ошибки provisioning уже проверяются отдельно; дополнительный fault proxy не нужен. | Неисследованная vendor failure остаётся внешней проверкой. |

При первой проверке обнаружена гонка только в restart fixture: scheduler читал
заменяемый service pointer. Исправлены capture и join; focused restart и полный
race run прошли. Docker fixture исправлен по прежнему OpenAPI: `reason` обязателен
даже при пустой причине approve. Ошибки локального cwd/Poetry env исправлены
в командах запуска, продукт/зависимости для них не менялись.

## Доставка и внешние проверки

GitHub v2 синхронизирован: 55 задач, 48 сценариев, 191 native dependency;
старые Done и открытый PR #5 сохранены. М01 остаётся открытым до review/merge.
Push/PR/CI ещё ожидаются; слияние автоматически не разрешено.

Реальный Telegram token не использован в этой приёмке. Внешние SMTP,
целевой password benchmark, реальные payment provider/production checks
остаются открыты. Happ, системное доверие и действующее VPN-подключение
не менялись. Новые зависимости, миграции, пустые будущие модули и event bus
не добавлены.
