# Этап 3 — Трафик в тарифах + напоминания (детализация до кода)

_База: форк `snoups/3xui-shop`. Пути реальные._

> **⚠️ Правки по аудиту 2026-07-04 (см. раздел 2A мастер-плана).** Обновлены под: **m1** (метод сброса — `client.reset_stats(inbound_id, email)`), **B6** (совместимость py3xui↔панель), **m7** (порядок update/reset + обработка ошибок), **M10** (промокоды не должны стирать лимит трафика), **M7** (`user_id` в кнопке «Продлить»), **M8** (локаль в кроне), **M2** (дедуп-ключи привязать к циклу), **B5** (фильтр Stars-рекуррента — ретро из Этапа 5), **m6** (нагрузка крона), **m2** (семантика порогов).

## Цель

Сделать подписку трёхмерной (срок + трафик + устройства): добавить лимит трафика в тарифы, пробросить его через покупку в 3x-ui, показать остаток в профиле, добавить напоминания по трафику и мультипороги по сроку, сбрасывать трафик при продлении.

## Что уже есть

- `VPNService.create_client(..., total_gb=0)` и `update_client(..., total_gb=0)` — **уже принимают трафик** (пишут `Client.total_gb`).
- `ClientData` уже отдаёт `traffic_total/remaining/used` (читается из панели в `get_client_data`).
- Крон `tasks/subscription_expiry.py` шлёт напоминание, но **только по сроку, один порог (24 ч)**; кнопка «Продлить» в коде закомментирована (помечено BUG).
- Провижининг в `_gateway._on_payment_succeeded` вызывает `vpn.create_subscription/extend_subscription/change_subscription(user, devices, duration)` — **без трафика**.

## Изменения по файлам

| Файл | Что делаем |
|---|---|
| `data/plans.json` | + поле `traffic_gb` в каждый план (0 = безлимит) |
| `app/bot/models/plan.py` | + `traffic_gb` в `Plan` + `from_dict/to_dict` |
| `app/bot/models/subscription_data.py` | + `traffic: int = 0` |
| `app/bot/routers/subscription/payment_handler.py` | проставить `callback_data.traffic = plan.traffic_gb` перед `create_payment`; показать трафик в тексте заказа |
| `app/bot/routers/subscription/subscription_handler.py` + `keyboard.py` | показать трафик в выборе тарифа |
| `app/bot/payment_gateways/_gateway.py` | пробросить `traffic_gb=data.traffic` в `vpn.*_subscription` |
| `app/bot/services/vpn.py` | + `traffic_gb` в `create/extend/change_subscription`; ГБ→байты; сброс трафика при продлении |
| `app/bot/utils/formatting.py` | (опц.) хелпер `gb_to_bytes` |
| `app/bot/tasks/subscription_expiry.py` | мультипороги по сроку + пороги по трафику + кнопка «Продлить» |
| `app/bot/routers/profile/*` | вывод остатка трафика (данные уже есть) |

---

## Шаги с код-скетчами

### 1. `plans.json` — добавить трафик
```json
{
  "durations": [30, 60, 180, 365],
  "plans": [
    { "devices": 1, "traffic_gb": 100, "prices": { "RUB": {"30": 70}, "XTR": {"30": 60} } }
  ]
}
```

### 2. `Plan` — `models/plan.py`
```python
@dataclass
class Plan:
    devices: int
    traffic_gb: int          # 0 = безлимит
    prices: dict[str, dict[int, float]]

    @classmethod
    def from_dict(cls, data):
        return cls(
            devices=data["devices"],
            traffic_gb=data.get("traffic_gb", 0),
            prices={k: {int(m): p for m, p in v.items()} for k, v in data["prices"].items()},
        )
```

### 3. `SubscriptionData` — `models/subscription_data.py`
```python
class SubscriptionData(CallbackData, prefix="subscription"):
    ...
    devices: int = 0
    duration: int = 0
    traffic: int = 0     # <-- ГБ, 0 = безлимит
    price: float = 0
```
(CallbackData ≤64 байт — одно целое влезает; при риске переполнения не носить в callback, а брать `plan.traffic_gb` в провижининге по `devices`.)

### 4. Проставить трафик при выборе оплаты — `payment_handler.py`
```python
plan = services.plan.get_plan(devices)
price = plan.get_price(currency=gateway.currency, duration=duration)
callback_data.price = price
callback_data.traffic = plan.traffic_gb   # <--
```

### 5. Проброс в провижининг — `_gateway.py`
```python
if data.is_extend:
    await self.services.vpn.extend_subscription(user, data.devices, data.duration, traffic_gb=data.traffic)
elif data.is_change:
    await self.services.vpn.change_subscription(user, data.devices, data.duration, traffic_gb=data.traffic)
else:
    await self.services.vpn.create_subscription(user, data.devices, data.duration, traffic_gb=data.traffic)
```

