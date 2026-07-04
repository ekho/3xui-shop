# Развёртывание (форк 3xui-shop)

Руководство по запуску **этого форка** `snoups/3xui-shop`. Форк отличается от upstream:

- работает **за внешним Traefik** (встроенный Traefik + Let's Encrypt удалены);
- секреты — через **docker compose secrets** (`/run/secrets/*`), не в `.env`;
- **апрув-гейт**: новые пользователи ждут подтверждения администратора;
- **трёхмерные тарифы**: срок + устройства + **лимит трафика**;
- **ручная оплата «карта→карта»** с модерацией администратором;
- **авто-рекуррент Telegram Stars** (для 30-дневного тарифа);
- аутентификация вебхука Telegram (`secret_token`), SQLite в режиме WAL, идемпотентность платежей.

> Обзор возможностей самого бота — в [README.md](README.md).

---

## ⚠️ Совместимость с панелью 3x-ui (важно)

Форк использует **py3xui 0.7.0** и рассчитан на **3x-ui ≥ v3.1** (проверено на **v3.4.2**).

- В 3x-ui v3.1+ клиентские операции переехали на эндпоинты `panel/api/clients/*`. Старые py3xui 0.3.x–0.6.x (легаси `panel/api/inbounds/*`) на таких панелях дают **404** — не использовать.
- Обратно: py3xui 0.7.0 **не работает** с панелями ≤ v3.0.
- **Зафиксируйте версию панели** и не обновляйте её без синхронной проверки бота.
- Инбаунды без TLS/Reality (например shadowsocks) поддержаны через compat-патч (`app/bot/utils/py3xui_compat.py`).

---

## 1. Требования

- VPS (Linux), **Docker** + **Docker Compose v2**.
- **Внешний Traefik** (v3) с настроенным HTTPS-резолвером, подключённый к **внешней docker-сети** (по умолчанию `traefik-proxy`). Бот не публикует порты — доступ только через Traefik.
- Домен, указывающий на сервер (`BOT_DOMAIN`).
- Панель **3x-ui v3.1+** (рекоменд. v3.4.2) с созданным inbound (vless/reality и т.п.).
- Токен Telegram-бота (@BotFather).

---

## 2. Подготовка внешнего Traefik

Бот подключается к внешней сети Traefik и объявляет маршрут через labels (см. `docker-compose.yml`). На стороне Traefik нужно:

1. **Внешняя сеть** (если ещё нет):
   ```bash
   docker network create traefik-proxy
   ```
   Имя должно совпадать с сетью `traefik-proxy` в `docker-compose.yml` и label `traefik.docker.network`.
2. **HTTPS-резолвер** (ACME/Let's Encrypt) и entrypoint `websecure` (:443). Имя резолвера укажите в `.env` → `TRAEFIK_CERTRESOLVER`.
3. **Проброс реального IP клиента** (`forwardedHeaders.trustedIPs`) на entrypoint — нужно для проверки IP Cryptomus-вебхука, если включаете крипту.

---

## 3. Секреты (docker compose secrets)

Секреты монтируются в `/run/secrets/<name>` и читаются в `config.py` по конвенции `<VAR>_FILE`. Создайте файлы в `./secrets/` (каталог в `.gitignore`, коммитить нельзя):

```bash
mkdir -p secrets
printf '%s' '123456789:your-bot-token'      > secrets/bot_token.txt
printf '%s' 'your-3xui-panel-password'      > secrets/xui_password.txt   # если авторизация по паролю
printf '%s' "$(openssl rand -hex 32)"       > secrets/webhook_secret.txt  # A-Za-z0-9_-, длина 1..256
chmod 600 secrets/*.txt
# опционально (если включаете):
# printf '%s' 'cryptomus-api-key'            > secrets/cryptomus_api_key.txt
# printf '%s' 'cryptomus-merchant-id'        > secrets/cryptomus_merchant_id.txt
# printf '%s' 'Сбербанк 2202 20** **** 1234, Иван И.' > secrets/manual_card_details.txt
```

Затем раскомментируйте соответствующие `environment: *_FILE`, `secrets:` и секцию `secrets:` в `docker-compose.yml`.

**Аутентификация в 3x-ui (P1):** поддерживается **токен ИЛИ логин+пароль**.
- Токен: задайте `XUI_TOKEN` (в `.env` или как секрет `xui_token`). Логин/пароль не нужны, `login()` не вызывается.
- Логин+пароль: задайте `XUI_USERNAME` + `XUI_PASSWORD` (пароль — секретом `xui_password`).

---

## 4. Переменные окружения

Несекретные — в `.env`. Секретные — через docker secrets (`*_FILE`) либо, для локального теста, напрямую переменной.

| Переменная | Обяз. | По умолчанию | Секрет | Описание |
|---|---|---|---|---|
| `BOT_TOKEN` | да | — | **да** | Токен бота (@BotFather). Секрет `bot_token`. |
| `BOT_DOMAIN` | да | — | нет | Домен бота (Host-rule Traefik, построение URL вебхука). |
| `BOT_ADMINS` | нет | `[]` | нет | ID админов через запятую (апрув, модерация, рассылка). |
| `BOT_DEV_ID` | да | — | нет | ID разработчика (получает алерты, тест-рефанды Stars). |
| `BOT_SUPPORT_ID` | да | — | нет | ID поддержки. |
| `BOT_PORT` | нет | `8080` | нет | Внутренний порт (за Traefik). |
| `WEBHOOK_SECRET` | реком. | — | **да** | `secret_token` вебхука Telegram (B7). Секрет `webhook_secret`. |
| `TRAEFIK_CERTRESOLVER` | да | `letsencrypt` | нет | Имя ACME-резолвера ВАШЕГО Traefik. |
| `SHOP_APPROVAL_REQUIRED` | нет | `True` | нет | Апрув-гейт для новых юзеров. `False` → как в upstream. |
| `SHOP_PAYMENT_STARS_ENABLED` | нет | `True` | нет | Оплата Telegram Stars. |
| `SHOP_PAYMENT_CRYPTOMUS_ENABLED` | нет | `False` | нет | Оплата Cryptomus (нужны ключи + вебхук). |
| `SHOP_PAYMENT_MANUAL_ENABLED` | нет | `False` | нет | Ручная оплата «карта→карта». Требует `MANUAL_CARD_DETAILS`. |
| `SHOP_PAYMENT_YOOKASSA_ENABLED` | нет | `False` | нет | Официальный эквайринг (по умолчанию выкл). |
| `SHOP_PAYMENT_YOOMONEY_ENABLED` | нет | `False` | нет | Официальный эквайринг (по умолчанию выкл). |
| `MANUAL_CARD_DETAILS` | если ручная | — | да* | Реквизиты для перевода (показываются юзеру). Секрет `manual_card_details`. |
| `XUI_USERNAME` | токен\|п.п. | — | нет | Логин панели (если не токен). |
| `XUI_PASSWORD` | токен\|п.п. | — | **да** | Пароль панели (если не токен). Секрет `xui_password`. |
| `XUI_TOKEN` | токен\|п.п. | — | да | Токен панели (если не логин/пароль). Секрет `xui_token`. |
| `XUI_SUBSCRIPTION_PORT` | нет | `2096` | нет | Порт subscription-сервиса панели. |
| `XUI_SUBSCRIPTION_PATH` | нет | `/user/` | нет | Путь subscription-сервиса. |
| `CRYPTOMUS_API_KEY` / `CRYPTOMUS_MERCHANT_ID` | если крипта | — | да | Ключи Cryptomus. Секреты `cryptomus_*`. |
| `REDIS_HOST` / `REDIS_PORT` | нет | `3xui-shop-redis` / `6379` | нет | Redis (FSM + дедуп напоминаний). |
| `LOG_LEVEL` | нет | `DEBUG` | нет | Уровень логов. |

\* «карта-реквизиты» технически не секрет (показываются юзеру), но хранение через docker secret удобно и не мешает.

> Хост панели 3x-ui хранится в БД (таблица `server`, добавляется через админку бота), а НЕ в env — поэтому `XUI_HOST` тут нет.

---

## 5. Тарифы (`plans.json`)

Смонтированный `./plans.json` (шаблон — `plans.example.json`). Формат — трёхмерный (срок/устройства/трафик):

```json
{
  "durations": [30, 60, 180, 365],
  "plans": [
    { "devices": 1, "traffic_gb": 100, "prices": { "RUB": {"30": 100}, "XTR": {"30": 80} } }
  ]
}
```
- `traffic_gb`: лимит трафика в ГБ, `0` = безлимит. Старые `plans.json` без поля → безлимит (обратная совместимость).
- Для **авто-рекуррента Stars** нужен тариф на `30` дней с ценой в `XTR`.

---

## 6. Пошаговый запуск

```bash
# 1. Конфиг
cp .env.example .env && nano .env        # BOT_DOMAIN, BOT_DEV_ID, BOT_ADMINS, TRAEFIK_CERTRESOLVER, флаги
cp plans.example.json plans.json && nano plans.json

# 2. Секреты (см. §3) + внешняя сеть Traefik (см. §2)
docker network create traefik-proxy      # если ещё нет

# 3. Сборка
docker compose build

# 4. Бэкап БД перед миграциями (если это обновление, не первый запуск)
cp app/data/bot_database.sqlite3 app/data/bot_database.$(date +%F).bak 2>/dev/null || true

# 5. Запуск (миграции Alembic выполняются автоматически в command контейнера)
docker compose up -d
docker compose logs -f bot
```

После старта: `/start` боту → админ получает заявку с кнопками Approve/Reject (если `SHOP_APPROVAL_REQUIRED=True`). Сервер 3x-ui добавляется через админку бота (🛠 → серверы).

---

## 7. Обновление, миграции, бэкап

- **Миграции** выполняются автоматически (`alembic upgrade head` в `command`). Схема БД — единственный head; при апгрейде форка бэкап делать **до** `docker compose up`.
- **Бэкап SQLite** (единственная точка отката для файловой БД):
  ```bash
  docker compose stop bot
  cp app/data/bot_database.sqlite3 backup-$(date +%F).sqlite3
  docker compose start bot
  ```
- **Single-instance:** бот рассчитан строго на 1 реплику (webhook + APScheduler-задачи не рассчитаны на несколько инстансов).
- **Смена версии панели 3x-ui** — только с проверкой совместимости py3xui (см. блок вверху).

---

## 8. Troubleshooting

| Симптом | Причина / решение |
|---|---|
| Все операции с панелью → `404` / клиент не создаётся | Несовместимость py3xui ↔ панель. Нужна 3x-ui v3.1+ и py3xui 0.7.0 (см. блок вверху). |
| `RuntimeError: No need to login ... token` при старте | Задан `XUI_TOKEN` — login по паролю не нужен; форк это учитывает. Проверьте, что не задавали одновременно противоречивую конфигурацию. |
| `inbound.get_list` падает с `ValidationError ... security` | Compat-патч не применился. Убедитесь, что `apply_py3xui_patches()` вызывается в `__main__.py`. |
| Оплата прошла, доступ не выдан | Смотрите алерты разработчику (`BOT_DEV_ID`): при ошибке провижининга бот шлёт «Manual re-provision required». |
| Крипто-оплата не активирует подписку (403 на вебхуке) | За внешним Traefik не пробрасывается реальный IP → IP-allowlist Cryptomus режет. Настройте `forwardedHeaders.trustedIPs`. |
| Поддельные апдейты / обход апрува | Проверьте, что задан `WEBHOOK_SECRET` (секрет `webhook_secret`) — иначе `/webhook` не аутентифицирован (B7). |
| `database is locked` под нагрузкой | SQLite; WAL+busy_timeout включены, но при реальной нагрузке рассмотрите PostgreSQL (`DB_HOST`/`DB_PORT`/…). |
| Напоминания по трафику не приходят | Тариф должен иметь `traffic_gb > 0`; лимит читается из settings инбаунда. |
| Логи | `docker compose logs -f bot`; файлы — в `./app/logs`. |

---

## 9. Замечания по функциям (из аудита)

- **Апрув-гейт**: `pre_checkout`/`successful_payment` не блокируются гейтом; reject при активном Stars-рекурренте отменяет подписку и рефандит списание.
- **Идемпотентность**: провижининг защищён compare-and-set по статусу транзакции (повторный вебхук не даёт двойного продления).
- **Ручная карта**: одна активная заявка на юзера; модерация атомарна (двойной Approve / Reject→Approve безопасны); в заявке админу — сумма и тариф.
- **Stars-рекуррент**: только 30-дн. тариф; `charge_id` первого платежа хранится для отмены; отмена/возобновление из профиля; при активном автопродлении ручная оплата блокируется (защита от двойного биллинга).

---

## 10. CI/CD — сборка и публикация образов (GHCR)

Workflow `.github/workflows/docker-publish.yml` собирает и публикует образ в **GitHub Container Registry** — `ghcr.io/ekho/3xui-shop`. Внешних секретов не нужно: используется встроенный `GITHUB_TOKEN` (`packages: write`).

**Схема тегов:**

| Событие | Docker-теги |
|---|---|
| `git tag 1.2.3` (или `v1.2.3`) → push тега | `1.2.3`, `1.2`, `1`, `latest` |
| push в `main` | `edge` (нестабильная), `main-<short-sha>` |

`latest` всегда указывает на последний релизный semver-тег; `edge` — на последний коммит `main` (для тестов, не для прода).

**Выпуск релиза:**
```bash
git tag 1.2.3
git push origin 1.2.3        # workflow соберёт и опубликует 1.2.3, 1.2, 1, latest
```

**Деплой из готового образа** (вместо локальной сборки) — в `docker-compose.yml` замените у сервиса `bot`:
```yaml
  bot:
    # build: .
    image: ghcr.io/ekho/3xui-shop:latest   # или :1.2  /  :edge для теста
```
Затем `docker compose pull bot && docker compose up -d`.

**Первичная настройка GHCR:**
- Первый успешный запуск workflow создаст package `3xui-shop` в вашем аккаунте. По умолчанию он **приватный** — чтобы тянуть без логина, сделайте его public в Settings → Packages, либо на сервере выполните `docker login ghcr.io` (PAT со `read:packages`).
- Убедитесь, что в репозитории разрешены GitHub Actions (Settings → Actions) и запись пакетов (Settings → Actions → Workflow permissions, либо job-level `packages: write` — уже задано в workflow).

> Образ собирается **multi-arch** — `linux/amd64` и `linux/arm64` (arm64 через QEMU-эмуляцию, шаг `setup-qemu-action`). Docker на любой из этих архитектур подтянет нужный вариант автоматически по manifest list. Сборка arm64 под эмуляцией заметно медленнее — если arm64 не нужен, уберите его из `platforms` и шаг QEMU.

