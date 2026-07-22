# Этап 4 — Ручная карта + модерация (детализация до кода)

_База: форк `snoups/3xui-shop`. Пути реальные._

> **⚠️ Правки по аудиту 2026-07-04 (см. раздел 2A мастер-плана).** Обновлены под: **B3** (модерация неатомарна — CAS вместо read-then-act; дырявый гард пропускает Reject→Approve), **M4** (сниппеты падали: `session()` не callable, `bot`/`config` не в сигнатурах), **M3** (регистрация роутера — в `routers/__init__.py`), **M12** (лимит PENDING-заявок и троттлинг «Я оплатил»), **M-no-amount** (сумма/тариф в заявке админу), **M9** (двойной биллинг при активном Stars-рекурренте), **M5** (локаль получателя).

## Цель

Добавить способ оплаты «карта→карта»: бот показывает реквизиты, пользователь жмёт «Я оплатил», админ подтверждает/отклоняет; при подтверждении — та же активация, что и у автоплатёжек.

## Как устроены платежи в базе (важно)

- Универсальный хендлер `payment_handler.callback_payment_method_selected` ловит `SubscriptionData.filter(F.state.startswith(NavSubscription.PAY))`, берёт шлюз `gateway_factory.get_gateway(method)`, вызывает `gateway.create_payment(data)` → **ожидает URL** и рисует `pay_keyboard(pay_url=...)`.
- Автошлюзы (пример `cryptomus.py`) в `create_payment` создают `Transaction(status=PENDING, payment_id=...)` и возвращают ссылку; вебхук/подтверждение → `handle_payment_succeeded(payment_id)` → наследуемый `_on_payment_succeeded` (обновляет транзакцию в COMPLETED и провижинит доступ).

Ручная оплата **не имеет URL**, поэтому для неё делаем отдельную ветку (показ реквизитов + «Я оплатил»), а подтверждение выполняет админ (вместо вебхука).

## Изменения по файлам

| Файл | Что делаем |
|---|---|
| `app/bot/utils/navigation.py` | + `PAY_MANUAL = "pay_manual"` в `NavSubscription`; + колбэки подтверждения/модерации |
| `app/config.py` + `.env` | + `PAYMENT_MANUAL_ENABLED`, реквизиты (`MANUAL_CARD_DETAILS`) |
| `app/bot/payment_gateways/manual_card.py` (new) | шлюз `ManualCard` (для реестра и `handle_payment_succeeded/canceled`) |
| `app/bot/payment_gateways/gateway_factory.py` | регистрация `ManualCard` по флагу |
| `app/bot/routers/subscription/manual_handler.py` (new) | ветка «реквизиты + Я оплатил», уведомление админов |
| `app/bot/routers/subscription/__init__.py` | include нового роутера **до** универсального payment-хендлера |
| `app/bot/locales/*` | тексты |

---

## Шаги с код-скетчами

### 1. Навигация + колбэки — `navigation.py`
```python
class NavSubscription(str, Enum):
    ...
    PAY_MANUAL = "pay_manual"
```
Модерация — через `CallbackData`-фабрику:
```python
class ManualPaidCallback(CallbackData, prefix="manpaid"):
    payment_id: str
class ManualModerationCallback(CallbackData, prefix="manmod"):
    action: str        # approve | reject
    payment_id: str
```

### 2. Конфиг — `config.py` + `.env`
`ShopConfig`: `PAYMENT_MANUAL_ENABLED: bool`. Реквизиты в отдельном поле/энве:
```
SHOP_PAYMENT_MANUAL_ENABLED=True
MANUAL_CARD_DETAILS="Сбербанк 2202 20** **** 1234, получатель Иван И."
```

### 3. Шлюз — `payment_gateways/manual_card.py` (new)
Нужен, чтобы попасть в фабрику и переиспользовать `_on_payment_succeeded`. Вебхука нет.
```python
class ManualCard(PaymentGateway):
    name = ""
    currency = Currency.RUB
    callback = NavSubscription.PAY_MANUAL

    def __init__(self, app, config, session, storage, bot, i18n, services):
        self.name = __("payment:gateway:manual")
        # ... присвоить зависимости (как в других шлюзах); вебхук не регистрируем

    async def create_payment(self, data: SubscriptionData) -> str:
        # M12: макс. одна активная PENDING manual-заявка на юзера — иначе юзер плодит
        #      неотличимые заявки и два админа заапрувят одну оплату (id разные, дедуп не спасёт).
        async with self.session() as session:
            existing = await Transaction.get_active_manual_pending(session, tg_id=data.user_id)
            if existing:
                return existing.payment_id                    # переиспользуем (или отменяем старую)
            payment_id = str(uuid.uuid4())
            await Transaction.create(session=session, tg_id=data.user_id,
                subscription=data.pack(), payment_id=payment_id, status=TransactionStatus.PENDING)
        return payment_id   # не URL, а id (используем в своей ветке)

    async def handle_payment_succeeded(self, payment_id): await self._on_payment_succeeded(payment_id)
    async def handle_payment_canceled(self, payment_id): await self._on_payment_canceled(payment_id)
```
Регистрация — `gateway_factory.py`, в список:
```python
(config.shop.PAYMENT_MANUAL_ENABLED, ManualCard),
```

