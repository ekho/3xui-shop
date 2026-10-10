# Встроенный Telegram

Native-профиль запускает один backend для HTTP, River jobs, scheduler и
Telegram. Python bot и отдельный `reconcile` здесь не запускаются. Перенесены
апрув web-триалов, клиентские команды, переходы в Mini App и клиентские
уведомления. Денежные Telegram-события обслуживают С34–С36; текущая передача
исполнения и совместимый rollback описаны в [С47](../../deploy/cutover/README.md).
Production переключение требует отдельного разрешения.

Из корня checkout с Docker, Go и OpenSSL:

```sh
python3 deploy/acceptance/local.py up
python3 deploy/acceptance/local.py check
python3 deploy/acceptance/local.py down
```

По умолчанию используется собственный проект `cabinet-native`, private state
`.superpowers/acceptance/native-docker` и loopback HTTPS на `58443`.
Панель закреплена на 3X-UI **3.7.0**, почта — Mailpit с TLS и authentication.
Публичные условия и поддержка задаются при запуске gateway. Системное доверие
сертификатам, Happ и production не меняются. Перед запуском убедитесь, что
loopback-порты свободны и прежний собственный стенд остановлен.

`check` запускает Go HTTP/jobs/Telegram integration с simulated Bot API и
настоящими SMTP/панелью. Затем публичное web-решение создаёт отложенную job,
compiled backend останавливается и запускается с той же БД. Проверяются
исходная Operation, один Grant, прежние ключи и readback панели. В этой проверке
физического рестарта Telegram выключен; его повтор callback и реконструкция
полного lifecycle проверяются отдельно Go integration. Private log — `native-go.log`.

Для отдельного **тестового** бота в private `public.env` задайте
`TELEGRAM_ENABLED=true`, путь `BOT_TOKEN_FILE` и реальные `BOT_OPERATOR_IDS`.
Токен должен быть файлом, операторы должны начать личный чат; webhook должен
отсутствовать. Пересоздайте только backend через те же три Compose-файла и
`--env-file`. Runtime-владение допускает один `serve` или `reconcile` для БД;
прежний poller и его supervisor должны быть подтверждённо остановлены.
При `TELEGRAM_ENABLED=false` токен не читается.

Для клиентского режима настройте **Main Mini App** этого бота в BotFather
на HTTPS `${CABINET_ORIGIN}/mini-app/cabinet`. Backend проверяет `getMe`, ID
токена, username и `has_main_web_app`, затем устанавливает menu button на
текущий адрес кабинета. Ненастроенный Main Mini App даёт безопасный код
`MINI_APP_NOT_CONFIGURED`; HTTP и jobs продолжают работу.
[Main Mini App и startapp](https://core.telegram.org/bots/webapps#launching-the-main-mini-app),
[поле getMe](https://core.telegram.org/bots/api#user).

`/start`, `/help`, `/support` работают в личном чате. `/start <source>`
передаёт исходную метку в Main Mini App; UUID и первый источник сохраняются
после подписанного входа и согласия. Старые кнопки ведут в защищённые экраны,
старый инвойс открывает точный собственный архивный факт либо полную историю.
Ключ подключения показывается только по действию внутри кабинета.

Клиентский outbox отделён от операторских карточек. Его generic ru/en
уведомления содержат только ссылку на кабинет; после рестарта pending
сохраняются. Потерянный ответ Telegram может повторить уведомление, но не
покупку или выдачу. Отозванная привязка, смена credentials, запрет или
карантин препятствуют отправке. Миграция 00026 запрещает downgrade при фактах.

401/409, настроенный webhook или неподдержанный payment update останавливают
Telegram-модуль с безопасным кодом в логах. HTTP и workers продолжают работу.
Для смены конфигурации остановите прежний runtime/poller и его supervisor,
дождитесь завершения и запустите один backend. Code rollback выполняется только
на проверенный compatible Go checkpoint с текущей PostgreSQL и поддержкой late
receipts по [С47](../../deploy/cutover/README.md). Сохраняются pending deliveries,
исходные keys и money facts. Прежний Python adapter и его API удалены.

Причина ещё не подтверждённого пересмотра хранится в памяти. После рестарта
оператор начинает диалог заново; подтверждённые решения и jobs уже в БД.
Неоднозначная отправка Telegram может оставить повторную карточку, но не вторую
выдачу. Неоднозначная запись панели требует сверки, автоматический create retry
на 3.7.0 остаётся выключенным.
