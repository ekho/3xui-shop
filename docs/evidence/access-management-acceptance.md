# Локальная приёмка управления доступом

2026-10-02: С48 → С09 → С07/С08 и необходимая часть С41 реализованы и
локально приняты. Закрыты **31 функциональный критерий**, финальный обзор,
ограниченный проход исправлений и совместимость переименования.
Проверенная продуктовая ревизия — `faa87fda6f52e246ab5bdfbc05e715685e1a0ee2`;
переименование — `a0ddfe6`. Последующие изменения отчёта/роадмапа — документация.

| Объём | Критерии и фактическая поверхность |
| --- | --- |
| С48: ограничения и исторический импорт | Все 8 AC: [карта проверок](s48-acceptance.md). Выбранные успешные проверки: import10, browser16, native probe1, focused3, registration1. Исходные FAIL/BLOCKED сохранены. |
| С09: каталог и неизменяемые ревизии | Все 7 AC: [карта проверок](s09-acceptance.md). Browser13/import10 и отдельная проверка ограниченного оператора в реальном PostgreSQL. |
| С07: компенсация, назначение, стартовый период и сброс | Все 8 AC: [карта проверок](s07-acceptance.md). Native/browser47 PASS; исходные 3 BLOCKED сохранены. Включены lost reply, restart, partial membership и восстановление истёкшего доступа. |
| С08/С41: профили, VPN-ban, месячный сброс | Все 8 AC: [карта проверок](s08-s41-acceptance.md). Native/browser/restore32 PASS. Месячные границы, два Service с общей БД, ожидание занятого аккаунта, uniqueness и неоднозначный reset проверены source-тестами с управляемым временем. |

Карты содержат точные ревизии, команды, артефакты и границы source/component/
native доказательств. Исторические JSONL, screenshots, manifests и выполненные
команды при переименовании не переписывались.

## Финальный обзор и исправления

Один свежий read-only `tradeos-final-reviewer` на Astra/high рассмотрел
`32b0205 → a0ddfe6`, сверил 445 хешей и сообщил три Important-дефекта.
Root подтвердил причины; один bounded backend pass исправил их на `faa87fd`.
Публичный API не менялся. Это обзор предыдущей ревизии с последующей проверкой
исправлений root, а не повторный обзор всей исправленной ветки другим агентом.

| Дефект | Исправление и регрессия |
| --- | --- |
| Согласие на повторный reset переживало эффект и аварию до финальной записи | `MarkAccessReset` потребляет согласие атомарно до native reset. `TestAccessResetConsentConsumedBeforeNativeRetry` проверяет failure финальной записи, новый трафик, восстановление без третьего reset и новое явное согласие. [RED](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-XCpFcJ/output.log>) → [GREEN](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-K1JqsB/output.log>). Месячный общий путь: `TestMonthlyAcknowledgedResetDoesNotRepeatAfterFinalWriteFailure`, [GREEN](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-jIovw8/output.log>). |
| Сохранённый до первого триала профиль мешал выдаче ключа | Subscription и получение ключа используют одну проверку `NoClientIntent` и подтверждённый trial. `TestAccessSavedProfileFirstTrialKeysFollowGrantedClient` проверяет клиентский/операторский ключ, прежнюю identity, pending и ban. [RED](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-S0Ku7Y/output.log>) → [GREEN](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-Aqu1ED/output.log>). Этот переход дополнительно проверен на новом native аккаунте. |
| Повторный unlimited после изменения hidden revision ошибочно становился no-op | No-op требует совпадения native expiry/devices/traffic/managed membership с новым target. `TestAccessUnlimitedRevisionChangeQueuesCurrentTerms` подтверждает queue→apply новой revision, прежние счётчики/identity, replay и настоящий no-op. [RED](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-Ydox4o/output.log>) → [GREEN](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-ygjGs0/output.log>). |

