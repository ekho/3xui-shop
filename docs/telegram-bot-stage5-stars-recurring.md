# Этап 5 — Авто-рекуррент Telegram Stars (детализация до кода)

_База: форк `snoups/3xui-shop`. Пути реальные._

> **⚠️ Правки по аудиту 2026-07-04 (см. раздел 2A мастер-плана).** Обновлены под: **B4** (`stars_charge_id` — только первого платежа `is_first_recurring`, иначе отмена падает с `CHARGE_ID_INVALID`), **B2** (идемпотентность: `Transaction.create` при дубле возвращает `None`, не исключение), **B1** (рекуррентное списание у не-approved юзера → рефанд/аудит), **B5** (ретро-правка крона Этапа 3 + поле `stars_expires_at`), **m5** (отмена без залипания флага), **m4** (кнопка «Возобновить»).

## Цель

Если первая оплата была в Stars — подписка продлевается автоматически (родная подписка Telegram Stars). Пользователь может отменить автопродление из профиля; при сбое/отмене — фолбэк на обычные напоминания (Этап 3).

## Что уже есть

- `telegram_stars.py` `create_payment` → `bot.create_invoice_link(..., currency=XTR)` — **разовый платёж** (без `subscription_period`).
- `payment_handler.py`: `pre_checkout_handler` (отвечает ok при наличии payload) и `successful_payment` (распаковывает `SubscriptionData`, создаёт `Transaction(COMPLETED)`, вызывает `stars_gateway.handle_payment_succeeded`). То есть разовый Stars-флоу полностью рабочий — надстраиваем рекуррент поверх него.

## Ограничение Telegram

Подписки Stars имеют **фиксированный период 30 дней** (`subscription_period = 2592000`, другое значение API не примет). Значит авто-рекуррент делаем **только для тарифа 30 дней**; для 60/180/365 Stars остаётся разовым.

## Изменения по файлам

| Файл | Что делаем |
|---|---|
| `app/bot/payment_gateways/telegram_stars.py` | `subscription_period=2592000` для 30-дн. тарифа |
| `app/bot/routers/subscription/payment_handler.py` | в `successful_payment` — ветка `is_recurring` → extend; **B4** charge id только первого платежа; **B2** дедуп по `Transaction.create is None`; **B1** рефанд у не-approved |
| `app/db/models/user.py` (+ миграция) | + `stars_charge_id`, `is_stars_auto_renew`, **`stars_expires_at` (B5)** |
| `app/bot/tasks/subscription_expiry.py` | **B5 (ретро-правка Этапа 3): фильтр `is_stars_auto_renew`, детект лапса по `stars_expires_at`** |
| `app/bot/routers/profile/*` | статус автопродления + кнопки «Отменить» **и «Возобновить» (m4)** |
| `app/bot/locales/*` | тексты |

---

## Шаги с код-скетчами

### 1. Рекуррентный инвойс — `telegram_stars.py`
```python
STARS_SUBSCRIPTION_PERIOD = 2592000  # 30 дней, единственное допустимое значение

async def create_payment(self, data: SubscriptionData) -> str:
    amount = 1 if await IsDev()(user_id=data.user_id) else int(data.price)
    prices = [LabeledPrice(label=self.currency.code, amount=amount)]
    kwargs = dict(
        title=..., description=..., prices=prices,
        payload=data.pack(), currency=self.currency.code,
    )
    # рекуррент только для месячного тарифа и если не продление/смена
    if data.duration == 30 and not data.is_extend and not data.is_change:
        kwargs["subscription_period"] = STARS_SUBSCRIPTION_PERIOD
    return await self.bot.create_invoice_link(**kwargs)
```

