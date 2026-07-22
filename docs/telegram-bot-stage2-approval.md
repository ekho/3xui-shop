# Этап 2 — Апрув-гейт (детализация до кода)

_База: форк `snoups/3xui-shop`. Все пути — реальные из репозитория._

> **⚠️ Правки по аудиту 2026-07-04 (см. раздел 2A мастер-плана).** Сниппеты ниже обновлены под блокеры **B1** (гейт не должен глотать платёжные апдейты; reject отменяет Stars-рекуррент), **M3** (регистрация роутера — в `routers/__init__.py`), **M5** (локаль получателя), **M6** (уведомление админов не по `is_new_user`), **m3** (pending-юзер не остаётся без ответа; точный разбор `/start`).

## Цель

Открытая регистрация: любой может нажать `/start`, но покупка/триал/меню доступны **только после подтверждения админом**. Реализуем централизованно через middleware (как `MaintenanceMiddleware`), не «латая» каждый хендлер.

## Подход

- Новое поле `approval_status` у `User` (pending/approved/rejected).
- `ApprovalMiddleware` — пропускает только approved и админов; остальным показывает «ожидайте», блокируя любые действия. Ставится **после** `DBSessionMiddleware` (нужен уже загруженный `user`).
- В `/start`: авто-апрув админов, уведомление админов с кнопками Approve/Reject для новых, ветки pending/rejected.
- Хендлер admin-действий approve/reject → меняет статус, уведомляет пользователя.
- Флаг `SHOP_APPROVAL_REQUIRED` для включения/выключения гейта.

---

## Изменения по файлам

| Файл | Что делаем |
|---|---|
| `app/bot/utils/constants.py` | + `class ApprovalStatus(Enum)` |
| `app/db/models/user.py` | + колонка `approval_status`, **+ `approval_requested_at` (M6, дедуп уведомлений)**, + `User.update(...)` (если нет) |
| `app/db/migration/versions/xxxx_add_approval.py` | миграция: колонки + backfill существующих в `approved` |
| `app/config.py` + `.env` | + `SHOP_APPROVAL_REQUIRED` в `ShopConfig` |
| `app/bot/middlewares/approval.py` (new) | `ApprovalMiddleware` |
| `app/bot/middlewares/__init__.py` | регистрация ApprovalMiddleware после DBSession |
| `app/bot/utils/navigation.py` | (опц.) через `CallbackData`-фабрику вместо enum |
| `app/bot/routers/main_menu/handler.py` | авто-апрув админа, уведомление админов, ветки pending/rejected |
| `app/bot/routers/admin_tools/approval_handler.py` (new) | хендлер approve/reject + клавиатура |
| `app/bot/routers/__init__.py` | **M3: реальная регистрация — `dispatcher.include_routers(admin_tools.approval_handler.router, ...)` здесь**, а не в `admin_tools/__init__.py` (там только `from . import ...`) |
| `app/bot/locales/*/LC_MESSAGES/*.po` | тексты (ru/en) |

---

## Шаги с код-скетчами

Код иллюстративный — привести к стилю проекта (i18n `_()`, логирование).

### 1. Enum статуса — `constants.py`
```python
class ApprovalStatus(Enum):
    PENDING = "pending"
    APPROVED = "approved"
    REJECTED = "rejected"
```

### 2. Модель `User` — `db/models/user.py`
```python
from app.bot.utils.constants import ApprovalStatus
from sqlalchemy import Enum as SAEnum

approval_status: Mapped[ApprovalStatus] = mapped_column(
    SAEnum(ApprovalStatus), default=ApprovalStatus.PENDING, nullable=False
)
```
Если нет `User.update` — добавить по образцу `Transaction.update`:
```python
@classmethod
async def update(cls, session: AsyncSession, tg_id: int, **kwargs) -> None:
    await session.execute(update(User).where(User.tg_id == tg_id).values(**kwargs))
    await session.commit()
```

