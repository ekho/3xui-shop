# С33.Р7 — реферальная льгота Telegram-триала

Владелец #52 / bonuses. Контракт `2026-10-10-referred-telegram-trial-v1`.
База `a42fc062a52df25d03dff0bd9ae0a71729d565a2`; prerequisites #31/#51
доставлены в PR85/107. Совместим с `2026-10-08-s33-telegram-trial-v1`,
`2026-10-10-referrals-v1` и `2026-10-10-referral-rewards-v1`.
Canonical decision: https://github.com/ekho/3xui-shop/issues/52#issuecomment-6096597477.

## Поведение

Клиент с исходным Telegram-каналом и сохранённым первым inviter активирует
единственный триал через прежний signed Mini App/HTTP endpoint. При
`SHOP_REFERRED_TRIAL_ENABLED=true` его полный срок равен
`SHOP_REFERRED_TRIAL_PERIOD` (default 7), вместо обычного `TRIAL_PERIOD`.
Это не добавочные дни и не оплаченный заказ. Default флага false сохраняет
обычный триал. `TRIAL_ENABLED` разрешает весь сценарий; disabled purchase
rewards/MONEY не отключают отдельную trial benefit. Трафик и устройства
берутся из прежних `TRIAL_TRAFFIC_GB` и `BONUS_DEVICES_COUNT`.

Политика зависит от неизменяемого SourceKind. Исходный web остаётся ручным
с обычным сроком после linking; исходный Telegram после добавления email
сохраняет автоматическую политику. Поздний start/link не меняет inviter.
Неизвестный/self source не создаёт реферальной льготы. Legacy history/IDs
сохраняются; неизвестная история триала не включает automatic eligibility.

## Владельцы и данные

**No DDL**, последняя миграция **00042**. `bonuses` использует существующие
00041 `referrals.referred_bonus_days` для резерва полного срока и
`referred_rewarded_at` для подтверждённой выдачи. `subscriptions` сохраняет
обычные request/grant/operation и вызывает узкие публичные bonuses hooks
в той же транзакции. Account lock и unique trial_grant защищают одну выдачу.
`bonuses.New(pool,authority,now)` и один `ConfigureSubscriptions` сохраняются.
Нового модуля, worker, endpoint или платёжного reward нет.

Резерв периода атомарен с request/grant/operation/River/audit. Неопределённый
эффект оставляет старый резерв и needs_review; confirmed readback отмечает
referred_rewarded_at и `referral_trial_granted` в той же финальной транзакции,
что и обычный grant. Replay возвращает старые request/operation. Новый ключ,
restart, concurrent event и другой alias одного account не дают новый триал.
Смена флага/периода после резерва не переписывает его.
Reservation audit ссылается на уже созданный request; следующий automatic
decision связывает его с operation. Grant audit содержит оба идентификатора.

## Права, ошибки и интерфейс

Сохраняются signed consent, current auth/Origin/CSRF/idempotency, restricted,
VPN ban, Telegram identity и обычные used/manual/access guards #31. Публичная
ошибка не содержит исходный config value, session или VPN identifiers.
Невалидные flags/period не достигают запуска; период ограничен существующим
безопасным диапазоном trial duration, без нового продуктового лимита.
Прежний Cabinet/Mini App показывает фактический срок подключения. DTO,
ru/en кнопки, keyboard, status/alert и ошибки/пустые состояния сохраняются.

## Приёмка и rollback

Owned PG/Redis/TLS panel/Bot API fixtures проверяют реальные HTTP/Telegram/
River boundaries: оригинальный source, linking/alias, flags и лимиты,
concurrency/replay, atomic queue/audit rollback, restart, uncertain write и
reconcile без смены operation/UUID/sub ID/key. Purchase rewards #51 имеют
свои независимые funding guards и проверяются регрессией. Существующие ru/en
trial UI checks выполняются; новый экран не нужен.

После локальной приёмки: independent ordinary review, exact-head principal
Platform и bot/backend/web Image gates, manual merge в v2, Closed/Project Done.
Production и live Telegram/panel/payment не входят. Rollback приложения
выключает SHOP_REFERRED_TRIAL_ENABLED для новых выдач, сохраняя grants,
резервы и уже поставленные jobs; существующий reconcile завершает их.
