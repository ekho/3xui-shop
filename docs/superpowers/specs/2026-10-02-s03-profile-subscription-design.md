# С03: профиль и подписка в кабинете

Дата: 2026-10-02. Основание: [роадмап](../../roadmaps/2026-10-01-platform-roadmap.md),
поручение пользователя автономно реализовать С03–С06. Дизайн и решения принимаются
исполнителем в рамках этого поручения. База: `0e2009e3cd16b134d43ce05fc6f3b71587c30824`.
Статус: реализация и технические проверки готовы; [приёмка С03](../../evidence/s03-acceptance.md) ожидает общего свежего review С03–С06.

## Результат

Клиент без Telegram видит собственный email/язык, состояние подписки, срок,
число устройств, общий трафик, upload/download, лимит и остаток. Кабинет различает
отсутствие доступа, выдачу, необходимость сверки, активный, истёкший,
заблокированный, отключённый и исчерпавший трафик доступ. Безлимитные значения
показаны явно; неизвестные данные не превращаются в ноль или «безлимит».

Расширяются существующие `/api/v1/me` и `/api/v1/subscription`; дополнительный
профильный endpoint и новый framework не нужны. Key остаётся отдельным запросом.
Выдача/конфигурация профилей/продление относятся к другим сценариям.

## Источники и владение

Текущий бот: `app/bot/routers/profile/handler.py`, `app/bot/models/client_data.py`,
`app/bot/services/vpn.py`. Собственные native3X-UI3.7.0 и upstream controller
подтвердили `GET panel/api/clients/traffic/:email`: `email,uuid,subId,up,down`.
`clients/get` даёт `usedTraffic`, но upstream подставляет0 при ошибке статистики;
для достоверного upload/download используется отдельный traffic endpoint.

Backend хранит факт выдачи и неизменный target; панель сообщает наблюдаемое
состояние доступа. Перед чтением проверяются applied Operation/Grant владельца
и совпадение panel_key/VPN UUID/subId. Статистика также сверяется с этим владельцем.
Чужие identity и неизвестные memberships не допускают раскрытия ключа.
Provision/reconcile сохраняет строгую проверку исходного target из С01.
Чтение профиля не вызывает add/attach/update/delete/reset, не продлевает срок,
не меняет assignment и не создаёт повторную выдачу.

## Состояния и статистика

Панельные inbound tags классифицируются существующими exact сегментами
`regular`, `euru`, `unlimited`; неизвестная группа остаётся unknown.
Профиль unlimited включает regular; профиль выбирается с сохранением
этого наследования. Смешение euru с другими профилями требует сверки.
В боте banned — независимый флаг пользователя, выделенных
banned inbounds нет. Источник ban в backend — `accounts.vpn_banned`, default false,
отдельный от ограничения входа `restricted`; его изменение относится к С08.
`enable=false` без этого флага означает отключение, а не доказанный ban.
Приоритет после подтверждённой выдачи: banned → expired → exhausted → disabled → active.
`expiryTime=0` означает отсутствие срока; finite expiry сравнивается с server clock.
`totalGB=0` означает безлимитный трафик, `limitIp=0` — безлимитные устройства;
конечное число устройств равно `max(0,limitIp-1)`, как в действующем правиле N+1.
Название профиля unlimited не определяет лимиты: в действующем боте он может
иметь конечные 100GB/7 устройств и не иметь срока. UI использует наблюдаемые
параметры, а не выводит безлимит из имени профиля.

Native bytes сохраняются без пересчёта в GiB. Up/down должны быть неотрицательными
int64; сумма проверяется на overflow. Остаток finite limit = max(0,limit-used).
Для безлимита остаток null, для отсутствующей статистики все неизвестные значения null.
Нулевая статистика — настоящее наблюдение, не отсутствие данных.
Успешное наблюдение сохраняет up/down/used/observed_at и подтверждённые native
expiry/limitIp/trafficLimit/enable/profile в PostgreSQL атомарно;
старое конкурентное наблюдение не перетирает более новое. При следующем сбое
панели UI сохраняет последние наблюдённые параметры, не возвращается к снимку
стартового триала; expiry сравнивается с текущим временем сервера.

Недоступность панели сохраняет подтверждённые cached значения с `data_stale=true`
и временем наблюдения; UI сообщает о сбое. Identity/membership mismatch показывает
needs_review и запрещает key; не выдаёт данные чужого клиента. Key требует свежую
подтверждённую identity, распознанный разрешённый профиль и доступное состояние;
banned/disabled/exhausted/needs_review закрыты. Expired сохраняет прежний явный
key fetch для повторного импорта; это не активирует подписку.

## API и экран

OpenAPI авторский источник: `docs/api/openapi.yaml` (JSON-compatible YAML).
В Subscription добавляются optional поля `traffic_upload_bytes`,
`traffic_download_bytes`, `traffic_remaining_bytes` (nullable int64 ≥0),
`unlimited_traffic`, `unlimited_devices`, `access_profile`, `panel_error`,
`connection_available`. Existing required fields/routes сохраняются;
status дополнен banned/disabled/exhausted. Generated Go/TS обновляются штатно.
Нет query account_id или клиентского выбора server/panel_key.

В кабинете используется существующая карточка и ru/en тексты: email, язык,
status, срок, устройства, upload/download/total/remain/limit, время наблюдения,
понятное предупреждение stale и отдельный Retry. Ключ только в памяти;
очищается при logout, истечении/ограничении доступа, отказе fresh key fetch и уходе.
На узком экране нет горизонтального overflow; semantic labels/focus/keyboard сохранены.

## Приёмка

1. Собственный активный trial показывает реальный split traffic и правильные N/bytes/expiry.
2. none/provisioning/needs_review/expired/banned/disabled/exhausted видимы и различимы.
3. Finite remaining clamp, zero traffic, unlimited limits и без срока не смешиваются с unknown.
4. Native negative/overflow/foreign traffic и unknown memberships отвергаются; чужой key закрыт.
5. Panel down после успешного чтения сохраняет cached values + stale/time; первый сбой не рисует нули.
6. Конкурентные наблюдения сохраняют новое время; чтение не делает panel writes или повторные grants.
7. ru/en, mobile/desktop, клавиатура, retry, key privacy и существующий С01/С02 не регрессируют.
8. Actual native3.7.0/browser проверка; живой Happ/системный VPN не переключаются.

Внешняя почта, целевой benchmark и внешнее развёртывание остаются отдельными gates
из С01; они не блокируют эту локальную реализацию.