### 3. Миграция Alembic
```bash
docker compose exec bot alembic revision --autogenerate -m "add user approval_status"
# проверить, затем:
docker compose exec bot alembic upgrade head
```
В миграции для существующих установок backfill (чтобы не заблокировать текущих клиентов):
```python
op.execute("UPDATE users SET approval_status = 'approved'")
```

### 4. Флаг конфигурации — `config.py` + `.env`
В `ShopConfig` добавить `APPROVAL_REQUIRED: bool`, распарсить из env (по образцу `PAYMENT_*`).
```
# .env
SHOP_APPROVAL_REQUIRED=True
```

### 5. `ApprovalMiddleware` — `middlewares/approval.py`
**B1/m3:** гейт висит на `dispatcher.update.middleware` и видит ВСЕ типы апдейтов, а `DBSessionMiddleware` кладёт `user` и для `PreCheckoutQuery`. Молчаливый `return None` для не-`Message`/не-`CallbackQuery` глотает `pre_checkout_query` (Telegram отменяет платёж через 10 с) и `successful_payment` (это `Message` без `text` → потеря денег при рекурренте Stars). Переходим на **явную обработку типов**:
```python
from aiogram.types import Message, CallbackQuery, PreCheckoutQuery

class ApprovalMiddleware(BaseMiddleware):
    def __init__(self, config: Config) -> None:
        self.config = config

    async def __call__(self, handler, event, data):
        user = data.get("user")
        if not self.config.shop.APPROVAL_REQUIRED or user is None:
            return await handler(event, data)
        if user.approval_status == ApprovalStatus.APPROVED or await IsAdmin()(user_id=user.tg_id):
            return await handler(event, data)

        ev = event.event
        # B1: деньги уже списаны — successful_payment пропускаем ВСЕГДА
        #     (в хендлере successful_payment — ветка рефанда для не-approved, см. Этап 5)
        if isinstance(ev, Message) and ev.successful_payment:
            return await handler(event, data)
        # B1: pre_checkout нельзя ронять молча — ответить отказом (error_message обязателен при ok=False)
        if isinstance(ev, PreCheckoutQuery):
            await ev.answer(ok=False, error_message=_("approval:notice:pending"))
            return
        # m3: точный разбор /start (Command("start") не матчит /startXXX; диплинк /start ref сохраняем)
        if isinstance(ev, Message) and (ev.text or "").split(maxsplit=1)[:1] == ["/start"]:
            return await handler(event, data)
        # m3: любое другое сообщение — не тишина, показать статус (по образцу MaintenanceMiddleware)
        if isinstance(ev, Message):
            notification = data.get("services").notification  # или NotificationService
            await notification.notify_by_message(
                message=ev, text=_("approval:notice:pending"), duration=...)
            return  # + опц. redis-троттлинг approval:pending:notice:{tg_id}, TTL ~30–60с
        if isinstance(ev, CallbackQuery):
            await ev.answer(_("approval:notice:pending"), show_alert=True)
        return  # стоп
```
Регистрация — `middlewares/__init__.py`, **после** `DBSessionMiddleware`:
```python
DBSessionMiddleware(session),
ApprovalMiddleware(config),   # <-- добавить последним
```
(Важно: у stock-проекта `MaintenanceMiddleware` стоит до DBSession, т.к. не требует БД. Апрув требует `user`, поэтому строго после DBSession.)

