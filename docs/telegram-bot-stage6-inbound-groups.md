# Этап 6 — Наборы инбаундов через тег-группы (детализация до кода)

> **ПЕРЕСМОТР МОДЕЛИ ВЛАДЕНИЯ (2026-07-06, вечер, по фидбеку пользователя):**
> источник истины по группам — **панель**, не бот. (1) Группы создаются/редактируются
> только на странице Groups панели — бот синкает список по API (реестр-таблица
> `inbound_groups` удалена миграцией a3b4c5d6e7f8, CRUD и ретег из бота убраны);
> (2) инбаунд принадлежит группе, если её имя встречается **сегментом** тега
> (`regular-premium-n2-x` → обе группы; сегментный матчинг вместо префикса);
> (3) в боте админ управляет только связкой пользователь↔группы («Группы
> пользователя») и набором групп тарифа (Plans Editor). Метка `client.group`
> зеркалится только для одиночного набора (bulkAdd с новым именем создал бы
> группу — нельзя). Описание ниже по тексту местами отражает ПЕРВУЮ редакцию
> (бот-владелец) — актуальная модель описана здесь и в DEPLOYMENT.md §5a.

> **СТАТУС (2026-07-06): РЕАЛИЗОВАНО** (шаги 0–8, включая админ-UI). Отличия от плана:
> - тарифы к моменту реализации переехали в БД (Plans Editor) — `inbound_groups` добавлен
>   колонкой в `plans` + редактирование в Plans Editor, а не только в plans.json;
> - назначение групп юзеру — в экране «Группы инбаундов» (User Editor оказался заглушкой);
> - шаг 0 выполнен в read-only объёме (токен покрывает `/panel/api/clients/*`, формы
>   get/export, "record not found" — подтверждены живьём; в панели уже есть группы-плейсхолдеры
>   banned/regular/unlimited). МУТИРУЮЩАЯ проба сохранена в scratchpad
>   (`probe_stage6_mutating.py`) — запуск требует отмашки (пишет в боевую панель);
>   ретег-проба отложена по той же причине (рестарт xray);
> - валидация: compileall, msgfmt, импорт-граф и все 14 миграций с нуля — в docker-образе
>   `ghcr.io/ekho/3xui-shop:latest`; юнит-прогон чистой логики (parse/base_tag/diff) зелёный.

## Цель

Клиент получает не один инбаунд (сейчас — всегда `inbounds[0]`, `server_pool.py:111-117`),
а **набор** инбаундов, определяемый его профилем. Состав наборов управляется из панели
(теги инбаундов), принадлежность юзера к набору — ботом (тариф + переопределение).

Принятые решения (2026-07-06):

| Вопрос | Решение |
|---|---|
| Где живёт состав набора | В панели: **префикс тега инбаунда** = имя группы (`regular-443-vless` → группа `regular`) |
| Почему префикс, а не «тег = группа» | `Inbound.Tag` в 3x-ui `gorm:"unique"` — два инбаунда не могут нести одинаковый тег |
| Почему тег, а не remark | Remark утекает в имена конфигов у клиентов (`genRemark` fallback = `remark-email`); Tag — внутренний |
| Профиль юзера | **Набор групп** (union), напр. premium = `["regular","premium"]`; хранится в `User.inbound_groups` |
| Пустой резолв набора | **Fail + алерт админу**; reconciler никогда не отцепляет до нуля |
| Строгость reconciler | Управляет **только инбаундами известных групп**; «чужие» теги не трогает |
| Панельная группа клиента | **Зеркалим**: `client.group` = имя профиля (косметика/bulk-операции в UI панели) |

## Что даёт панель v3.4.2 (проверено по исходникам, тег f3a57d4)

Членство клиент↔инбаунды — первоклассное (`client_inbounds` link-таблица, `ClientRecord`
с глобально-уникальным email). Клиент-центричный API под `/panel/api/clients`
(тот же корень, что уже используемые эндпоинты; токен-авторизация — проверить пробой, шаг 0):

