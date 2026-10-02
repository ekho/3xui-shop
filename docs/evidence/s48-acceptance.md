# С48: локальная приёмка ограничений аккаунта

Статус: подготовка. Поведенческий прогон на сборке С48 ещё не выполнен; PASS
не заявляется. Исходная продуктовая ревизия `32b0205`, план и spec — `a42d3ea`.
Проверяемая поверхность — только собственный Docker project `cabinet-s01-local`:
кабинет `https://localhost:58443`, 3X-UI 3.7.0, Mailpit и его PostgreSQL.

После сборки backend/gateway и проверки их точных image digest выполнить по порядку:

1. `python3 deploy/s48/local.py import-acceptance` — новые согласованные
   synthetic Telegram identities, read-only SQLite export→stdin import,
   dry-run/apply/replay/conflict и копия PG в отдельной БД этого проекта.
2. `node deploy/s48/browser.mjs` — реальные HTTPS browser/API роли, email,
   trial, restrict/unrestrict, history, mobile/keyboard и replay после ручного
   снятия ограничения. Рекомендуемый ограниченный запуск использует
   `node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 -- python3 deploy/s48/local.py import-acceptance`
   и аналогично с `--timeout-seconds 360 -- node deploy/s48/browser.mjs`.

Артефакты фактических прогонов появятся только после исполнения в приватном
`.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e/`. Каждая строка
там содержит criterion, target, exact command, expected, actual, verdict и
ссылку на артефакт; здесь будут опубликованы только обезличенные выводы,
без email, Telegram ID, cookie, key, SMTP token или сырого panel ответа.

Граница будущего переключения: остановка старого registration approval
middleware, scheduler и reminders должна быть единой операцией в окне С32/С46/С47.
Legacy-код и его данные остаются до этого cutover; триальный approval С01
продолжает работать. Этот локальный прогон не переключает Telegram и не
проверяет production, Happ, установленный VPN или системное хранилище доверия.