### 6. Стартовый флоу — `routers/main_menu/handler.py`
В начале `command_main_menu`, после удаления прошлого сообщения:
```python
# авто-апрув админов
if await IsAdmin()(user_id=user.tg_id) and user.approval_status != ApprovalStatus.APPROVED:
    await User.update(session, tg_id=user.tg_id, approval_status=ApprovalStatus.APPROVED)
    user.approval_status = ApprovalStatus.APPROVED

if config.shop.APPROVAL_REQUIRED and user.approval_status != ApprovalStatus.APPROVED \
        and not await IsAdmin()(user_id=user.tg_id):
    # M6: НЕ завязывать на is_new_user — User создаётся на ЛЮБОМ первом апдейте (не только /start);
    #     если первый апдейт не /start, гейт его заблокирует, is_new_user сгорит,
    #     и уведомление админам не уйдёт никогда → юзер навсегда в pending.
    #     Слать при PENDING с дедупликацией, чтобы повторный /start не спамил.
    if user.approval_status == ApprovalStatus.PENDING and user.approval_requested_at is None:
        await notify_admins_new_request(message.bot, config, user)  # см. ниже
        await User.update(session, tg_id=user.tg_id, approval_requested_at=datetime.now(timezone.utc))
    text = _("approval:message:pending") if user.approval_status == ApprovalStatus.PENDING \
        else _("approval:message:rejected")
    await message.answer(text)
    return
# ... существующий код построения меню
```
> **M6:** добавить в модель `User` поле `approval_requested_at: Mapped[datetime | None]` (или redis-ключ по образцу `tasks/subscription_expiry`); сбрасывать его при approve/reject и при повторном запросе (`rejected → «запросить снова» → pending`), чтобы новое уведомление ушло.
Хелпер уведомления админов:
```python
from app.bot.routers.admin_tools.approval_handler import approval_keyboard

async def notify_admins_new_request(bot, config, user):
    admin_ids = set(config.bot.ADMINS) | {config.bot.DEV_ID}
    text = _("approval:admin:new_request").format(
        name=user.first_name, username=user.username or "-", tg_id=user.tg_id)
    for admin_id in admin_ids:
        try:
            await bot.send_message(admin_id, text, reply_markup=approval_keyboard(user.tg_id))
        except Exception:
            pass
```

### 7. Callback + клавиатура — `routers/admin_tools/approval_handler.py` (new)
```python
from aiogram.filters.callback_data import CallbackData

class ApprovalCallback(CallbackData, prefix="approval"):
    action: str   # "approve" | "reject"
    user_id: int

def approval_keyboard(user_id: int):
    b = InlineKeyboardBuilder()
    b.button(text=_("approval:button:approve"), callback_data=ApprovalCallback(action="approve", user_id=user_id))
    b.button(text=_("approval:button:reject"), callback_data=ApprovalCallback(action="reject", user_id=user_id))
    return b.as_markup()

router = Router(name=__name__)

@router.callback_query(ApprovalCallback.filter(), IsAdmin())
async def on_approval(callback, callback_data: ApprovalCallback, session, bot, i18n):
    new_status = ApprovalStatus.APPROVED if callback_data.action == "approve" else ApprovalStatus.REJECTED
    target = await User.get(session, tg_id=callback_data.user_id)
    await User.update(session, tg_id=callback_data.user_id,
                      approval_status=new_status, approval_requested_at=None)  # M6: сброс метки

    # B1: reject при активном Stars-рекурренте обязан отменить подписку, иначе Telegram
    #     продолжит списывать звёзды за сервис, которым юзер пользоваться не может.
    if new_status == ApprovalStatus.REJECTED and getattr(target, "is_stars_auto_renew", False) \
            and target.stars_charge_id:
        try:
            await bot.edit_user_star_subscription(
                user_id=target.tg_id, telegram_payment_charge_id=target.stars_charge_id, is_canceled=True)
        except TelegramBadRequest as e:
            logger.warning(f"cancel stars sub on reject {target.tg_id}: {e}")  # могла быть уже отменена
        await User.update(session, tg_id=target.tg_id, is_stars_auto_renew=False)

    # M5: текст юзеру рендерить в ЕГО локали (SimpleI18nMiddleware ставит локаль админа-инициатора)
    locale = (target.language_code if target else None) or DEFAULT_LANGUAGE
    with i18n.use_locale(locale):
        user_text = _("approval:user:granted") if new_status == ApprovalStatus.APPROVED else _("approval:user:denied")
    try:
        await bot.send_message(callback_data.user_id, user_text)
    except TelegramForbiddenError:
        pass  # юзер заблокировал бота — не роняем хендлер до callback.answer()
    # обновить сообщение админу (кто и что решил) — в локали текущего апдейта (админа), это ок
    await callback.message.edit_text(callback.message.text + "\n\n" +
        _("approval:admin:done").format(status=new_status.value))
    await callback.answer()
```
**M3 — регистрация роутера.** `routers/admin_tools/__init__.py` содержит только `from . import ...`; реальная регистрация — в `app/bot/routers/__init__.py::include()` через `dispatcher.include_routers(...)`. Добавить `admin_tools.approval_handler.router` именно туда (плюс `from . import approval_handler` в пакет). Иначе кнопки Approve/Reject мертвы (callback без ответа).

