# С44 — управление процессом и наблюдаемость

Задача: [#46](https://github.com/ekho/3xui-shop/issues/46). Контракт:
`2026-10-09-s44-v1`; база `origin/v2` = `9305fa5b6a5dec44648aa9a57bb1501164e455f6`.
Принятая архитектура: `2026-10-05-modular-monolith-v1`, один Go-процесс
HTTP/River/Telegram, отдельный frontend/Caddy. Предпосылки #6/#60/#30/#40 CLOSED.

## Результат

Оператор с доступом к конкретному Docker host/project перезапускает только
`backend` через Compose. Учётная запись оператора кабинета сама по себе не
даёт доступа к Docker. Web/shell endpoint, supervisor и отдельный worker/bot
не добавляются. Перед остановкой фиксируются исполнитель, причина, ревизия
и Compose project в защищённом журнале оператора. Плановые работы используют
существующее окно обслуживания и действующий graceful shutdown.

`/healthz` сохраняет контракт: PostgreSQL доступен → `200 {"ok":true}`.
Отдельный `/readyz` выдаёт только безопасный boolean/error envelope и проверяет
PostgreSQL, Redis, запущенный River и отсутствие shutdown. Не раскрывает
токены, адреса, получателей, идентификаторы пользователей или сырые ошибки.
Отключение/сбой Telegram не меняет readiness кабинета.

Go сообщает lifecycle HTTP, River, schedulers, главного и support Telegram в
структурированных логах с именем модуля, состоянием и безопасным кодом.
Telegram различает disabled, starting, running, degraded и stopped;
восстановление после временной ошибки наблюдаемо, terminal failure не
представляется работающим каналом. HTTP продолжает работу при отказе Telegram.
Пустой successful claim снимает только ошибку получения задания; ошибка send
остаётся до реального успешного send, включая пустую очередь под lease.
Core failure вызывает корректное завершение процесса, чтобы действующая
политика перезапуска могла восстановить его. Остановка остаётся ограниченной
по времени, принятые операции сохраняются в PostgreSQL/River.

Оповещение вне Telegram использует существующий `notifications.SendSMTP`
(TLS, проверка сертификата и прежние SMTP credentials) напрямую, независимо
от PostgreSQL/River/Telegram. `OPERATIONS_EMAIL_FILE` опционально задаёт один
валидный адрес оператора; plaintext/conflicting/empty/невалидный адрес
отклоняется. Без получателя остаются логи и readiness. Настроенный канал
оповещает о готовности/остановке/ошибке процесса и деградации/восстановлении
Telegram. Сбой SMTP даёт безопасный лог, ограничен таймаутом и не завершает
кабинет. Письма содержат только имя модуля, состояние и код; сырые ошибки,
токены, ссылки подключения и адреса зависимостей не передаются. Доставка
best effort; остановленный/убитый процесс не может сам отправить письмо.
Живой процесс не заменяет внешний контроль Docker host/readiness.

## Приёмка и границы

- `/readyz`: success, PostgreSQL/Redis outage, River stopped, shutdown;
  `/healthz` regression и readiness при disabled/degraded Telegram.
- Lifecycle: startup/shutdown/error, завершение schedulers/channels и HTTP
  drain; код/label/recovery без утечки raw provider error.
- SMTP: локальный TLS fixture, оповещения и безопасный bounded failure;
  конфигурация fail closed, повтор состояния не создаёт поток писем.
- Compiled backend: restart того же собственного Compose service, данные
  и принятые jobs сохранены; readiness восстанавливается. Контейнерный probe
  проверяет настоящий backend, scratch image не требует curl/shell.
- Native Compose parser/healthcheck и Caddy route; одна свежая whole-branch
  проверка и CI на точном HEAD перед передачей PR координатору.

Ресурсы приёмки имеют собственные имена, порты, сеть и БД. Существующий
`cabinet-native`, стенды других сессий и основной checkout не затрагиваются.
Production, реальный Telegram/SMTP/VPN и merge/закрытие/архивирование исключены.
Серверный реестр и новые инфраструктурные права принадлежат #42; схемы БД,
платежи, backup/restore, перенос и удаление Python здесь не меняются.