- `GET  /get/:email` → `{client, inboundIds, externalLinks, usedTraffic}` — usedTraffic **агрегирован по всем инбаундам**
- `POST /add` — создать клиента сразу в наборе (`inboundIds: [...]`)
- `POST /update/:email` — панель сама пропагирует правки во все членства
- `POST /del/:email` — удаление отовсюду
- `POST /:email/attach` / `/:email/detach` — `{inboundIds: [...]}`
- `GET  /export` — все клиенты как `{client, inboundIds}` одним вызовом (для reconciler)
- `POST /groups/bulkAdd` — `{group, emails}` (только метка `client.group`, членство не трогает)

Подписка собирается панелью автоматически: sub-сервер включает **все** активные инбаунды,
где есть клиент с данным subId (`getInboundsBySubId`, порядок `SubSortIndex`).
`get_key()` не меняется: `{subscription_url}{vpn_id}` — состав ссылки следует за членством.

py3xui 0.7.0 этих эндпоинтов не знает → сырые вызовы. Прецедент уже в форке:
`server_pool._fetch_subscription_url` ходит в `panel/api/setting/all` через `api.inbound._post`.

## Изменения по файлам

- `app/bot/services/xui_clients.py` — **new**: обёртки клиент-центричного API
- `app/bot/services/inbound_groups.py` — **new**: сервис групп (реестр + резолв + ретег инбаундов)
- `app/bot/services/server_pool.py` — резолвер групп вместо `get_inbound_id`
- `app/bot/services/vpn.py` — переезд на клиент-центричные вызовы
- `app/bot/models/plan.py` + `plans.json` — поле `inbound_groups`
- `app/db/models/user.py` + `app/db/models/inbound_group.py` (**new**) + одна миграция —
  колонка `users.inbound_groups` + таблица-реестр `inbound_groups`
- `app/bot/routers/admin_tools/group_handler.py` — **new**: админ-CRUD групп (+ `keyboard.py`,
  `navigation.py` NavAdminTools, регистрация в `routers/__init__.py`)
- `app/bot/routers/admin_tools/user_handler.py` — редактирование набора групп юзера
- `app/bot/tasks/` — reconciler в существующем кроне
- i18n ru/en — тексты алертов админу + экраны управления группами
- `DEPLOYMENT.md` — конвенция тегов, миграция панели

## Шаги с код-скетчами

### 0. Живая проба API (до любого кода)

Скрипт по образцу `final_validate.py` (scratchpad, panel_probe.env у пользователя):
- токен-доступ к `GET /panel/api/clients/get/:email`, `POST /add`, `attach/detach`, `del`, `export`, `groups/bulkAdd`;
- точные схемы payload'ов снять из `frontend/src/pages/api-docs/endpoints.ts` клона v3.4.2
  (scratchpad `3x-ui-src`) и подтвердить пробой: формат `add` (клиент + inboundIds в одном объекте?),
  что возвращает `export`, пропагация `update/:email` на все инбаунды, поведение `needRestart/pendingNode`;
- probe-клиент в 2 инбаундах → подписка отдаёт ссылки обоих; после `detach` — одного;
- ретег инбаунда через `api.inbound.update` (py3xui): смена `tag` применяется, клиенты инбаунда
  не теряются, коллизия уникальности тега отдаёт внятную ошибку. Панель после тестов чистая.

### 1. Обёртки API — `services/xui_clients.py` (new)

```python
class XuiClientsApi:
    """Клиент-центричный API панели v3.4.2+. py3xui его не покрывает."""
    def __init__(self, api: AsyncApi, host: str): ...

    async def get(self, email: str) -> ClientView | None      # {client, inboundIds, usedTraffic}; 404/"not found" -> None (как P3)
    async def add(self, client: dict, inbound_ids: list[int]) -> None
    async def update(self, email: str, fields: dict) -> None
    async def delete(self, email: str) -> None
    async def attach(self, email: str, inbound_ids: list[int]) -> None
    async def detach(self, email: str, inbound_ids: list[int]) -> None
    async def export(self) -> list[ClientView]                 # для reconciler, 1 вызов на сервер
    async def set_group_label(self, group: str, emails: list[str]) -> None  # groups/bulkAdd
```