### 8. i18n-ключи (ru/en)
`approval:message:pending`, `approval:message:rejected`, `approval:notice:pending`, `approval:admin:new_request`, `approval:admin:done`, `approval:user:granted`, `approval:user:denied`, `approval:button:approve`, `approval:button:reject`.

---

## Крайние случаи

- **Спам админам (M6):** уведомляем при `approval_status==PENDING` с дедупликацией по `approval_requested_at` (НЕ по `is_new_user` — иначе теряется, если первый апдейт не `/start`); повторный `/start` запрос не дублирует.
- **Гонка двух админов:** второй клик — статус уже выставлен (идемпотентно); сообщение показывает, кто решил.
- **Rejected → повторный `/start`:** показываем «отказано»; опц. кнопка «запросить снова» (переводит в pending, сбрасывает `approval_requested_at` → новое уведомление).
- **Reject при активном Stars-рекурренте (B1):** отменить подписку `edit_user_star_subscription(is_canceled=True)` + сбросить `is_stars_auto_renew`, иначе Telegram продолжит списывать.
- **Платёжные апдейты (B1):** `pre_checkout_query` от не-approved → `answer(ok=False)`; `successful_payment` → всегда пропустить в хендлер (рефанд/аудит — на стороне хендлера, Этап 5).
- **Существующие клиенты (для не-fresh БД):** backfill в `approved` в миграции.
- **Гейт мешает тестам:** `SHOP_APPROVAL_REQUIRED=False` → поведение как в stock.

## Тест-план (DoD)

1. Новый `/start` → статус `pending`, админам пришли Approve/Reject; покупка/триал/меню недоступны, показывается «ожидайте».
2. Approve → пользователю пуш «доступ открыт» (**на его языке, M5**), появляется меню и триал; покупка работает.
3. Reject → пользователю «отказано», действия по-прежнему заблокированы; **если был активен Stars-рекуррент — подписка отменена (B1)**.
4. Админ/дев на `/start` → авто-approved, гейт не мешает.
5. `SHOP_APPROVAL_REQUIRED=False` → гейт отключён.
6. **B1:** approve → покупка 30-дн. Stars → reject → следующее рекуррентное списание обрабатывается (рефанд/аудит), подписка отменена, деньги не теряются.
7. **M6:** первый апдейт юзера — не `/start` (напр. текст) → затем `/start`: уведомление админам всё равно приходит.
8. **m3:** pending-юзер пишет боту произвольный текст → получает «ожидайте», а не тишину.

## Объём и порядок коммитов

Оценка: **L (~1–2 дня)**. Порядок:
1. `ApprovalStatus` + колонка + миграция + backfill.
2. Флаг конфигурации.
3. `ApprovalMiddleware` + регистрация.
4. Стартовый флоу + уведомление админов.
5. Хендлер approve/reject + клавиатура + include роутера.
6. i18n-тексты.
7. Прогон тест-плана.
