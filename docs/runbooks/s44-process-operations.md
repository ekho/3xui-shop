# С44: защищённый restart и состояние backend

Источник: [#46](https://github.com/ekho/3xui-shop/issues/46),
[спецификация](../superpowers/specs/2026-10-09-s44-process-operations-design.md).
HTTP, River, schedulers и оба Telegram-канала работают в одном `backend`.
Web-роль оператора не даёт прав Docker/SSH. Процедура доступна только оператору
с отдельным разрешением на конкретный host, окружение и Compose project.
Эта инструкция сама по себе не разрешает production-действия.

## Проверить цель и зафиксировать действие

В примере используются существующие native Compose-файлы. Для другого
развёртывания используйте его проверенный env и **точный** набор overlays.
Сначала задайте `OPS_PROJECT` и абсолютный путь `OPS_ENV` своего окружения.
Не используйте чужой project, общий default или `down -v` для restart.

```sh
ops_compose() {
  docker compose --project-name "$OPS_PROJECT" --env-file "$OPS_ENV" \
    -f deploy/acceptance/compose.acceptance.yml \
    -f deploy/acceptance/compose.local.yml \
    -f deploy/acceptance/compose.native.yml "$@"
}
docker context show
ops_compose config --quiet
ops_compose ps
ops_compose config --images
ops_compose exec -T backend /server healthcheck
ops_compose logs --since 10m backend
```

Сверьте host/project, image digest/ревизию, отсутствие второго `serve`/`reconcile`
и применимость окна обслуживания. Запишите исполнителя, время, причину, target
project и текущую ревизию в защищённый журнал эксплуатационных работ. SSH/Docker
доступ и журнал действий принадлежат host-оператору; приложение не может
достоверно определить исполнителя команды Docker. Не копируйте `config` целиком,
секреты, подписочные ссылки или содержимое пользовательских запросов в журнал.

## Остановить и восстановить один процесс

```sh
ops_compose restart --timeout 75 backend
ops_compose ps backend
ops_compose exec -T backend /server healthcheck
ops_compose logs --since 5m backend
```

Compose посылает SIGTERM. Readiness снимается до HTTP drain, Telegram-запросы
отменяются, River завершает работу с прежним лимитом времени. `75s` покрывает
HTTP/channel drain 20s + scheduler join 20s + River graceful 20s + River hard
stop 3s + SMTP drain 4s и небольшой запас для освобождения ресурсов. После
истечения drain HTTP закрывает оставшиеся соединения; River отменяет job contexts.
Если job игнорирует отмену и не завершился за hard stop, backend выходит с ошибкой
без ожидания удерживаемого PostgreSQL connection. Новое выполнение опирается на
прежние lease/retry/idempotency правила; уведомление после аварии не гарантируется.
Перезапускается тот же образ; изменения env/секретов или образа требуют
отдельного `up -d --no-deps backend` после проверки их источника и полномочий.
PostgreSQL/Redis, frontend/Caddy и volumes restart не затрагивает.

Успех: один backend, probe exit `0`, HTTP `/readyz` → `200 {"ok":true}`,
логи HTTP/River/schedulers running и соответствующее состояние Telegram.
Проверьте конкретную ранее принятую операцию через обычный интерфейс/историю:
повтор не должен создавать вторую выдачу или денежный факт. Очередь и данные
остаются в PostgreSQL; память диалога Telegram при restart не сохраняется.

При core failure процесс выходит, `restart: unless-stopped` восстанавливает его.
Docker healthcheck маркирует readiness; **unhealthy сам по себе не перезапускает
контейнер**. При 503 сначала проверьте PG/Redis и состояние River, а не повторяйте
restart вслепую. При повторном startup failure остановите только свой backend,
сохраните безопасные коды и устраните причину в конфигурации. Если причиной была
новая ревизия/конфигурация, верните ранее проверенные digest/файлы, затем
`up -d --no-deps backend` и повторите probe/операторскую проверку. Restart не
откатывает БД; rollback схемы или восстановление backup — отдельная процедура.

## Готовность и оповещения

- `/healthz`: доступность PostgreSQL, прежний контракт сохранён.
- `/readyz`: PostgreSQL + Redis + River, без shutdown; только безопасный
  результат, без подробностей о конфигурации/пользователях. Telegram независимо.
- `operations state` в backend logs: имя модуля, состояние, безопасный код.
  Disabled/starting/running/degraded/stopped различимы; восстановление канала
  даёт новую запись. Startup, shutdown и ошибки HTTP/River/schedulers также видны.

Для почты положите **один адрес оператора** в файл с ограниченными правами и
задайте в deployment env `OPERATIONS_EMAIL_FILE=/absolute/path/to/recipient`.
Compose монтирует его в backend; параметры SMTP/TLS/CA и credentials остаются
существующими. Plaintext `OPERATIONS_EMAIL`, пустой/невалидный/conflicting файл
отклоняются. Для применения меняющейся конфигурации требуется recreate backend.
Адрес не выводится в логах. Без этого файла почтовый канал отключён.

Письма о lifecycle/деградации содержат только module/state/code; одно и то же
состояние подавляет повторы. Отправка ограничена по времени, очередь конечна.
`SMTP_UNAVAILABLE`/`QUEUE_FULL` означают, что часть оповещений не доставлена;
источник состояния остаётся в логах. SMTP недоступен → кабинет продолжает работу.
Письма best effort: SIGKILL, падение host/процесса или одновременная недоступность
SMTP исключают гарантированное оповещение. Проверку `/readyz` и Docker events
извне подключают к уже используемому host-контролю, а не к Telegram или самому
backend. Настройка реального host/SMTP и external acceptance выполняются отдельно.