Транспорт — тот же приём, что `_fetch_subscription_url` (сессия/токен py3xui, свой URL).
`ClientView` — dataclass: `email, inbound_ids, enable, expiry_time, total_gb, limit_ip, used_traffic, group`.

### 2. Резолвер групп — `server_pool.py`

```python
GROUP_RE = re.compile(r"^([a-z0-9]+)-")   # группа = сегмент тега до первого дефиса

def parse_group(tag: str) -> str | None: ...

async def resolve_group_inbounds(self, api, groups: set[str]) -> dict[str, list[int]]:
    """{группа: [inbound_id]} только для запрошенных групп; инбаунды enable=True."""
    inbounds = await api.inbound.get_list()   # py3xui + P2-патч уже работает
    ...

# get_inbound_id() удалить после переезда vpn.py (git: единственный потребитель — create_client)
```

Известные группы = **реестр** — таблица `inbound_groups` в БД бота (см. шаги 4 и 7);
миграция сеет `regular`, загрузка plans.json авторегистрирует упомянутые там группы
(plans.json деплой-контролируем; опечатка даст группу с 0 инбаундов → сработает политика fail+алерт).
Инбаунд с тегом вне реестра для бота невидим (политика «только известные группы»).
Валидация имени группы: `^[a-z0-9]+$` (дефис — разделитель, запрещён в имени).

### 3. Тарифы — `plans.json` + `models/plan.py`

```json
{ "devices": 3, "traffic_gb": 100,
  "inbound_groups": ["regular", "premium"],
  "prices": { ... } }
```

Поле опционально, дефолт `["regular"]`. В `Plan` — `inbound_groups: list[str]` + валидация
имён на старте (по регэкспу; косяк конфига должен падать при загрузке, не при покупке).

### 4. БД — `User.inbound_groups` + реестр групп, одна миграция

```python
class User:
    inbound_groups: Mapped[str | None]  # JSON-массив строк; None -> дефолт ["regular"]

class InboundGroup(Base):               # models/inbound_group.py (new) — реестр известных групп
    name: Mapped[str]                   # unique, ^[a-z0-9]+$
    created_at: Mapped[datetime]
```

Одна миграция в цепочку после текущего head (проверить `alembic heads`): колонка + таблица
+ seed `regular`. Backfill юзеров не нужен: `None` истолковывается как дефолт, reconciler доцепит.
`users.inbound_groups` заполняется при провижининге (create/extend/change) из плана;
переопределение админом — через User Editor (шаг 7в).

### 5. Слой VPN — `services/vpn.py`

- `create_client`: вместо `get_inbound_id` → `resolve_group_inbounds(api, groups(user))`;
  пустой union → **не создавать**, лог CRITICAL + алерт админу (механизм — как заявки manual card);
  `xui_clients.add(client, inbound_ids)`, в payload сразу `group` = имя профиля
  (профиль = отсортированный join групп, напр. `regular+premium`; или первая группа — решить на пробе,
  что панель допускает в имени группы).
- `update_client` (extend/change): мутации через `xui_clients.update(email, ...)` — панель
  пропагирует по всем членствам; смена тарифа со сменой групп → diff старый/новый набор → attach/detach + `set_group_label`.
- `delete_client`: → `xui_clients.delete(email)`.
- `get_client_data`: → `xui_clients.get(email)` — `used_traffic` агрегирован, `total_gb`/`limit_ip`
  из `ClientRecord` одним вызовом. **Упрощает P4**: `get_client_settings` (чтение settings инбаунда)
  больше не нужен в этом пути; P3-семантика (None на отсутствующем) — внутри `get()`.
- `get_key`, `is_client_exists` — без изменений по смыслу (`is_client_exists` → `get() is not None`).