Семь связанных тестов с `-race` прошли: [лог](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-C82wGv/output.log>).
Изменены восемь backend-файлов; SQL-код сгенерирован.
[Хеши исправлений](../../.superpowers/acceptance/naming/important-fixes.json).

## Окончательные source checks

`go test -race ./... -count=1` после исправлений: exit0, 207.304s,
шесть пакетов с тестами и два без тестов; [полный лог](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-wqzFb5/output.log>).
`go vet ./...`: exit0, [лог](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-Hanbob/output.log>).
Использованы `TEST_DATABASE_URL_FILE`/`TEST_REDIS_URL_FILE`, mode0600 файлы
и собственный тестовый PostgreSQL/Redis.

Web105/105, typecheck/build и Python105/105 не повторялись после backend-only
pass: root сверил остальные хеши с уже проверенным source.
[Web](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-1BocrF/output.log>),
[Python](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-Ajk2E3/output.log>),
[неизменность](../../.superpowers/acceptance/naming/post-review-source-stability.json),
[итоговый manifest](../../.superpowers/acceptance/naming/final-source-checks.json).
Naming scanner и `git diff --check` прошли. Первый неуспешный naming race
и исправление трёх DB fixtures сохранены в [исходном manifest](../../.superpowers/acceptance/naming/source-checks.json).

## Совместимость локального стенда

[Naming v1](../architecture/application-naming.md) применён в окне обслуживания
только к собственному Docker-стенду с **3X-UI 3.7.0**. До schema13→14 сохранён
backup mode0600. До запуска новых workers сравнение подтвердило сохранность
109 аккаунтов, 23 access operations, 551 audit events и 205 River jobs:
у заданий менялся только kind, остальные поля и история совпали.
[Данные до/после](../../.superpowers/acceptance/naming/migration-data.json).
Native settings/identity, panel image и конфигурация основного Docker VPN
сохранились. Один контрольный Redis-ключ сохранил значение и PTTL при переносе
namespace: [proof](../../.superpowers/acceptance/naming/redis-transfer.json).
Source-тесты проверяют четыре job kinds во всех семи состояниях, атомарный
отказ для неоднозначных jobs и продолжение восстановленного trial.

Первый rollout остановился до выключения writers из-за static-IP конфликта
offline validation; проверка в network-none исправила этот контроль.
Следующий остановился после успешной миграции: native readback шёл через
выключенный gateway. Root сверил историю при остановленном backend, поднял
gateway, проверил native digest и завершил первый запуск workers.
Backup, Redis transfer и миграция повторно не выполнялись.
[Завершение](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-WMBWI6/output.log>),
[runtime/images](../../.superpowers/acceptance/naming/runtime.json).
Исходные failed receipts `fRrnID`/`okiWIf` сохранены в private run-check logs.

Новый compatibility browser прогон дал **7/7 PASS**: прежние аккаунты входят,
клиентский/операторский ключ совпадает, role/CSRF guard и прежний VPN-ban
сохраняются, операторский экран 375px имеет keyboard focus. Новый unbanned
аккаунт с сохранённым EURU получил один trial grant, active подписку и оба
ключа. Компенсация добавила один день через новый worker, сохранив native
identity, membership, лимиты и счётчики.
[Expected/actual строки](../../.superpowers/acceptance/naming/browser.jsonl),
[лог](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-9vGVrE/output.log>).

Postflight: health OK, точные новые images, 208 completed jobs только с semantic
kinds (141 mail/46 trial/21 access), unresolved access0, Docker VPN подключён
с прежней конфигурацией; bot/reconcile/probe остановлены.
[Postflight](../../.superpowers/acceptance/naming/postflight.json),
[лог](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-DRGghB/output.log>).
C03 compatibility и C05 итоговая локальная приёмка прошли.

Результат сохранён в локальной feature-ветке. Production, реальный cutover,
push/MR/merge/CI/release не выполнялись. Установленный Happ и Mac VPN не
переключались. Физический месячный cron tick и внешняя SMTP/performance
готовность этим локальным результатом не подтверждаются.
