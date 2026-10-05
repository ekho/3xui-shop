# С06: локальный операторский кабинет

Используется подтверждённый web-аккаунт и обычная cookie-сессия. Самостоятельная
регистрация не выдаёт роль. `/admin` требует backend entitlement; идентификатор
актора в решениях берётся из сессии. Право выдаётся отдельной командой владельца
стенда, после проверки нужного account UUID.

Сохранить UUID из собственного authenticated `/api/v1/me` в отдельный файл0600,
доступный только владельцу стенда. Значение не передаётся аргументом команды,
не включается в logs, `.env` или Git. Поставляемая команда:

```sh
/server operator grant --account-file /run/secrets/operator_account
/server operator revoke --account-file /run/secrets/operator_account
```

Для Compose монтируется этот файл read-only в одноразовый контейнер `backend`.
Контейнер запускается от UID/GID владельца файла; нужны существующие
`DATABASE_URL_FILE` и доступ к PostgreSQL. HTTP/очереди эта команда не запускает.
Grant разрешён только подтверждённому unrestricted web-аккаунту. Revoke удаляет
существующую роль также у restricted аккаунта. Повтор команды идемпотентен;
фактическая смена права записывается в аудит. Удаление роли закрывает следующие
защищённые запросы, текущая клиентская сессия сохраняет безопасный выход.

Backend и opt-in Telegram adapter имеют разные требования: backend принимает
`BOT_OPERATOR_IDS=`. Для заявки при отсутствии Telegram-операторов нужен
действующий web-оператор. Решение, reservation и worker используют прежние UUID,
target и один Grant. Профиль bot не включается для web-only проверки.

На собственном `cabinet-s01-local` стенде:

```sh
LOCAL_PROFILE=legacy python3 deploy/acceptance/local.py up
node deploy/operator-cabinet/browser.mjs
```

До второй команды source/image должны соответствовать проверяемой ревизии,
`BOT_OPERATOR_IDS` в private `public.env` и running backend должен быть пустым,
adapter остановлен, HTTPS cabinet/Mailpit/native3.7.0 и прежний Docker VPN здоровы.
Driver создаёт отдельные web-аккаунты, выдаёт роль через файловый CLI, проверяет
обращение и web-триал. Для Telegram-only создания читает настоящий `ADMIN_TG_ID`
из private worktree `.env`; уже использованный ID не заменяется выдуманным.
Внешнего Telegram transport, legacy SQLite writers и production здесь нет.

Controlled fixtures меняют только собственные новые account flags и scoped
apply trigger; прежние значения/trigger восстанавливаются в finally. Restore
использует настоящие dump/restore и правила [С02](s02-account-security.md),
проверяет roles/actors/identity/history/bytea и отзывает старые sessions/proofs.
Кратковременный fixture actor101 нужен только существующему restore helper,
после него возвращается пустой Telegram config. Предыдущий Docker VPN сохраняется.
Установленный Happ, системный VPN, clipboard и доверие сертификатам Mac
не изменяются. Подробные verdicts сохраняются локально без credentials/keys.

Миграция00009 не допускает Down при Telegram-origin аккаунтах или web-решениях,
чтобы не удалить их source/actor. Откат приложения требует совместимого кода
и сохранения новой схемы/данных; rollback старым dump не используется.
Перенос старых клиентов/единственного writer и внешнее включение — С46/С47,
вне этой локальной приёмки.