### 6. Reconciler — `tasks/inbound_reconcile.py` (new, в существующий APScheduler)

```python
async def reconcile_server(connection):
    actual = {c.email: c for c in await xui.export()}          # 1 вызов
    for user in users_on_server_with_active_key(connection.server):
        desired = union(resolve_group_inbounds(...)[g] for g in groups(user))
        if not desired:
            alert_admin(user); continue                        # политика: fail + алерт, не детачить
        have = set(actual[user.email].inbound_ids) if user.email in actual else set()
        managed = ids_of_known_groups                          # чужие теги не трогаем
        to_attach = desired - have
        to_detach = (have & managed) - desired
        ...
```

- Любая ошибка API по юзеру → skip (никаких detach по частичным данным).
- Интервал — как у трафик-крона (или реже: раз в час); порядок в тике: resolve один раз на сервер.
- Дрифт метки `client.group` можно чинить тем же прогоном (`groups/bulkAdd` пачкой) — v2, не обязательно.

### 7. Управление группами — `services/inbound_groups.py` + `routers/admin_tools/group_handler.py` (new)

Паттерн — как `server_handler.py`: enum-константы в `NavAdminTools`, `IsAdmin()`-фильтр,
клавиатуры в `admin_tools/keyboard.py`, FSM для ввода имени.

```python
class InboundGroupService:                       # services/inbound_groups.py
    async def list(self) -> list[GroupInfo]      # имя + инбаундов по серверам + юзеров + планы-ссылки
    async def create(self, name: str) -> None    # валидация ^[a-z0-9]+$, unique; зеркало groups/create в панели
    async def rename(self, old: str, new: str)   # каскад, см. ниже
    async def delete(self, name: str) -> None    # только пустую, см. гарды
    async def add_inbound(self, server, inbound_id, group)     # ретег
    async def remove_inbound(self, server, inbound_id, group)  # снять префикс
```

**7а. Экраны админки** (`GROUP_MANAGEMENT` в меню admin tools):
- список групп → карточка группы: инбаунды по серверам (remark/протокол/порт), число юзеров,
  каким планам назначена;
- в карточке — переключение инбаундов сервера (чекбокс «в группе / нет»), rename, delete;
- создание: FSM-ввод имени → создать в реестре (+ `groups/create` в панели — зеркало-плейсхолдер).

**7б. Ретег инбаунда** = запись в панель через `api.inbound.update` (py3xui 0.7.0, сигнатурная
совместимость подтверждена B6-валидацией; включить в пробу шага 0):
- read-modify-write: `get_list` → снять/поставить префикс в `tag` → `update(inbound_id, inbound)`;
- новый тег: `{group}-{tag_без_старого_известного_префикса}`; снятие — префикс убирается;
- ловить ошибку уникальности тега (коллизия → показать админу, ничего не менять);
- предупреждение в UI: update инбаунда перезапускает xray; если в xray-роутинге есть правила
  по `inboundTag` — они останутся на старом теге (бот их не трогает, ответственность админа);
- после изменения состава — предложить прогнать reconcile сейчас (кнопка), иначе доедет кроном.

**7в. Назначение групп юзеру** — в существующем User Editor (`user_handler.py`):
показать текущий набор, чекбоксы по реестру → записать `users.inbound_groups`
→ немедленный reconcile этого юзера (diff + attach/detach + `set_group_label`).

**Каскады и гарды:**
- **rename**: запрещён, пока группа упомянута в plans.json (сначала правится файл — иначе
  автроегистрация при следующей загрузке воскресит старое имя); иначе: ретег всех инбаундов
  группы на всех серверах → обновить `users.inbound_groups` (замена имени в JSON) → rename
  в реестре → зеркало `groups/rename` в панели. Порядок именно такой: упавший на середине
  каскад чинится повторным rename (идемпотентно) или reconciler'ом.
- **delete**: только если группа не упомянута в plans.json, нет юзеров с ней в наборе и ни один
  инбаунд не несёт её префикс (сначала снять инбаунды в 7а). Иначе — отказ с причиной.
