# С26 — общая и кампанийная статистика

Основание: #36, автономный мандат #55/2026-10-06-v2-sequential-execution-and-merge; Native.
Результат: оператор читает общие показатели и показатели неизменяемой когорты кампании, раздельные деньги, подтверждённую активность, серверы/клиентов и ссылки фиксированных tag-групп.
Предпосылки #60/#25/#35 закрыты; source base940b8138aa300ecac8062735748abeee182ad303, branch feature/s26-statistics. Статус: backend и общий UI реализованы; actual local3.7.0 report/process/restart, Python112 и web438 прошли. Полный Go1278/1278 также прошёл; независимое review и доставка ещё pending.
Версия решения 2026-10-08-s26-statistics-v1, owner audit_reports/#36. Runtime discriminator StatisticsReport.version — statistics-v1; номер сценария остаётся только в документах/GitHub.

Использую brainstorming для архитектурного пути: публичные report ports и единый компонент меняют связи нескольких владельцев. Повторного approval не требуется по прямому поручению вести остальные документы и реализацию автономно; Native сохраняется.

## Решение
Использовать публичные typed function ports, собираемые в app.NewModules, и read-only REPEATABLE READ в audit_reports. Показатели денег/триала остаются вычислениями владельцев payments/subscriptions. Их DTO переходят в отчётный контракт с type aliases под прежними именами, без изменений старого JSON, импортов потребителей и финансовых guards.
Альтернатива — преобразование независимых DTO в app — сохраняет изоляцию, но требует повторять каждый денежный field. SQL других модулей в audit_reports нарушает принятые границы. Новые процессы, зависимости, интерфейсы, кэш, export, произвольные периоды, графики и фоновые сборщики не требуются.

## Показатели
- users: все выбранные account UUID, включая Telegram/legacy/restricted; global использует фактические account IDs, empty campaign никогда не означает global.
- web/telegram registrations и legacy_name/legacy_trial_used остаются метаданными карточки С25 из старого endpoint. Новый общий отчёт не добавляет speculative разбивку direct registrations. Наличие source_name не доказывает старый signed hash. LegacyTrialUsed — известные flags сохранённых acquisition, не все исторические триалы.
- trial_users: granted trial_grants; paid_orders/users/repeat_users: точная связка paid order/funding receipt С25, даже после fulfillment review/refund. Repeat >=2 funded orders у одного UUID.
- money: RUB/USD/XTR отдельно, exact minor strings, 643 нормализуется в RUB; gross, известный net и count неизвестных net отдельно. Возвраты — исходные returned_currency/returned_amount decimal strings, без выдуманной конвертации/вычитания.
- legacy completed/paid/repeat/quoted: отдельный архивный блок; unknown quotes сохраняются. Импорт не становится native cash proof.
- conversions: trial_users/users, paid_users/users, repeat_users/paid_users. Проценты exact decimal strings с двумя знаками, округление half-up; denominator0 => null. 1/3 => "33.33". Это не trial-to-paid attribution.
- active_users: только текущий подтверждённый доступ; nullable при неизвестной части. known_active/known_inactive/unknown дают явные счётчики и observation timestamp.
- restricted не выключает VPN по С48. VPN-ban и незавершённая выдача независимы.
- servers/clients: текущее настроенное ядро с одной panel_id, actual provider records, не количество покупателей; global overview явно подписан даже для campaign scope.
- groups: banned/regular/unlimited/euru. Inbound references — точные hyphen tag segments, disabled включены и enabled отдельно; user references — persisted access_profile и VPN-ban intent выбранных UUID; plan references — текущие stored catalogue profiles включая hidden/archived. Unknown user profile отдельно. Один UUID может ссылаться и на свой профиль, и на banned overlay. Unlimited access inheritance regular не удваивает stored references.

