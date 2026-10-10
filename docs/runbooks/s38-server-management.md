# С38 — управление серверами

Доступ получает только подтверждённый, unrestricted web-оператор с отдельным
инфраструктурным разрешением. Обычная операторская роль его не выдаёт.
Владелец окружения проверяет UUID аккаунта и сохраняет его в абсолютный файл
с правами 0600; UUID, credentials и ключи не передаются аргументами или в Git.
Используются существующие `DATABASE_URL_FILE` и read-only secret mounts:

```sh
/server infrastructure grant --account-file /run/secrets/infrastructure_account
/server infrastructure revoke --account-file /run/secrets/infrastructure_account
```

Grant требует уже выданной операторской роли. Revoke основной роли удаляет и
инфраструктурное разрешение; следующий запрос проверяет актуальное право.
CLI не запускает HTTP, очереди или Telegram. Выдача через интерфейс отсутствует.

В кабинете открыть `/admin/servers` (ru) или `/admin/servers?lang=en`.
Добавление принимает уникальные name, HTTPS host и целый max_clients от 0 до
2147483647. Используются общие credentials/CA настроенных панелей; URL не должен
содержать credentials, query или fragment. ID создаёт backend. Существующие
host и ID не редактируются. Legacy `null` capacity отображается отдельно от 0.

Ping обновляет наблюдение конкретного сервера, sync — всех действующих серверов.
Offline означает неуспешную проверку доступности, а не пустую панель. После
потери ответа повтор того же действия сохраняет ключ идемпотентности и возвращает
исходный результат; для нового наблюдения начать новое действие.

Удаление требует подтверждения и проверяет назначенных клиентов, резервы,
незавершённые trial/access targets и всех клиентов панели. Занятый сервер даёт
409; недоступная или неизвестная панель — 503. После сетевой проверки backend
повторно проверяет назначения под общей блокировкой пула. Подсказка доступности
кнопки не заменяет эту проверку. Успех сохраняет tombstone, без переноса клиентов,
физического удаления строки или изменения VPN-ключей. Sync его не восстанавливает.

В private Telegram требуется актуальная связь с тем же проверенным аккаунтом:

```text
/servers
/server ID
/server_add name | https://host | max_clients
/server_ping ID
/servers_sync
/server_delete ID confirm
```

Команды в группе и пересланные сообщения не выполняют действий. Ссылка из
списка/карточки открывает раздел серверов кабинета; ru/en берётся из аккаунта.

## Собственная локальная проверка

Fixture создаёт две изолированные TLS-панели 3X-UI 3.7.0 на loopback 61444/61449.
Эти порты и подсеть 172.31.112.0/28 должны быть свободны. Он не использует
существующий `cabinet-native` stack. Private state содержит случайный пароль и
TLS key; не публиковать его или provider responses. Docker/DB identities уже
созданного fixture сохраняются через `runtime.json` с purpose marker.

```sh
export SERVER_MANAGEMENT_NATIVE_STATE=/absolute/private/server-management-native
python3 deploy/server-management/local.py up
python3 deploy/server-management/local.py check
# TEST_DATABASE_URL_FILE/TEST_REDIS_URL_FILE указывают test-only файлы 0600.
RUN_BROWSER_TESTS=1 go -C backend test -race ./tests \
  -run 'TestNativeServerManagement|TestServerManagementBrowser' -count=1 -timeout=10m
python3 deploy/server-management/local.py down
```

Браузер читает собранный `web/dist`: перед проверкой выполнить
`npm --prefix web run build -- --mode test`. Testkit создаёт отдельную БД;
Telegram transport остаётся локальным симулятором. Это не production-проверка.
Миграция 37 допускает Down только при пустых новых role/action таблицах;
при сохранённых фактах откатывать приложение, сохраняя схему и данные.