- зеркальные вызовы `groups/*` в панели — best-effort: их падение логируется, но не валит операцию
  (панельная группа — косметика).

### 8. Миграция боевой панели (mole.ekho.name, 8 инбаундов)

1. Задеплоить бота с этапом 6 (реестр после миграции содержит `regular`).
2. Через админку бота (7а) включить нужные инбаунды в `regular` — бот сам ретегнет их по
   конвенции (`regular-443-reality`, ...). Если в xray-роутинге есть правила по `inboundTag` —
   поправить руками синхронно (бот предупредит).
3. Существующие юзеры: `inbound_groups=None` → дефолт `regular`, первый прогон reconciler
   доцепит их ко всем `regular-*` инбаундам (сейчас они только в первом).
4. Понаблюдать лог первого прогона (объём attach = юзеры × (N_regular − 1)).

## Крайние случаи

- **Юзер без сервера / не approved** — reconciler пропускает (фильтр как в m6 из этапа 3).
- **Сервер offline** — пропуск целиком, никакой сходимости по недоступному серверу.
- **Тег переименовали, группа исчезла** — resolve пустой → алерт, юзеры остаются где были (гарантия «не до нуля»).
- **Админ руками прицепил юзера к инбаунду с «чужим» тегом** — бот не трогает (known-groups-only).
- **Параллельность покупка/reconciler** — attach/detach идемпотентны, порядок не важен; CAS не нужен.
- **`email` глобально уникален в v3.4.2** (`ClientRecord.Email uniqueIndex`) — форку это в плюс
  (email=tg_id), суффиксование `user1-in2` из старых 3x-ui не требуется.
- **Трафик-пороги крона** (этап 3) — перевести на `used_traffic` из `get()`: агрегат по набору,
  иначе пороги считают только один инбаунд.
- **Ретег наперегонки с правкой в панели** — read-modify-write без CAS, last-write-wins;
  операции админа редки, риск принят. При ошибке update — состав не меняется, показать админу.
- **Каскад rename упал на середине** — часть инбаундов ретегнута, часть нет: повторный rename
  идемпотентен (префиксы обрабатываются по одному), юзеры с промежуточным состоянием сойдутся
  reconciler'ом после завершения.
- **Группа удалена, а тег остался** (правили в панели мимо бота) — группа выпала из реестра →
  инбаунды с её префиксом становятся «чужими», бот их не трогает; юзеры с ней в наборе →
  резолв пустой по этой группе → union может стать пустым → сработает fail+алерт.

## DoD

- Проба (шаг 0) зелёная на v3.4.2: токен, add-в-набор, attach/detach, export, ретег инбаунда,
  подписка отражает набор.
- Юнит: `parse_group`/резолвер/diff reconciler/каскад rename (чистые функции, без сети).
- `compileall` зелёный, i18n msgfmt ru/en OK (алерты + экраны групп).
- Пустой резолв не создаёт/не рвёт клиента, алерт уходит.
- Админ-CRUD: создать группу → включить инбаунд (тег меняется в панели) → назначить юзеру →
  reconcile прицепил; delete/rename упираются в гарды с внятной причиной.
- DEPLOYMENT.md: конвенция тегов, `inbound_groups` в plans.json, порядок миграции панели.

## Объём

~4 новых модуля (`xui_clients.py`, `inbound_groups.py`, `inbound_reconcile.py`,
`group_handler.py`) + модель `inbound_group.py` + проба, правки в 8 файлах (`server_pool.py`,
`vpn.py`, `plan.py`, `user.py`+миграция, `user_handler.py`, `navigation.py`,
`admin_tools/keyboard.py`, регистрация роутера/таска), i18n ×2.
Заметно больше этапа 3 — ближе к этапам 4+5 вместе; админ-UI (шаг 7) можно выделить
во вторую итерацию, ядро (шаги 0–6, 8) самодостаточно при ручном тегировании в панели.
