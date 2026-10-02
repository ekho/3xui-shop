# С08 — профили доступа и VPN-ban; обязательный reset С41

Статус: автономная спецификация по разрешению пользователя; после С48/С09 и executor С07.
Основание: [роадмап](../../roadmaps/2026-10-01-platform-roadmap.md),
`app/bot/services/inbound_groups.py`, `vpn.py`, `tasks/unlimited_reset.py`.

## Результат

Оператор выбирает один профиль regular/euru/unlimited и независимо включает
или снимает VPN-ban. Account restriction и support-ban не меняются.
UUID/sub_id/panel_key/assigned server сохраняются; чужие clients и unmanaged
inbounds не изменяются. Старые unknown groups — ошибка со сверкой, не
тихий переход на regular. Новый/пустой неlegacy profile по умолчанию regular.

## Семантика

- Regular/euru заменяют только access profile и управляемые memberships,
  сохраняя текущий срок/devices/traffic и ban overlay. Без panel client профиль
  сохраняется для будущего provisioning; новый trial/bonus использует его.
- Unlimited назначает текущую hidden unlimited revision из С09, expiry=0,
  её devices/traffic, memberships unlimited+regular и прежний ban. Отсутствующий
  plan, конфликт devices или неподготовленный С41 честно блокируют действие.
  Выдача без существующего клиента использует сохранённую identity на
  разрешённой configured панели; второй trial Grant не создаётся.
- Снятие unlimited на regular или euru возвращает configured starter trial
  с выбранным профилем, now+trial period и reset; сохраняет ban. Без клиента
  меняется сохранённый профиль, без фиктивной выдачи.
- Ban сохраняет memberships/лимиты и выключает native client. Unban явно
  включает его; finite expired/exhausted состояние по-прежнему ограничивает
  реальную доступность. Ни смена профиля, ни reset не снимают ban неявно.
- Desired inbound IDs получаются по точным сегментам известных тегов,
  unlimited наследует regular. Empty набор или неизвестные/изменённые managed
  memberships требуют отказа/сверки; arbitrary tags не создаются и не правятся.

Профиль и операция сохраняются через общий persistent executor С07; новая
параллельная реализация panel writes не добавляется. Конкурирующие profile,
ban, compensation, reset и initial provisioning сериализуются одним account
ownership; role revoke и ban во время reset не обходятся. Частично применённый
profile или потерянный ответ становятся needs_review; БД не заявляет fully
applied до проверки native лимитов/memberships/enabled/identity.

## С41 как обязательная зависимость

Unlimited не включается с обещанием «reset позже». В этом же выпуске
реализуется минимальный ежемесячный reset, необходимый для полного С08.
Используется существующий River и стандартные Go time/timezone, без нового
scheduler dependency. Config сохраняет принятую timezone при переносе;
дефолт UTC, неверная timezone не запускает job в случайном часовом поясе.

В первый день месяца в00:00 в configured timezone создаётся одна операция
на account/calendar period, включая унаследованные regular inbounds. Уникальность
хранится в БД, а не только в памяти процесса. Restart/два процесса не делают
второй reset. Startup догоняет пропущенный старт только в legacy grace3600s;
не сбрасывает трафик старых месяцев. После очереди операция остаётся связанной
с конкретным периодом. Ban/unlimited проверяются снова непосредственно перед
записью; забаненные и уже вышедшие из unlimited пропускаются.
Reset сохраняет expiry=0, лимиты, profile, identities и не продлевает срок.
Panel enable side effect обрабатывается тем же ban guard, что в С07.
Неоднозначный reset — needs_review с явной операторской сверкой; River retry
не обнуляет новые счётчики вслепую. Системный actor и period сохраняются в audit.

## API и UI

С07 access-operations расширяется typed kind set_profile/set_vpn_ban:
`profile` либо `vpn_banned` и обязательный reason; чужие поля запрещены.
Session UUID, live operator role, Idempotency-Key, Origin/CSRF обязательны.
Повтор того же desired state с новым ключом не создаёт лишний эффект; replay
возвращает прежнюю operation, другой body с тем же ключом —409.
Operator card и клиентский С03 показывают подтверждённый profile и VPN-ban
отдельно от account/support restrictions, незавершённые writes видимы.
RU/EN, mobile375px, keyboard focus/labels, disabled inputs, confirmation
для unlimited/ban/reset, сохранение reason при error обязательны.

## Приёмка

1. regular→euru→regular меняет только managed memberships/profile; native
   identity, срок/лимиты/трафик и ban сохранены; unmanaged inbounds не тронуты.
2. Unlimited использует hidden revision, expiry=0 и inherited regular;
   revoke применяет starter trial/reset/выбранный profile с прежним ban.
3. Ban/unban отдельны от account/support restriction; profile/reset/месячный
   job не разбанивают, expired/exhausted не выдаются за active.
4. No-client profile/revoke сохраняют корректное будущее намерение;
   missing/empty/unknown groups и неподготовленный С41 не дают тихой выдачи.
5. Idempotency/concurrent profile-ban-comp-reset/provision/restart и partial
   attach/detach/re-enable проверены общим executor; needs_review не скрывается.
6. С41: разные timezone и границы месяцев, startup≤3600s/после grace,
   double process/restart, unique period, banned/non-unlimited skip и потеря
   ответа проверены; нет второго reset или автоматического продления срока.
7. Operator authorization/input/CSRF и UI status/pending/error/retry,
   RU/EN/mobile/keyboard пройдены; С03 читает применённое текущее состояние.
8. Actual own Docker3X-UI3.7.0/API/browser подтверждают profiles/ban/reset,
   постоянные identities и восстановление; прежние сценарии не сломаны.