### 2. Обработка списаний — `payment_handler.py` (`successful_payment`)
Рекуррентное списание Telegram присылает `successful_payment` с `is_recurring=True` и тем же `invoice_payload`. Payload несёт исходный `SubscriptionData` (`is_extend=False`), поэтому для рекуррента **принудительно продлеваем**, а не создаём.
```python
sp = message.successful_payment
data = SubscriptionData.unpack(sp.invoice_payload)

# B1: рекуррентное списание приходит даже у не-approved юзера (reject после покупки) —
#     деньги списаны, доступа нет: рефанд (для рекуррента заодно отменяет подписку) + аудит.
if user.approval_status != ApprovalStatus.APPROVED and not await IsAdmin()(user_id=user.tg_id):
    await Transaction.create(session=session, tg_id=user.tg_id, subscription=data.pack(),
                             payment_id=sp.telegram_payment_charge_id, status=TransactionStatus.CANCELED)
    await bot.refund_star_payment(user.tg_id, sp.telegram_payment_charge_id)
    await User.update(session, tg_id=user.tg_id, is_stars_auto_renew=False)
    # уведомить юзера/админов
    return

# dev-рефанд только для разовых тестов, не для рекуррента
if await IsDev()(user_id=user.tg_id) and not sp.is_recurring:
    await bot.refund_star_payment(user.tg_id, sp.telegram_payment_charge_id)

if sp.is_recurring and not sp.is_first_recurring:
    data.is_extend = True                      # авто-продление
    data.state = NavSubscription.PAY_TELEGRAM_STARS

# B2: идемпотентность — Transaction.create при дубле payment_id возвращает None (НЕ исключение).
#     Проверяем ДО User.update и провижининга, иначе повторная доставка апдейта = +30 дней бесплатно.
txn = await Transaction.create(session=session, tg_id=user.tg_id, subscription=data.pack(),
                               payment_id=sp.telegram_payment_charge_id, status=TransactionStatus.COMPLETED)
if txn is None:
    logger.warning(f"duplicate stars payment {sp.telegram_payment_charge_id}, skip")
    return

# B4: charge_id принимает editUserStarSubscription ТОЛЬКО от первого платежа подписки.
#     Перезапись рекуррентами/разовыми ломает отмену (CHARGE_ID_INVALID) и «теряет» подписку.
if sp.is_first_recurring:
    await User.update(session, tg_id=user.tg_id, stars_charge_id=sp.telegram_payment_charge_id)
if sp.is_recurring:                            # первый И рекурренты — подписка жива (самовосстановление)
    await User.update(session, tg_id=user.tg_id, is_stars_auto_renew=True)
if sp.subscription_expiration_date:            # B5: точный сигнал для крона (детект лапса/отмены)
    await User.update(session, tg_id=user.tg_id, stars_expires_at=sp.subscription_expiration_date)
# разовые Stars-платежи (is_extend/is_change/тариф≠30дн; sp.is_recurring отсутствует) НЕ трогают
# ни stars_charge_id, ни is_stars_auto_renew.

gateway = gateway_factory.get_gateway(NavSubscription.PAY_TELEGRAM_STARS)
await gateway.handle_payment_succeeded(payment_id=sp.telegram_payment_charge_id)
```
> `_on_payment_succeeded` уже роутит по `data.is_extend` → `extend_subscription` (продление срока + сброс трафика из Этапа 3). Для первого платежа `is_recurring=True, is_first_recurring=True` → идём как обычное создание.
> **B2 (несимметрия со шлюзами):** Stars-транзакция создаётся сразу `COMPLETED`, поэтому CAS-дедуп из Этапа 4 (`PENDING→COMPLETED`) для Stars НЕ применять — здесь дедуп по возврату `None` из `Transaction.create`. CAS остаётся для Cryptomus/manual.

### 3. Модель — `db/models/user.py` (+ миграция)
```python
stars_charge_id: Mapped[str | None] = mapped_column(String(64), nullable=True)
is_stars_auto_renew: Mapped[bool] = mapped_column(default=False, nullable=False)
stars_expires_at: Mapped[datetime | None] = mapped_column(nullable=True)  # B5: детект лапса/отмены без вебхука
```
Миграция автогенерацией Alembic (как в Этапе 2).

### 4. Профиль — отмена/возобновление автопродления
Показывать «Автопродление (Stars): вкл/выкл»; кнопки «Отменить» и «Возобновить»:
```python
@router.callback_query(F.data == NavProfile.CANCEL_STARS_SUB)
async def cancel_stars_sub(callback, user, session, bot):
    if not user.stars_charge_id:
        await callback.answer(_("profile:stars:no_sub")); return
    # m5: подписка могла лапснуть/быть отменённой/зарефанженной → edit_user_star_subscription бросит
    #     TelegramBadRequest (CHARGE_ID_INVALID). Флаг сбрасываем ЛОКАЛЬНО в любом случае,
    #     иначе is_stars_auto_renew навсегда залипнет True и кнопка отмены не работает.
    try:
        await bot.edit_user_star_subscription(
            user_id=user.tg_id, telegram_payment_charge_id=user.stars_charge_id, is_canceled=True)
    except TelegramBadRequest as e:
        logger.warning(f"cancel stars {user.tg_id}: {e}")
    await User.update(session, tg_id=user.tg_id, is_stars_auto_renew=False)
    await callback.answer(_("profile:stars:canceled"))

# m4: отмена ботом (is_canceled=True) ставит bot_canceled — юзер НЕ реактивирует подписку сам
#     из настроек Telegram. Даём кнопку «Возобновить» (работает, пока текущий период ещё активен).
@router.callback_query(F.data == NavProfile.RESUME_STARS_SUB)
async def resume_stars_sub(callback, user, session, bot):
    if not user.stars_charge_id:
        await callback.answer(_("profile:stars:no_sub")); return
    try:
        await bot.edit_user_star_subscription(
            user_id=user.tg_id, telegram_payment_charge_id=user.stars_charge_id, is_canceled=False)
        await User.update(session, tg_id=user.tg_id, is_stars_auto_renew=True)
        await callback.answer(_("profile:stars:resumed"))
    except TelegramBadRequest:
        # период уже истёк — реактивация невозможна, вести на разовую покупку/напоминания
        await callback.answer(_("profile:stars:resume_failed"), show_alert=True)
```
(Требуется `NavProfile.CANCEL_STARS_SUB`, `NavProfile.RESUME_STARS_SUB` в навигации и кнопки в клавиатуре профиля. В тексте отмены предупредить: доступ сохранится до конца оплаченного периода, повторно включить можно кнопкой «Возобновить», пока период не истёк.)