### 6. Слой VPN — `services/vpn.py`
```python
def gb_to_bytes(gb: int) -> int:
    return int(gb) * 1024 ** 3  # 0 -> 0 (безлимит)

async def create_subscription(self, user, devices, duration, traffic_gb: int = 0) -> bool:
    if not await self.is_client_exists(user):
        return await self.create_client(user=user, devices=devices, duration=duration,
                                         total_gb=gb_to_bytes(traffic_gb))
    return False

async def extend_subscription(self, user, devices, duration, traffic_gb: int = 0) -> bool:
    # m7: reset ПЕРЕД update (на 3x-ui v3.4.2 resetTraffic сам ре-энейблит; порядок не критичен,
    #     но extend не считать успешным, пока не прошли ОБА вызова).
    ok = await self.update_client(user=user, devices=devices, duration=duration,
                                  replace_devices=True, total_gb=gb_to_bytes(traffic_gb))
    if not ok:
        return False
    if not await self.reset_traffic(user):   # сброс использованного при продлении
        logger.error(f"extend: reset_traffic failed for {user.tg_id}")  # алерт админу, не тихий крэш
        return False                          # не помечать extend успешным без сброса
    return True
```
> ⚠️ Единицы: в 3x-ui поле `totalGB` — **в байтах**. ГБ из тарифа умножать на 1024³. `py3xui.Client.total_gb` пишет именно `totalGB` (byte-значение).

**M10 — промокоды не должны стирать лимит трафика.** В форке `VPNService.update_client(..., total_gb: int = 0)` безусловно делает `client.total_gb = total_gb`, а `process_bonus_days`/`activate_promocode` зовут его без `total_gb` (дефолт `0` = безлимит) → снимут платный лимит. Сменить сигнатуру и сохранять текущий лимит при `None` (симметрично тому, как сохраняются устройства при `replace_devices=False`):
```python
async def update_client(self, user, devices, duration, replace_devices=False,
                        total_gb: int | None = None, ...):
    client = await self.get_by_email(user)          # текущее состояние из панели
    ...
    if total_gb is not None:
        client.total_gb = total_gb                  # меняем лимит только когда явно передан
    # else — оставляем client.total (байты) как есть → промокод/бонус срок не трогает трафик
```
Проверить ВСЕ вызовы `update_client` (extend/change_subscription передают `total_gb` явно; `process_bonus_days` — нет).

**m1/B6 — сброс трафика: точная сигнатура.** `update_client` счётчик не обнуляет. В py3xui **0.3.2** метод — `client.reset_stats(inbound_id: int, email: str)` (`inbound_id` в URL), не `resetClientTraffic/{email}`:
```python
async def reset_traffic(self, user) -> bool:
    connection = await self.server_pool_service.get_connection(user)
    if not connection:
        return False
    inbound_id = await self.server_pool_service.get_inbound_id(connection.api)
    if inbound_id is None:
        return False
    try:
        await connection.api.client.reset_stats(inbound_id=inbound_id, email=str(user.tg_id))
        return True
    except Exception as e:
        logger.error(f"reset_traffic {user.tg_id}: {e}")
        return False
```
> **B6:** это для py3xui 0.3.x + 3x-ui ≤v3.0. На 3x-ui v3.1+ клиентские эндпоинты переехали в `panel/api/clients/*` → нужна py3xui 0.7.0 и адаптация всех вызовов `vpn.py` (см. раздел 9 мастер-плана). Зафиксировать версию панели.

