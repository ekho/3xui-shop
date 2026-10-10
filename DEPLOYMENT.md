# Развёртывание v2

Продукт состоит из одного Go backend и отдельного web/Caddy image. Backend
объединяет HTTP/River/main и support Telegram/schedulers. PostgreSQL содержит
все продуктовые факты и задания; Redis — ограничители запросов. Python image,
Alembic/Poetry и `/internal/v1` transport больше не используются.

## Настройки и образы

[Deployment inventory](docs/runbooks/s45-product-configuration.md) перечисляет
текущие readers/defaults и ограничения. Public параметры web передаются при
запуске Caddy; секреты и connection URI — через приватные `*_FILE` вне Git.
[Preview releases](docs/releases-v2.md) публикуют backend и web; это не deploy.

`deploy/acceptance/.env.example` и `compose.acceptance.yml` служат шаблоном
выделенного стенда. Local/native overlays содержат синтетические panel/SMTP
fixtures и фиксированные test ports. Перед использованием в другом окружении
оператор должен проверить project, volumes, origin/TLS, сохранённые keys и panel
identity. `dedicated-acceptance` нельзя подставлять вместо identity существующей
панели. Root `docker-compose.yml` старого бота удалён.

## Процессы

1. Подготовьте приватные файлы, подтвердите UID/file access и инфраструктуру.
2. Выполните `server migrate`; Goose хранит историю единственного `backend/go.mod`.
3. Запустите один `server serve`, затем проверьте `/readyz`, модульные states и
   собственный пользовательский сценарий. Telegram off разрешает HTTP/River без token.
4. Перед recovery закройте admission, остановите `serve` с SIGTERM и подтвердите
   его завершение. Только затем запускайте `server reconcile` для очереди provision.

Единая session advisory lock PostgreSQL не допускает второй `serve`/`reconcile`
до HTTP/polling/jobs/panel effects. При потере её соединения runtime завершается.
Она не управляет внешним legacy процессом: его остановка остаётся обязательным
этапом [cutover](deploy/cutover/README.md).

[Process operations](docs/runbooks/s44-process-operations.md) описывает readiness,
shutdown и diagnostics; [backup/restore](docs/runbooks/s43-backup-restore.md)
описывает private input, новую restore DB и проверку schema/attachments.
Rollback использует текущие PostgreSQL данные и совместимую Go-версию.
Старый SQLite snapshot не заменяет новые деньги, grants, accounts или jobs.

## Проверка

[Local acceptance](docs/runbooks/s01-test-rollout.md) использует только собственные
fixtures. Полный CI сохраняет Go race/static/generated/security, browser,
настоящую 3X-UI 3.7.0/TLS SMTP, group/server reconciliation, backup/restore и
backend/web image gates. Python остаётся лишь stdlib orchestration/fixtures.
Production не включается никакой командой из локальной приёмки.
