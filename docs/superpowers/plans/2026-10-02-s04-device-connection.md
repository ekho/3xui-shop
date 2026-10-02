# С04: план подключения устройства

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Coordinator integrates an owned native frontend slice; one fresh whole-branch review after С03–С06.

**Goal:** Выбор платформы, установка и безопасный импорт собственной подписки.
**Architecture:** Расширить существующий key panel кабинета; локальный QR, стандартные ссылки установки и ручной fallback. Backend API из С03 сохраняется.
**Tech Stack:** React/TypeScript, browser URL/clipboard/canvas, qrcode1.5.4, Playwright.
**Spec:** [С04](../specs/2026-10-02-s04-device-connection-design.md).

## Global Constraints

- Начинать после освобождения frontend файлов С03; не менять backend provisioning.
- Ключ только в памяти, явный fetch; скрытие всех представлений через60 секунд и при потере доступа/visibility/navigation.
- Все5 платформ, ru/en, keyboard и mobile375px; fallback независимо от deep link.
- Pin qrcode1.5.4/@types1.5.6 и lock; нет внешних QR requests.
- Native Happ, VPN/системное доверие и production исключены; не запускать deep links в автоматических checks.

## Review Focus

- RF1: поздний key fetch или QR generation не воскрешает скрытые данные.
- RF2: отказ clipboard оставляет readonly input и объяснение ручного импорта.
- RF3: ошибка/запрет нового key fetch очищает QR, href и ранее раскрытый input.
- RF4: язык RU не подменяет выбранный регион iOS-магазина.
- RF5: macOS deep link доступен, other-platform инструкция не обещает поддержку неизвестного клиента.

### Task 1: Подключение в существующем кабинете

**Files:** Modify `web/src/Cabinet.tsx`, `web/src/i18n.ts`, `web/src/style.css`, `web/package.json`, `web/package-lock.json`; create `web/src/Connection.tsx` только если отдельный компонент уменьшает сложность кабинета; test `web/tests/s04.spec.ts`.
**Interfaces:** Existing `api.getSubscriptionKey(signal)` → SubscriptionKey, С03 connection_available/status. New connection controls consume current key/clear callback; no second independent secret cache.

- [ ] RED Playwright: five platform instructions and exact hrefs, iOS region selector independent of lang; copy/QR own URL, same input/deep link; expired allowed, denied states hidden.
- [ ] RED RF1–RF3: defer key/QR response, hide/logout/visibility; fast-forward60s; clipboard rejects and fallback selects input; invalid HTTPS response never becomes clickable.
- [x] Install exact QR dependency versions/lock and implement the smallest shared key lifecycle with abort/generation guard; qr canvas has accessible name and text fallback.
- [x] GREEN `npm --prefix web run typecheck`, `npm --prefix web run build -- --mode test`, targeted `s03.spec.ts` and `s04.spec.ts` plus existing С01/С02 browser checks. Mock/prevent deep link navigation before clicking.
- [x] Specialist returns changed files/commands/verdict; coordinator inspects/stages/commits the owned slice.

### Task 2: Native browser acceptance

**Files:** Existing `deploy/s01/browser.mjs` or new focused `deploy/s04/browser.mjs`; create `docs/evidence/s04-acceptance.md` and update progress/roadmap.
**Interfaces:** Own HTTPS Docker cabinet/native3X-UI3.7.0; existing file-based test account and CA, no credentials or key printed.

- [x] Actual browser confirms installation links, copy/QR/deep-link string and own API key; intercept protocol click, assert no remote QR request and no new trial/grant/panel writes.
- [x] Record all5 AC against exact tested product revision, real versus mocked checks and excluded native Happ action.
- [ ] Coordinator local commit; whole-branch regression/review runs after С06.

Статус перед финальным review: реализация и техническая приёмка проверены;
итоговый review/закрытие ещё открыты. Неподтверждённые исторические RED steps
оставлены неотмеченными; Ruling и точные результаты — в
[общем evidence](../../evidence/s03-s06-progress.md#итоговые-проверки-перед-review).
