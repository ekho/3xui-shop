# С33.Р7 — локальная приёмка реферального триала

Дата 2026-10-10, owner [#52](https://github.com/ekho/3xui-shop/issues/52).
Контракт `2026-10-10-referred-telegram-trial-v1`, ветка
`feature/s33-referral-trial`, base `a42fc062a52df25d03dff0bd9ae0a71729d565a2`.
**No DDL**, последняя миграция **00042**. [Спецификация](../superpowers/specs/2026-10-10-s33-referral-trial-design.md),
[план](../superpowers/plans/2026-10-10-s33-referral-trial.md).
Exact final HEAD, независимое review, CI и merge фиксируются в #52/PR после
коммита; этот документ описывает выполненные локальные проверки.

## Результат и доказательства

bonuses резервирует полный реферальный срок в существующем referral fact.
subscriptions сохраняет один обычный grant/operation/River job; прежний
VPN worker подтверждает выдачу. Default SHOP_REFERRED_TRIAL_ENABLED=false,
SHOP_REFERRED_TRIAL_PERIOD=7; обычные traffic/devices и TRIAL_ENABLED сохраняются.
Web-origin после linking остаётся ручным. Trial не создаёт paid order или
purchase rewards; их funding/refund/restriction guards #51 сохранены.

Использовались два собственных Docker PG/Redis containers с проверенным
непересекающимся IPAM и localhost random ports. Каждая проверка создаёт и
удаляет отдельную testkit database. HTTP/SMTP/panel работают через собственные
TLS fixtures, native Telegram SDK polling использует собственный Bot API.
Чужие fixtures и Docker daemon не менялись.

| Проверка | Результат |
| --- | --- |
| HTTP/shared transaction | `TestReferredTelegramTrialReservation/Policy/Rollback`: восемь разных конкурентных ключей дают одну выдачу; повтор сохраняет IDs/срок; disabled/unknown/self/legacy/web credentials/consumed facts; zero traffic/devices; очередь и reservation audit полностью откатываются. Focused HTTP/app `-race` PASS (174.637s/9.776s), включая прежние #31 auth/CSRF/strict input/source/used/ban/restriction и module/SQL boundaries. |
| Небезопасные лимиты | `TestReferredTelegramTrialInvalidLimits`, `-race` PASS: zero/negative/overflow period, negative/overflow traffic/devices не оставляют request/grant/operation/referral reservation. Config проверяет false/7 defaults, true/false, 1..106751 days и безопасные startup errors до secrets/dependencies. |
| Native Telegram/shared backend | 12 основных сценариев и source подслучаи `-race` PASS89.088s: шесть новых #52, обычный Telegram trial #31, referrals #50, четыре purchase reward сценария #51. Нет failures/skips. HTTP signed Mini App, SDK /start и operator callbacks, River и TLS PanelClient остаются настоящим Go путём. |
| Реальная цель доступа | `TestNativeReferredTrialReservationRecovery`: 7 полных дней вместо 3+7, 27GiB/2 devices (panel limitIp=3), один grant/job; persisted start определяет ровно семидневный expiry. TLS readback проверяет срок, лимиты, UUID/sub ID. После усиления этих assertions отдельный `-race` PASS на окончательных исходниках. |
| Restart и aliases | Новые Modules/River/Telegram на retained DB сохраняют reservation/period/traffic/devices и request/operation/keys. Actual SMTP initial-email proof → web login → Telegram unlink/relink с другим alias оставляет один account/первый inviter; второй trial недоступен. Original web+TG через actual link и native operator callback сохраняет ручные 3 дня. |
| Неопределённый эффект | `TestNativeReferredTrialUncertainPanelWrite`: потерянный add response и недоступный readback оставляют needs_review/unrewarded. Новый ключ отвергнут, повтор target/identity не создаётся; authorized operator reconcile подтверждает тот же срок/add ровно один раз. |
| Финальная атомарность | Ошибка `referral_trial_granted` audit после внешней записи откатывает trial applied/grant/referral timestamp. Старый target сохраняется; operator reconcile завершает его и создаёт единственный grant audit. Очередь отдельно откатывает весь reservation и допускает тот же key после ремонта fixture. |
| Purchase rewards и schema | Focused bonuses/db/HTTP `-race` PASS5.808s/7.567s/21.941s: прежние funded callbacks, pending rewards, source/refund guards, legacy DAYS/MONEY и schema retention. Новой миграции/перенумерации нет. Native #49 promo restart + actual Chromium backend, `-race` PASS15.128s. |
| Интерфейс | Прежние `telegram-trial.spec.ts`/`referrals.spec.ts`, Chromium21/21 PASS9.2s: ru/en, keyboard/semantic names, loading/empty/error/retry/auth/uncertain state. UI/DTO не менялись. |
| Статика | Go/web generation без consumer drift; `go vet ./...`, semantic names, web typecheck/test build, Compose config и diff checks PASS. Build сохраняет dependency directive/chunk warnings; dependency files не менялись. |

Сначала новый shared test показал ordinary 3 days и отсутствие reservation.
Native checks затем выявили преждевременную audit FK на ещё не вставленную
operation. Reservation audit теперь содержит account/request/referral; тот же
atomic automatic decision связывает request с operation. Final grant audit
содержит оба IDs. Исправление не добавляет третьего hook или исполнителя.

## Ограничения и доставка

Restart здесь означает пересоздание native module/worker/SDK instances на
сохранённой БД. Отдельный OS-process kill и populated-referral-trial dump/restore
локально не выполнялись. Panel/Bot API — собственные fixtures; browser UI
проверяется через intercepted API, а #49 connected Chromium — с actual backend.
Full principal Platform перед merge проверяет весь Go/race, сохранённый Python,
connected browser consumers, реальный отдельный Go process/3X-UI3.7.0 и public
schema backup/restore. Эти CI gates не подменяют отдельную populated-state
репетицию новой льготы. Требуются три Image gates на final source.

Независимое review/PR/manual merge относятся к exact final HEAD и записываются
в canonical #52/PR. Done — merge в v2 и выполненная локальная приёмка;
preview публикация разрешена отдельно и не добавляет ожидание к DoD.
Production, live Telegram/платежи/panel, полный legacy import и cutover
не выполнялись. Выключение benefit flag действует на новые выдачи; persisted
pending/review facts завершаются прежним worker/reconcile.