### 4. Ветка «реквизиты + Я оплатил» — `routers/subscription/manual_handler.py` (new)
Ловим `PAY_MANUAL` раньше универсального хендлера:
```python
# M4: config добавлен в сигнатуру (используется config.shop.MANUAL_CARD_DETAILS) — иначе NameError
@router.callback_query(SubscriptionData.filter(F.state == NavSubscription.PAY_MANUAL))
async def manual_payment(callback, user, callback_data, services, gateway_factory, session, config, bot):
    # M9: активный Stars-рекуррент + ручная оплата → двойной биллинг (Telegram спишет звёзды сам).
    if getattr(user, "is_stars_auto_renew", False):
        await callback.answer(_("payment:manual:cancel_autorenew_first"), show_alert=True)
        return  # предложить сначала отменить автопродление (edit_user_star_subscription), см. Этап 5
    plan = services.plan.get_plan(callback_data.devices)
    callback_data.price = plan.get_price(Currency.RUB, callback_data.duration)
    callback_data.traffic = plan.traffic_gb
    gateway = gateway_factory.get_gateway(NavSubscription.PAY_MANUAL)
    payment_id = await gateway.create_payment(callback_data)   # создаёт/переиспользует PENDING
    await callback.message.edit_text(
        _("payment:manual:instructions").format(
            price=callback_data.price, details=config.shop.MANUAL_CARD_DETAILS),
        reply_markup=manual_paid_kb(payment_id))
```
Кнопка «Я оплатил» → `ManualPaidCallback(payment_id)`:
```python
# M4: session добавлен в сигнатуру (обогащаем заявку данными транзакции)
@router.callback_query(ManualPaidCallback.filter())
async def manual_paid(callback, callback_data, bot, config, session, redis):
    # M12: троттлинг — одна заявка = максимум одно уведомление админам
    throttle_key = f"manual:paid:{callback.from_user.id}:{callback_data.payment_id}"
    if await redis.get(throttle_key):
        await callback.answer(_("payment:manual:already_sent")); return
    await redis.set(throttle_key, "1", ex=timedelta(minutes=10))

    # M-no-amount: админ должен видеть сумму/тариф, а не только id — иначе подтверждает вслепую
    txn = await Transaction.get_by_id(session, payment_id=callback_data.payment_id)
    data = SubscriptionData.unpack(txn.subscription)
    plan = services.plan.get_plan(data.devices)
    admin_text = _("payment:manual:admin_request").format(
        user_id=callback.from_user.id, username=callback.from_user.username or "-",
        payment_id=callback_data.payment_id, price=data.price, currency=Currency.RUB.symbol,
        duration=data.duration, devices=data.devices, traffic=plan.traffic_gb)
    admin_ids = set(config.bot.ADMINS) | {config.bot.DEV_ID}
    for admin_id in admin_ids:
        try:
            await bot.send_message(admin_id, admin_text,
                reply_markup=manual_moderation_kb(callback_data.payment_id))
        except Exception:
            pass
    await callback.message.edit_text(_("payment:manual:awaiting_admin"))
```

