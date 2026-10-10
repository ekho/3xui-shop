# С45: локальная приёмка конфигурации продукта

Дата: 2026-10-10. Задача: [#47](https://github.com/ekho/3xui-shop/issues/47).
База `ad66cfeb8bf89d8259f88ff0a8f80d0011c022b0`, ветка
`feature/s45-product-config`, контракт `2026-10-10-s45-product-config-v1`.
[Spec](../superpowers/specs/2026-10-10-s45-product-config-design.md),
[plan](../superpowers/plans/2026-10-10-s45-product-config.md),
[inventory и процедура](../runbooks/s45-product-configuration.md).
Точный source HEAD, полный CI и merge фиксируются в PR и итоговом issue comment.
Миграций, settings DB/API и production-переноса нет.

## Среда и границы

Собственный worktree, PostgreSQL/Redis из `backup_restore.py up` с UUID-project,
свободной подсетью и динамическими loopback-портами. Оба guarded fixture inputs
указывают на его PostgreSQL. Go testkit создаёт отдельные UUID-базы.
Smoke использует отдельный UUID Compose project, проверяет свободную `/28`
подсеть и выделяет свой loopback HTTPS port; чужие ресурсы не изменяются.
Compose interpolation очищается от deployment inputs родительского shell,
включая optional Telegram/support/audit/email и `COMPOSE_*`.

Реальные локальные backend/web images:

- Backend: `sha256:05b1d8a206be7daa7bb75eb0ec63d151220a9b1c8dae4fb96d18a76e41ddb09b`.
- Web: `sha256:a16a6b12b56d3d5705e858bb85807585fa884dd6b084c188511df2791b320cf0`.

Caddy использует собственный self-signed certificate. Chromium направляет
`cabinet.example.test` в loopback, получает настоящий `/config.json` через
browser fetch. Документные ссылки проверяются без переходов на внешний сайт.
Main/support Telegram и operational email выключены; bot token не создаётся.
Нет живых panel/payment providers, сообщений Telegram, денег или production.

## Проверки

| Проверка | Результат и граница |
| --- | --- |
| Go secret-file/config boundary | PASS под `-race`: required/plaintext/missing/unreadable/empty/conflict/valid; безопасная ошибка без значения или пути, старые Go semantics сохраняются. |
| Legacy Python secret files, callers, receipt email | PASS: explicit file precedence, отсутствие fallback при bad file, Heleket/YooKassa/DB/Redis callers; YooKassa-on требует валидный deployment `SHOP_EMAIL`. |
| Полный connected Python набор | 117 PASS за 25.525s; затем новый тест изоляции smoke и повтор всех шести config/inventory тестов PASS. Текущий набор содержит 118 тестов; полный final-source набор проверяет CI. |
| Полный web browser набор | 566 PASS за 4.9m. Runtime config включает reload/registration versions, missing/malformed/invalid/incomplete settings, invalid product name; ru/en branding, accessible role/name и keyboard focus. |
| Shell generator, typecheck, source names, generation, Go vet | PASS. Генерируемые контракты/SQL не изменились. Inventory test связывает перечень с фактическими Go/Python/shell readers. |
| Реальный serve/HTTP/River без Telegram token | PASS: выключенный Telegram не требует token; два последовательных deployment набора готовы через Caddy. |
| Реальный serve с Telegram-on без token | PASS: отдельный процесс отказывает с безопасным `SERVICE_UNAVAILABLE`; первый backend остаётся ready. |
| Два public deployment набора на одном web image | PASS: меняются имя, версии, documents/support URLs; image ID, HTML и все найденные asset bytes неизменны, `/config.json` имеет `no-store` и точный public allowlist. |
| Chromium через настоящий Caddy, оба deployment набора | PASS в ru/en: title/brand plain text, accessible names, keyboard focus, document/support href, без configuration alert. Регистрация не записывается этим smoke. |
| Прежний контейнерный smoke | PASS: HTTPS/CSP/public-private routing, доступ к secret files, повтор migrations, сохранённый River job после recreate, ограниченный rollback, provision-only restore runtime и cleanup своего project. |
| С42–С44 и тест на момент cutoff | Focused `internal/httpapi` PASS под `-race` за 54.647s: maintenance authority/replay/admission и single-connection guards, mail delivery compatibility/cancellation/single connection, readiness, native lifecycle, все 27 случаев plan-change eligibility. |
| Полный локальный Go race | Не завершён: выбранный общий package timeout 10m достигнут в `TestPlanChangeEligibility/restriction` при создании очередной test DB; остальные packages PASS. Assertion failure не зафиксирован. Named test и эксплуатационные regressions прошли отдельным прогоном; это не заменяет полный required CI с 60m timeout. |
| Compose native-disabled contract | PASS: backend/migrate/reconcile получают `LEGACY_BOT_API_ENABLED=false`; disabled adapter/main bot файлы допускают `/dev/null`, public name передаётся gateway. |

Focused команда (четыре test-only file inputs задаются окружением):

```sh
go -C backend test -race ./internal/httpapi \
  -run '^(TestPlanChangeEligibility|TestMailDelivery.*|TestMaintenance.*|TestReadyz.*|TestNativeLifecycle.*)$' \
  -count=1 -timeout=5m -v
```

Начальные Python failures воспроизвели fallback на плохой secret file, browser
failures — отсутствие deployment branding/валидации. После исправлений PASS.
Первый connected Python запуск без test URL files отказал на prerequisite;
повтор получил собственные fixture inputs. Первая версия native browser helper
использовала Node request, который не применяет Chromium DNS mapping, и получила
`ENOTFOUND`; browser fetch исправил сам harness. Последний полный smoke прошёл,
cleanup выполнялся и после неуспешного запуска. Зависимости установлены на
Python 3.13 из существующего lock; версия проекта/lock не менялась.

## Независимое ревью

Обычный fresh reviewer Astra/high, read-only, без TradeOS role. Найдены и закрыты:

- P1: ambient shell мог включить внешний poller в synthetic smoke. Исправлено
  в общем Compose runner, подтверждено отдельным isolation test и реальным smoke.
- P2: удаление branded receipt default могло оставить YooKassa-on с пустым email.
  Добавлена ранняя безопасная проверка `SHOP_EMAIL` и адресные negative tests.

Reviewer подтвердил оба исправления. Последняя дельта common legacy flag,
optional adapter file и native Chromium helper повторно проверена тем же
reviewer: новых actionable findings нет. Runtime/full CI reviewer не запускал;
локальные runtime результаты получены координатором.

## Ограничения и доставка

Host permissions и журнал actor/reason — существующая операторская процедура,
не новый web audit endpoint. Screen reader проверен через browser accessible
roles/names; ручной VoiceOver сеанс не проводился. Живые Telegram/providers,
DNS/TLS нового публичного домена и production требуют своей внешней приёмки.
Referral/promocode Р7, legacy import и cutover не считаются выполненными здесь.
Backup по С43 не включает deployment env/secrets/certs/Redis/external state.

Все required Platform scopes и три image checks должны пройти на точной PR
ревизии/checkout tree перед manual merge. Preview GHCR/prerelease разрешён
мандатом; production deployment не разрешён. Состояние issue/Project и cleanup
фиксируются после действительного merge, отдельно от этих локальных результатов.