### 5. Фолбэк при сбое/отмене (сценарий L62)
- Если рекуррентное списание не прошло, Telegram сам уведомляет пользователя и подписка лапсится → далее срабатывают напоминания Этапа 3 (по сроку/трафику) с кнопкой ручного продления.
- **B5 — ретро-правка крона Этапа 3** (внести `tasks/subscription_expiry.py` в файлы этого этапа): (1) при `user.is_stars_auto_renew` крон пропускает пороги по сроку (автосписание продлит само — иначе рекуррент-юзер каждый месяц получает ложное «истекает» и может заплатить второй раз, M9/B5); (2) явного вебхука «подписка отменена в Telegram» нет → детект по `stars_expires_at`: если `now > stars_expires_at + грейс(12–24ч)` и нового рекуррентного `successful_payment` не было — снять `is_stars_auto_renew` и вернуть юзера в обычные напоминания. Это же поле закрывает и отмену юзером в Telegram без вебхука (не Later, а часть Этапа 5).

## Проверки совместимости
- `create_invoice_link(subscription_period=...)` и `edit_user_star_subscription(...)` доступны в **aiogram 3.15+** (в форке `aiogram ^3.15`). Свериться с актуальной сигнатурой при реализации.
- Тест: с `IsDev` цена = 1 звезда; проверить первое списание (создание) и последующее (продление). Учесть, что рефанд рекуррента отменяет подписку — для теста рекуррента рефанд отключён.

## Крайние случаи
- **Не 30-дн. тариф** → Stars разовый (без подписки); продление — напоминаниями.
- **Повторная доставка апдейта (B2)** — дедуп по `Transaction.create(...) is None` ДО провижининга (не полагаться на «сохранит уникальный id»: create молча вернёт None, а `handle_payment_succeeded` без проверки продлит второй раз).
- **B4 — charge_id:** `editUserStarSubscription` принимает id только первого платежа (`is_first_recurring`); рекурренты/разовые его не трогают.
- **B1 — reject/не-approved при активном рекурренте:** рекуррентное списание рефандится (заодно отменяет подписку) + аудит-транзакция; см. ветку в §2 и отмену при reject в Этапе 2.
- **m5 — лапснувшая/отменённая подписка при отмене из профиля:** `try/except TelegramBadRequest`, флаг сбрасывать локально в любом случае.
- **Пользователь отменил в Telegram / лапс (B5)** — детект по `stars_expires_at + грейс`, снять флаг, вернуть в напоминания.
- **Смена тарифа при активной подписке** — сначала отменить старую Stars-подписку, затем оформить новую (первый платёж новой придёт с `is_first_recurring=True` → корректно перезапишет charge id).
- **M9 — ручное/крипто-продление при активном рекурренте** (Этап 4) — не принимать молча, иначе двойной биллинг.

## DoD
Покупка 30-дн. тарифа за Stars создаёт автообновляемую подписку; рекуррентное списание автоматически продлевает (срок + сброс трафика); пользователь отменяет автопродление из профиля (**и «Возобновить», пока период активен; без залипания флага, m4/m5**); тарифы ≠30 дн. остаются разовыми; сбой/отмена уводят в напоминания; **charge id первого платежа не затирается рекуррентами (B4); повторная доставка апдейта не даёт второго продления (B2); reject при активном рекурренте → рефанд/отмена, деньги не теряются (B1)**.

## Объём
**M.**