### 7. Напоминания — `tasks/subscription_expiry.py`
Расширить проход: пороги по сроку и по трафику, отдельные redis-ключи, рабочая кнопка «Продлить». С учётом правок аудита (M2, M7, M8, B5, m2, m6):
```python
DAYS_THRESHOLDS = [3, 1]          # дни до конца
TRAFFIC_THRESHOLDS = [1.0, 0.8]   # m2: по убыванию — слать только максимальный сработавший

# m6: фильтровать юзеров без активной подписки (server_id/подписка есть); ключи проверять ДО панели.
#     Для порогов достаточно client.get_by_email (total/up/down/expiry одним ответом),
#     НЕ get_client_data (тянет get_limit_ip → inbound.get_list() на каждого юзера).
client = await vpn_service.get_by_email(user)
if not client:
    continue

# M8: локаль юзера в APScheduler-таске (событие отсутствует → _() даст дефолт; из-за этого
#     кнопка в форке и закомментирована с # BUG). Оборачиваем текст И клавиатуру.
locale = user.language_code or DEFAULT_LANGUAGE

# --- по сроку ---
# B5: у юзера с активным Stars-рекуррентом expiry продлевается автосписанием — не пугать «истекает».
if client.expiry_time and client.expiry_time > 0 and not user.is_stars_auto_renew:
    days_left = (datetime.fromtimestamp(client.expiry_time/1000, timezone.utc) - now).days
    for d in DAYS_THRESHOLDS:
        key = f"notify:exp:{user.tg_id}:{d}"
        if days_left <= d and not await redis.get(key):   # m2: <= d, не == d (пропуск рана не теряет порог)
            with i18n.use_locale(locale):
                await notify(user, _("task:expiry_days").format(days=d), extend_kb(user))
            await redis.set(key, "1", ex=timedelta(days=2))
            break                                          # m2: один (ближайший) порог за раз

# --- по трафику ---
if client.total and client.total > 0:                     # безлимит (0) → пропускаем
    used_ratio = (client.up + client.down) / client.total
    for t in TRAFFIC_THRESHOLDS:                           # по убыванию
        key = f"notify:traf:{user.tg_id}:{int(t*100)}"
        if used_ratio >= t and not await redis.get(key):
            # m2: 1.0 — доступ уже приостановлен панелью, текст «трафик исчерпан», не «заканчивается»
            msg_key = "task:traffic_depleted" if t >= 1.0 else "task:traffic_used"
            # B5: рекуррент-юзеру кнопку вести в смену тарифа/отмену автопродления, не в разовое extend
            kb = change_or_cancel_kb(user) if user.is_stars_auto_renew else extend_kb(user)
            with i18n.use_locale(locale):
                await notify(user, _(msg_key).format(pct=int(t*100)), kb)
            await redis.set(key, "1", ex=timedelta(days=30))
            break                                          # m2: только максимальный порог за ран
```
**M7 — кнопка «Продлить».** `extend_kb(user)` обязана проставлять `user_id`, иначе payload уйдёт с `user_id=0`, оплата пройдёт, а `_on_payment_succeeded` упадёт на `User.get(tg_id=0)`:
```python
def extend_kb(user):
    b = InlineKeyboardBuilder()
    b.button(text=_("subscription:button:extend"),
             callback_data=SubscriptionData(state=NavSubscription.EXTEND, user_id=user.tg_id))  # M7
    return b.as_markup()
```
Дополнительно — guard в `_gateway._on_payment_succeeded`: `if user is None:` (деньги уже списаны → алерт админу, не тихий крэш).

**M2 — дедуп-ключи привязать к циклу.** Ключ `notify:traf:{tg_id}:{pct}` с фиксированным TTL 30 дн. не сбрасывается при продлении → в новом периоде уведомление подавляется, на тарифах 60/180/365 дн. — спамит. При успешном `extend_subscription` (после `reset_traffic`) и при ручном сбросе трафика админом чистить набор ключей одним вызовом:
```python
await redis.delete(f"notify:traf:{tg}:80", f"notify:traf:{tg}:100",
                   f"notify:exp:{tg}:3", f"notify:exp:{tg}:1")   # пороги константны, SCAN/KEYS не нужны
```

**m6 — параметры джобы:** в `add_job(...)` задать `coalesce=True` и `misfire_grace_time`, чтобы пропущенный ран догонялся, а не терялся (`max_instances=1` по умолчанию).
> Авто-отключение: по сроку 3x-ui отключает сам (`expiry_time`); по трафику — при достижении `totalGB`. Пункт «на 100% вручную `enable=False`» **убран** — панель блокирует depleted-клиента сама (m2).

### 8. Профиль — `routers/profile/*`
`ClientData.traffic_remaining/traffic_total/traffic_used` уже готовы — вывести остаток трафика на странице подписки и добавить кнопку «Продлить».

## Крайние случаи
- Безлимит (`traffic_gb=0` / `client.total==0`) → пропускать трафик-пороги (нет деления на ноль).
- Смена часового пояса/точности: пороги по дням считать в UTC (как в базе).
- **M2:** дедуп-ключи чистить при продлении/сбросе, иначе новый цикл без напоминаний или спам на длинных тарифах.
- **B5:** юзеру с `is_stars_auto_renew=True` не слать «срок истекает» (автосписание продлит само); детект лапса — по `stars_expires_at` (см. Этап 5).
- **M10:** промокод/бонусные дни не должны обнулять лимит трафика (`update_client` с `total_gb=None`).

## DoD
Тариф с лимитом трафика; покупка ставит лимит в 3x-ui; профиль показывает остаток; приходят напоминания на 3д/1д и 80%/100% (**рекуррент-юзерам — без ложных «истекает»**); продление сбрасывает трафик и продлевает срок; **промокод не снимает лимит; напоминания повторяются в каждом новом цикле**.

## Объём
**M** (трафик — S–M, напоминания — M).