### 5. Модерация админом
**B3:** read-then-act с гардом `status == COMPLETED` дырявый — не закрывает гонку Approve/Approve, Approve+Reject и Reject→Approve (после Reject статус CANCELED, а гард ловит только COMPLETED → Approve по «живой» кнопке другого админа выдаст доступ по отклонённому платежу). Делаем **compare-and-set по статусу ДО вызова шлюза**; действует только тот, у кого `rowcount == 1`.
**M4:** `session` инжектится как открытый `AsyncSession` (не фабрика) — убран `async with session()`; в сигнатуру добавлен `bot` (нужен в ветке reject).
```python
from sqlalchemy import update

@router.callback_query(ManualModerationCallback.filter(), IsAdmin())
async def manual_moderation(callback, callback_data, session, gateway_factory, bot, i18n):
    pid = callback_data.payment_id
    target_status = (TransactionStatus.COMPLETED if callback_data.action == "approve"
                     else TransactionStatus.CANCELED)
    # B3: атомарный переход PENDING -> target; выигрывает ровно один клик
    res = await session.execute(
        update(Transaction)
        .where(Transaction.payment_id == pid, Transaction.status == TransactionStatus.PENDING)
        .values(status=target_status))
    await session.commit()
    if res.rowcount != 1:                                   # уже обработано / гонка проиграна
        await callback.answer(_("payment:manual:already_processed")); return

    txn = await Transaction.get_by_id(session, payment_id=pid)
    gateway = gateway_factory.get_gateway(NavSubscription.PAY_MANUAL)
    if callback_data.action == "approve":
        # статус уже COMPLETED; повторная запись COMPLETED в _on_payment_succeeded идемпотентна
        await gateway.handle_payment_succeeded(pid)         # -> активация
    else:
        await gateway.handle_payment_canceled(pid)
        # M5: текст юзеру — в его локали; notify_by_id глотает TelegramForbiddenError
        target = await User.get(session, tg_id=txn.tg_id)
        with i18n.use_locale((target.language_code if target else None) or DEFAULT_LANGUAGE):
            await services.notification.notify_by_id(chat_id=txn.tg_id, text=_("payment:manual:rejected"))
    await callback.message.edit_text(callback.message.text + "\n\n" +
        _("payment:manual:done").format(action=callback_data.action))
    await callback.answer()
```
> **B3 (UX):** остальным админам кнопки остаются «живыми» (Telegram правит только сообщение кликнувшего). Сохранять `chat_id+message_id` всех разосланных админ-сообщений (Redis по `payment_id`) и после решения снимать клавиатуры у всех через `edit_message_reply_markup`. Безопасность обеспечивает CAS, снятие клавиатур — только UX.

### 6. Порядок роутеров
**M3:** реальная регистрация — `dispatcher.include_routers(...)` в `app/bot/routers/__init__.py::include()`, а НЕ в `routers/subscription/__init__.py` (там только `from . import ...`). Добавить `subscription.manual_handler.router` в этот вызов **строго перед** `subscription.payment_handler.router`, иначе универсальный `SubscriptionData.filter(F.state.startswith(PAY))` перехватит `PAY_MANUAL` и подставит uuid `payment_id` как `pay_url` в `pay_keyboard` → невалидная URL-кнопка/исключение. **Надёжнее** не полагаться на порядок в плоском списке из ~20 роутеров (легко сломать при мерже), а исключить `PAY_MANUAL` из универсального фильтра:
```python
@router.callback_query(SubscriptionData.filter(
    F.state.startswith(NavSubscription.PAY) & (F.state != NavSubscription.PAY_MANUAL)))
```

### 7. Кнопка выбора «карта»
В клавиатуре выбора способа оплаты кнопка `ManualCard` появляется автоматически из `gateway_factory.get_gateways()` (как остальные) — проверить, что список способов строится из фабрики.

## Крайние случаи
- **B3 — конкурентная модерация:** гард обязан быть атомарным CAS `status == PENDING` (через WHERE), а не `!= COMPLETED`. Закрывает Approve/Approve, Approve+Reject, Reject→Approve, двойной Reject. Одиночный UPDATE атомарен на SQLite и на Postgres (G9).
- **M12 — лимит заявок:** макс. одна активная PENDING manual-транзакция на юзера (`create_payment` переиспользует); троттлинг «Я оплатил» (redis); TTL-крон отмены брошенных PENDING — **в объём Этапа 4**, не Later.
- **M9 — активный Stars-рекуррент:** ручное продление не принимать молча (двойной биллинг); предложить сначала отменить автопродление либо показать админу флаг в заявке.
- **Чек (опц.):** принять фото и переслать админу — FSM-состояние ожидания фото после «Я оплатил» (для доверенного MVP можно пропустить).
- **Реквизиты** — из конфига; при смене карты не трогаем код.

## DoD
Пользователь выбирает «карта», видит реквизиты и сумму, жмёт «Я оплатил»; админ получает Approve/Reject **с суммой и тарифом в заявке**; подтверждение создаёт клиента в 3x-ui и выдаёт доступ; отказ уведомляет пользователя (**на его языке**); **повторное подтверждение безопасно во всех комбинациях Approve/Reject (B3); макс. одна активная заявка на юзера (M12); при активном Stars-рекурренте нет двойного биллинга (M9)**.

## Объём
**M.**
