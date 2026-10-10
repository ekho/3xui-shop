# С45 — ограниченный план

Источник: [спецификация](../specs/2026-10-10-s45-product-config-design.md), #47.

1. Добавить адресные проверки secret-file границы и runtime имени продукта;
   воспроизвести небезопасный fallback и отсутствие deployment branding.
2. Исправить существующий Python string-secret reader/его пропущенные callers;
   добавить необязательное имя в существующие config.json, title и brand.
   Сохранить Go file-only/conflict semantics и все публичные API/DB identities.
3. Провести используемые module/retention параметры через существующий Compose,
   обновить deployment example и полный inventory. Секреты — только paths.
   Сохранить opt-in payment overlays, maintenance/backup/lifecycle runbooks.
4. Проверить собственные PG/Redis fixtures, реальный serve/restart/worker,
   HTTP+Caddy на одном image и ru/en browser; выполнить static/generation и
   сохраняемые native #44/#45/#46 tests. Убрать только собственные fixtures.
5. Независимый read-only review, исправления по фактам, commit с Conventional
   Commits/Co-Authored-By, PR → `v2`, exact-head required CI, manual merge.
   Затем закрыть #47/Project Done и дать root delivery/cleanup evidence.

Миграций и DB-backed configuration нет. Р7 referral/promocode не реализуются.
