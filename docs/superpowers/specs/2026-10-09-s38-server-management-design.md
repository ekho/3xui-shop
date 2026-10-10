# С38 — оператор инфраструктуры ведёт серверы

Версия `2026-10-09-s38-server-management-v1`; задача #42; owner `vpn`.
База: `9305fa5b6a5dec44648aa9a57bb1501164e455f6`, реестр С39 v1 (#41),
модульный монолит v1 (#55), принятый роадмап. Документ фиксирует решения
реализации в рамках поручения автономно подготовить проверенный PR в `v2`.
Слияние, Done и архивирование оставлены координатору.

## Результат и граница

Оператор инфраструктуры получает список и карточку серверов, добавление
`name/host/max_clients`, ping, sync и безопасное удаление. Web-кабинет и
нативный Telegram вызывают одни публичные операции `vpn`; SQL/private-пакеты
другого модуля не используются. Реестр, назначение, ключи, операции и резервы
С39 сохраняются. Произвольное редактирование, перенос клиентов, импорт,
production и отдельный выпуск исключены.

## Права

Обычный оператор не получает инфраструктурного доступа автоматически.
`accounts` хранит отдельное явное разрешение для существующего проверенного
web-оператора; выдача/отзыв выполняются служебной CLI с account-file, как
существующая операторская роль. Отзыв основной роли удаляет инфраструктурное
разрешение. Это эквивалент более узкого IsDev в единой модели аккаунтов:
инфраструктурный доступ выдаётся только ответственному за прежний dev-контур.
Администратор не может выдать себе это разрешение через HTTP или Telegram.

Web использует существующие session/Origin/CSRF проверки; Bearer и Mini App
не получают доступа. Telegram — только private chat с совпадающим sender,
свежим ResolveTelegramContext и инфраструктурным разрешением связанного
web-оператора. Проверяются restriction, версия учётных данных, связь и роль;
mutation и replay повторно проверяют право в транзакции. Список, карточка и
provider reads тоже требуют инфраструктурного права.

## Реестр, повторы и удаление

- Создание принимает только name, HTTPS host и целый max_clients >= 0
  (HTTP верхняя граница 2147483647). ID создаёт backend. URL не содержит
  userinfo/query/fragment; общие file-only credentials/CA С39 переиспользуются.
  Уникальные name/host и retired identities не перезаписываются.
- Повтор с одним actor/idempotency key и тем же входом возвращает прежний
  результат; другой вход даёт 409. Параллельные вызовы не создают два сервера.
  Key передаётся и для ping/sync/delete; Telegram получает стабильный key
  из идентичности принятого update. Повтор проверяет текущие права.
- Список показывает ID/name/host/max_clients, online/observed_at,
  assigned_clients/reserved_clients. Карточка добавляет доступные действия.
  Пароли, токены, subscription base и VPN-ключи не входят в DTO или audit.
- Ping и sync используют существующий TLS PanelClient и С39 observation
  ordering. Панельные вызовы выполняются вне SQL transaction/pool lock.
  Offline — определённый результат наблюдения, provider body не выдаётся.
- Удаление требует явного подтверждения. Под общей lock пула проверяются
  назначенные аккаунты, резервы и незавершённые trial/access targets этого ID.
  Любой такой факт даёт 409 SERVER_IN_USE без изменения фактов клиента.
  Если панель недоступна или содержит клиентов, удаление также запрещено:
  read-only проверка всех клиентов не должна превращать неизвестное в ноль.
  При успешном readback проверка backend фактов повторяется под lock.
- Пустой сервер получает tombstone `retired=true`, online=false и новую
  revision; физического DELETE нет. Старое наблюдение не оживляет сервер.
  Повтор удаления безопасен. Настроенный primary не восстанавливается sync.
  С40/С47 определяют перенос/вывод занятого сервера; здесь нет обхода отказа.
- Успешные действия сохраняют audit с actor/channel/action/server ID и
  результатом, без host/provider body/секретов; audit ошибка откатывает
  соответствующую mutation. Read-only ping/sync никогда не меняют клиентов.

## API и интерфейсы

Канонический источник — `docs/api/openapi.yaml` (JSON OpenAPI). Добавляются:
GET `/api/v1/operator/servers`, GET `/api/v1/operator/servers/{server_id}`;
POST collection, `/servers/sync`, `/{server_id}/ping`, `/{server_id}/delete`.
POST используют существующий UUID Idempotency-Key. Delete body содержит
`confirmation:true`; ping/sync — пустой объект. Errors используют существующую
envelope: 400 input, 401 session, 403 role/CSRF, 404 unknown/retired,
409 conflict/in-use, 503 unavailable. Серверный ID — opaque text, включая
прежний legacy ID; path всегда кодируется. Добавочный optional boolean
`can_manage_servers` в OperatorSession управляет видимостью навигации.
Go и TS consumers генерируются штатными командами, не редактируются вручную.

Web сохраняет существующий стиль, ru/en, labelled inputs, keyboard/focus,
role alert/status, пустые/offline/error состояния. Перед удалением показывает
подтверждение и запрещение занятого сервера. Повтор после потери ответа
сохраняет key, изменение входа создаёт новый key.

Telegram сохраняет функциональные действия в компактном private интерфейсе:
`/servers`, `/server ID`, `/server_add name | https://host | max_clients`,
`/server_ping ID`, `/servers_sync`, `/server_delete ID confirm`.
List/card дают подсказки и защищённую ссылку в web-кабинет; delete без confirm
выдаёт пояснение без mutation. ru/en следуют locale аккаунта. Нельзя читать
server secrets или провоцировать сетевой вызов до проверки actor/permission.

## Приёмка

1. Клиент, обычный оператор, отозванная роль/связь и Mini App получают отказ;
   инфраструктурный оператор проходит через HTTP и private Telegram.
2. Повтор/параллельное добавление: один ID, один registry fact и один audit;
   conflict при изменённом input; ошибка audit не оставляет server mutation.
3. Assigned/reserved/in-flight/provider clients/offline запрещают удаление;
   пустой online удаляется. Sync, stale observation и restart сохраняют tombstone,
   назначения, targets, grant, ключи и клиентские записи.
4. Реальные HTTP/browser consumers проверяют ru/en, keyboard и ARIA semantics,
   confirmation, empty/error/offline и payload/response contract.
5. Отдельная собственная TLS 3X-UI 3.7.0 проверяет actual add/list/ping/sync/delete,
   Bot API остаётся локальным симулятором. Чужие runtime/production не меняются.
6. Generate/vet, соответствующие race/integration tests, весь web suite и
   exact-source CI проходят; evidence отделяет локальную приёмку от merge/release.

Rollback: откат приложения без уничтожения реестра/операций. Down с новой
ролью/action history должен отказывать, если потеряет факты; пустые новые
таблицы допускают обратимую миграцию. Автоматический prerelease после merge
в v2 выполняется существующим workflow; эта сессия не выполняет merge.
