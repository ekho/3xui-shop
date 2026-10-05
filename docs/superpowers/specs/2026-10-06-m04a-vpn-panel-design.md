# М04а — клиент 3X-UI в модуле vpn

Дата: 2026-10-06. Владелец: GitHub #58. Версия: `2026-10-06-m04a-vpn-v1`.
Подготовка и выполнение автономны по принятому поручению владельца;
общая архитектура — `2026-10-05-modular-monolith-v1`, выполнение Native.

## Результат и граница

Первый из двух последовательных PR М04 переносит существующий HTTPS-клиент
3X-UI из `platform` в `modules/vpn`. Он исполняет все прежние панельные вызовы
триала, чтения профиля/ключа, назначения тарифа, профилей/ban и monthly reset.
Второй PR завершает владельцев подписок и устойчивых операций/SQL. Задача #58
остаётся открытой после М04а; деньги С10/PR #5 интегрируются после полного М04.

Сравнивались перенос всего М04 одной веткой, сначала подписки с прежним
панельным клиентом и сначала панельный транспорт. Выбран последний: существующий
клиент не зависит от SQL, River или wire, а все потребители уже определены.
Он даёт работающую проверяемую границу без временного доступа нового модуля
к чужой реализации. Схема БД и бизнес-правила на этом шаге сохраняются.

## Публичный Go-контракт

`vpn.Config` содержит только `PanelURL`, `PanelToken`, `PanelUsername`,
`PanelPassword`, `PanelRootCAs`. Значения по-прежнему загружает существующий
file-secret loader; владельцу транспорта не передаются настройки аккаунтов/почты.

`vpn.NewPanelClient(Config) *PanelClient` создаёт прежний клиент с отдельной
cookie jar на операцию. Его публичные методы сохраняют сигнатуры:
`Close`, `RegularInboundIDs`, `GetClient`, `Traffic`, `AccessProfile`, `AddClient`,
`Attach`, `ProfileInboundIDs`, `MembershipDiff`, `Detach`, `UpdateAccess`,
`ResetAccessTraffic`, `DisableAccess`. `UpdateAccess` принимает `vpn.AccessTarget`.
Поля клиента, HTTP/envelope/login/CSRF/tag helpers остаются неэкспортированными.
Нет интерфейса с одной реализацией, новой зависимости или панели как сервиса.

`vpn.ProvisionTarget`, `vpn.AccessTarget`, `vpn.PanelClientView` сохраняют
порядок полей, JSON tags, nil/empty и точность int64. Target никогда не
публикуется через HTTP. `vpn.Matches`/`vpn.MissingInbounds` сохраняют прежнее
сравнение при provisioning. Sentinel errors `ErrPanel`, `ErrIdentity`,
`ErrMembership`, `ErrTraffic` сохраняют прежние значения и errors.Is.

`platform` оставляет совместимые aliases и один перевод Config в vpn.Config;
его потребители получают реальный vpn-клиент, без второго сетевого обработчика.
Фасад не содержит HTTP, TLS, login, CSRF или tag logic. Старые рабочие target
JSON по-прежнему декодируются без пересоздания ключей, assignment или операций.
Будущий `subscriptions` будет использовать тот же публичный пакет.

## Совместимость и безопасность

- Один Go-процесс, существующие HTTP/API/CLI и River job kinds сохраняются.
- HTTPS/TLS verification, redirect refusal, 10s request/5s dial+TLS budgets,
  ограничение ответа 1MiB, en-US ошибок и отсутствие POST retries сохраняются.
- Numeric panel DB id не заменяет VPN UUID. Проверяются key/UUID/subID и
  неотрицательные лимиты/трафик; неоднозначное отсутствие не считается missing.
- Перенос не меняет disabled/unknown memberships, device-count +1 и
  преобразование 3.7.0 ClientRecord в update payload с сохранением чужих полей.
- Сетевые вызовы остаются вне SQL Tx; ownership/lease/watchdog и readback
  выполняет прежний worker до следующего PR. Одна операция имеет одного писателя.
- Настоящие деньги, production, внешние SMTP/benchmark, переключение живого
  Happ и изменения macOS trust отсутствуют. Own Docker 3X-UI строго **3.7.0**.

## Приёмка

Прямой vpn-клиент проходит прежние native-record и CSRF/login/TLS/POST tests.
Новая проверка update payload читает реальный TLS request: UUID id, literal
device limit 3, allowedIPs array, expiry/traffic/ban, сохранённое неизвестное
поле. Table cases проверяют отменённый context и отсутствие HTTP для unsafe
origin. Persisted-target fixtures проверяют обе старые JSON формы и helpers.

Регрессия: Go race со всеми connected browser сценариями; module boundary;
vet/generation без drift; 105 Python и полный Playwright; web static/runtime
config; Compose build/smoke; реальная 3X-UI3.7.0, TLS Mailpit, simulated Telegram
и physical stop/start с теми же Operation/Grant/keys. Один Astra/high reviewer
проверяет всю ветку. Затем exact-source CI, merge в v2 и preview publication.
Отсутствие SQL-переноса в М04а не считается завершением М04.