## Чтение панели и активность
VPN owner один раз читает /panel/api/inbounds/list и /panel/api/clients/list на панель. 3X-UI3.7.0 full list возвращает flattened ClientRecord с inboundIds и optional traffic. Paged slim без UUID непригоден. В полном списке 3.7.0 traffic присоединяется по email и вложенные UUID/subId пусты: только отсутствующие/пустые идентификаторы связываются с enclosing unique client. Email, непустые conflicts и nonnegative/overflow counters проверяются прежним strict parser; отдельный Traffic() сохраняет прежние guards. No per-account get/traffic calls, panel mutations, ObserveProfileTraffic, job writes.
Использовать существующие HTTPS/CA/auth/redirect/1MiB limits и общий provider deadline10s; outage/oversize/malformed => unavailable/unknown, не нули. Только счётчики и stable safe error code в ответе, без credentials/keys/sub IDs/raw provider JSON.
Подтверждение аккаунта: exact panel_id/panel_key/VPN UUID/sub_id; нет unresolved trial/access; совпадают persisted applied target и текущий лимит/expiry/device/membership; trial target fallback для NoClientIntent. Applied legacy identity без доказанного target остаётся unknown. Нет назначения/доступа без pending => inactive.
Проверить enabled, expiry>now (0=без expiry), used<quota (0=без лимита), nonnegative traffic sum без overflow, known enabled managed membership. Drift/duplicate identity/missing traffic/unknown membership => unknown. Несколько inbound memberships не создают несколько users.
Panel snapshot отличается по времени от DB snapshot; обе даты сохраняются, обещания атомарности с провайдером нет.

## API и интерфейс
Additive POST /api/v1/operator/reports/statistics, operation ReadOperatorStatistics.
Body: {campaign_id:null|UUID}; unknown fields/query rejected, существующий bounded decoder16KiB.
Cookie web operator, Origin/CSRF на POST; actor только session. Read-only без Idempotency-Key и audit writes.
401/403 до protected reads; свежая RequireOperator повторно после завершения snapshot и provider reads, перед возвратом. Missing campaign404; paused/deleted сохраняют cohort; malformed400; DB unavailable503. Panel partial/unavailable200 с nullable counters.
200 содержит version, campaign_id, database_observed_at, users/trials/payments/conversions/activity/groups/unknown_user_profiles/servers с явно документированным scope.
Один AdminStatistics компонент использует endpoint в /admin/statistics и campaign card; old campaign endpoint/JSON остаются совместимыми. Нативные деньги и архив visually отдельно; empty/loading/error/retry, unknown/partial/time, refresh руками. RU/EN, 375px, keyboard/focus/semantic headings and dl. Abort/request identity предотвращают stale-response смены кампании/роли/языка. Секреты отсутствуют в HTML, API и ошибках.

## Приёмка
1. Empty global и empty cohort корректны; чужие UUID не входят в кампанию; deleted/paused не теряют историю; missing404.
2. Exact funding/extra receipt/refund/legacy/currencies/netunknown; native UI и campaign card совпадают. Exact 1/3, zero denominator.
3. RBAC/CSRF/Origin/forged actor/unknown JSON/query, роль отозвана во время blocked panel response =>403 и no payload. Никаких DB/panel writes от отчёта.
4. Bulk2reads; duplicate/malformed/oversize/negative/overflow/niltraffic/wrong UUID/sub/panel/unknown membership => unknown; pending/review, expired/exhausted/disabled/ban, zero infinite/unlimited.
5. Group refs точные, archived/hidden plans, unknown profiles, memberships dedup. Server outage не делает clients0 или active0.
6. Rendered RU/EN global+campaign views, money >2^53, partial/empty/retry/abort/stale/roleloss, 375px keyboard.
7. Actual local pinned3X-UI3.7.0+TLS mail/stubTG, generated/static/wholeGo/Web/Python, one final Astra/high review, exact-source CI, manual PR to v2+images/prerelease.
Production, внешний wallet/SMTP/Telegram и Happ/VPN/Mac trust исключены; C13 external acceptance остаётся отдельно открытой. Полный pool/C39, history/retention/C29, import/C46 и cutover/remove Python/C47 не объявляются завершёнными С26.
