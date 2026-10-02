# С03: план профиля и подписки

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans for coordinator integration. Native backend/frontend specialists implement owned boundaries under the enabled TradeOS placement policy. One fresh whole-branch review after С03–С06.

**Goal:** Полный собственный профиль/подписка с достоверными состояниями и split traffic.
**Architecture:** Расширить существующие Service, OpenAPI и кабинет; использовать native3.7.0 GET traffic и PostgreSQL observations.
**Tech Stack:** Go/Echo/pgx/sqlc, React/TypeScript/Vite, Playwright, собственные PostgreSQL/Redis/3X-UI3.7.0.
**Spec:** [С03](../specs/2026-10-02-s03-profile-subscription-design.md).

## Global Constraints

- С03–С06 разрешены автономно; локальные изменения/commits, production и live Happ исключены.
- Не менять ownership, UUID/subId, grants или strict provision/reconcile target; profile читает native состояние.
- Bytes/int64, N+1, null unknown, 0 unlimited; только совпавшая identity, нет panel writes из read.
- Canonical OpenAPI до consumers; generated не редактировать вручную; ru/en и keyboard/mobile обязательны.
- Existing15 С01 +9 С02 operations сохраняются; response additions optional.

## Review Focus

- RF1: aggregate0 из clients/get при ошибке traffic не должен выглядеть реальным наблюдением.
- RF2: disabled из quota/expiry не должен стать административным ban.
- RF3: счётчики overflow/чужая traffic identity не должны раскрыть key или испортить cached values.
- RF4: поздний concurrent read не затирает свежую observation.
- RF5: stale subscription не оставляет ранее раскрытый key после ограничения/logout/navigation.

### Task 1: Backend и контракт чтения

**Files:** Modify `docs/api/openapi.yaml`, `backend/internal/platform/panel.go`, `subscription.go`, `backend/db/queries/provisioning.sql`; create migration00007 and focused `profile_test.go`; generated wire/store coordinator + backend respectively.
**Interfaces:** Existing Subscription/SubscriptionKey; native ReadTraffic and access memberships; optional split fields; strict existing provision untouched.

- [x] Координатор обновляет авторский контракт и генерирует Go/TS до dispatch.
- [ ] Backend пишет RED tests `TestProfileStates`, `TestProfileTrafficBoundary`, `TestProfileCache` на real PG + TLS panel; assert examples up100/down200/limit1024 → used300/remain724, zero valid, limit0/remainnull, foreign/negative/overflow stale, accounts.vpn_banned vs native disabled, unlimited profile with finite limits, native writes0.
- [x] Реализовать минимальный parser/observation/state derivation и protected key; старые проверки provision должны остаться зелёными.
- [x] Cache regression: native unlimited profile с7 устройствами/100GB/expiry0 после успешного чтения и outage сохраняет последние лимиты/срок/profile/counters + stale/time; более старый read не перетирает metadata.
- [x] GREEN: `TEST_DATABASE_URL_FILE=<private> TEST_REDIS_URL_FILE=<private> go -C backend test ./internal/platform -run 'TestProfile|TestPanel|TestProvision' -race -count=1`; generation/vet. Логи private, verdict без identity/secrets.
- [x] Отчёт specialist, coordinator inspect и local commit Task1.

### Task 2: Экран кабинета

**Files:** Modify `web/src/Cabinet.tsx`, `web/src/api/client.ts`, `web/src/i18n.ts`, `web/src/style.css`; create `web/tests/subscription-profile.spec.ts`.
**Interfaces:** Task1 frozen generated Subscription shape; account/me unchanged; late fetches cancelled.

- [x] Frontend пишет RED browser tests для states/split/unlimited/stale, upload100/down200/used300/remain724; unknown не0, key clearing, keyboard/mobile/ru-en.
- [x] Реализовать профиль в существующей карточке, controls/key guards; без новых UI libraries.
- [x] GREEN: `npm --prefix web run typecheck`, `npm --prefix web run build -- --mode test`, targeted Playwright С03 + existing С01/С02.
- [x] Отчёт specialist, coordinator inspect и local commit Task2.

### Task 3: Native acceptance и coverage

**Files:** Existing `deploy/acceptance/local.py`, browser driver и `backend/tests`; create `docs/evidence/s03-acceptance.md`.

- [x] Реальная own3.7.0 readback подтверждает traffic fields/identity; browser actual API показывает состояния и counters; не менять native Happ или production.
- [x] Проверить cache outage и отсутствие writes в focused TLS tests; native existing target/Grant/VPN сохраняются.
- [x] Записать все8 AC с точной ревизией, commands/result и оставшимися external launch gates; обновить roadmap/progress.
- [x] Local commit Task3; общий regression/review после С06, без повторения зелёных suites без причины.

Итоговый статус: локальная реализация и приёмка завершены; один свежий
whole-branch review и обязательный RED→GREEN fix pass пройдены. Неподтверждённые
исторические RED steps оставлены неотмеченными; Ruling и точные результаты —
в [общем evidence](../../evidence/s03-s06-progress.md#итоговое-закрытие-локальной-приёмки).
