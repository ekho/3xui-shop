# С36 — Browser payments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Явная Mini App кнопка открывает публичный browser login; самостоятельный вход сохраняет аккаунт и даёт текущие внешние методы.
**Architecture:** Один готовый UI-блок в main, existing openMiniBrowser и AccountIdentity. Backend/auth/billing/access contracts сохраняются; настоящий owned browser test доказывает границы.
**Tech Stack:** Existing React/TypeScript/Playwright и Go stdlib/native integration helpers; новые зависимости отсутствуют.
**Spec:** docs/superpowers/specs/2026-10-08-s36-browser-payments-design.md
**Method:** Native, один cohesive task, одно fresh Astra/high whole-branch review, один author Critical/Important RED→GREEN pass; no re-review.
**Base:** fresh origin/v2 `0d19b02669a90c335a7ac62d5062de4715687706`; PR→v2, без codex prefix.
**Authority:** #34/2026-10-08-s36-browser-payments-v1/comment6056519052; автономные документы/исполнение/необходимые manual merges уже разрешены.

## Global Constraints

- Production/real money/live Telegram/live Happ/VPN/Mac trust не входят в этот этап. Provider form перехватывается fixture до сети.
- Реальная локальная панель только3X-UI3.7.0; SMTP с TLS; все данные/accounts/secrets принадлежат тесту.
- Public URL только current origin+/login и выбранный en query; без account/order/quote/amount/initData/bearer/CSRF/email/key/hash.
- Независимый email/password login, без WebView cookie/storage sharing, registration/merge или автоматической Stars cancellation.
- Existing Accounts UUID/session revocation и Payments/current billing guards сохраняются; денег/доступа от SDK или successURL нет.
- Только существующие helpers/stdlib/native styles; никакого new API/migration/module/process/dependency/identity reader.
- ru/en, native keyboard/focus/help association и375px без overflow; normal web/admin без Mini блока.
- Каждый Ruling хранится с причиной и ценой; до удаления SDD архивируются все решения/minors/checks.

## Review Focus

- Уже существующая cookie другого аккаунта: manual login остаётся обязательным, signed Mini/cookie identity не смешиваются.
- Initial email отзывает Mini session: header browser entry работает после ended, UUID/VPN/trial сохраняются.
- Dirty route/query/hash и повтор: public URL не переносит приватные параметры, repeat не создаёт order/control.
- Older SDK с отсутствующим/ошибающимся openLink и keyboard action: синхронный existing fallback с noopener,noreferrer.
- Active/unknown Stars/pending order/disabled external method: навигация не снимает owner guards и не выдаёт доступ; current С35 owner checks и pending/no-proof native case сохраняются.

---

### Task 1: Явный переход и доказательство независимого входа

**Files:**
- Modify: `web/src/main.tsx` — общий Mini payment-блок/header public login.
- Modify: `web/src/MiniApp.tsx` — ru/en copy, own HTTP login intercept.
- Test: `web/tests/mini-app.spec.ts` — existing signed SDK fixture/URL/header/setup/empty/browser/fallback; preserve old assertions.
- Test: `web/tests/real.spec.ts` — conditional `TEST_BROWSER_PAYMENTS_FILE` case.
- Modify: `web/tests/safe-reporter.ts` — existing safe checkpoint whitelist adds BROWSER only.
- Test: `backend/tests/native_client_telegram_test.go` — `TestNativeMiniBrowserPayments` in existing native test package.
- Modify test helper: `backend/tests/native_stars_test.go` — optional `rub ...string` in nativeStarsFixture, overriding RUB before immutable insert; existing default calls unchanged.
- Evidence: `docs/superpowers/evidence/2026-10-08-s36-browser-payments.md`.
- C35 delivery record only: checked step5 and actual merge/release in already completed plan/evidence; no C35 product changes.

**Interfaces:**
- Consumes: `miniText(lang:Lang)`, `openMiniBrowser(url:string):void`, `link(path:string,lang:Lang)`; existing AccountIdentity/auth/catalogue/order public APIs.
- Consumes test-only: `openMode`, `nativeStarsFixture`, `nativeStarsInit`, `launchNative`, `f.signup`, `f.letters`, `assertNativePanel`, safe reporter.
- Produces: exact ru/en payment button and clean public login URL; no exported/backend contract.
- Native test input0600: signed init_data, owned email/password, other owned cookie, test-only TLS control_url; values never argv/URL/log.
- Test-only POST /code accepts a UUID challenge, matches owned recipient+actual delivered SMTP letter, returns its8-digit code; no product auth shortcut or public callback.

- [x] **Step 1: Write rendered failing checks**

Add `Mini App other payments ru/en` on cabinet/catalogue/renew/change-plan/order:
`button('Other payment methods')` / ru exact name; focus+Enter opens only origin/login(locale); setup-link→existing identity; dirty URL strips all but selected locale; repeat preserves POST count. Add existing SDK missing/throw fallback arguments; ended/expired/no SDK and normal browser no private payment button.

Change existing header URL expectation to /login with all original privacy/session assertions retained. Mini external-order negative checks still forbid real Pay/Report/checkout form while permitting the new public payment-navigation button.

Example exact check:

```ts
const action=page.getByRole('button',{name:'Other payment methods',exact:true});
await action.focus();await page.keyboard.press('Enter');
expect(await page.evaluate(()=>(window as any).__sdk.opened)).toEqual(['http://127.0.0.1:4173/login?lang=en']);
```

Run: `npm --prefix web run test:e2e -- tests/mini-app.spec.ts`
Expected: FAIL because button is absent and old header still /cabinet, not fixture/parser/prerequisite errors.

- [x] **Step 2: Write current native/browser failing check**

In native_client_telegram_test.go add TestNativeMiniBrowserPayments guarded by RUN_BROWSER_TESTS=1. Reuse nativeStarsFixture(t,"12345"), enable owned YooMoney config, launch current Go graph; create another owned web account/cookie. Existing helpers verify actual SMTP.

Real Playwright case uses two independent contexts (including that wrong cookie): signed Mini consent/trial; payment navigation uses public login only; setup first email via real code/password/consents; old Mini bearer401; ended removes button but retains header; external manual login gives same /me UUID. Select YooMoney RUB12345 order, intercept provider POST, return successURL, assert order pending/access unchanged.

Go asserts before/after account vpn_id/sub_id/panel_key, original trial1grant/1operation + real frozen panel target;1 pending YooMoney order,0receipts/0purchase access; other account untouched. Private fixture/control/error output follows existing safe patterns.

Run: `env RUN_BROWSER_TESTS=1 NATIVE_DOCKER_STATE=$PWD/.superpowers/acceptance/native-docker TEST_DATABASE_URL_FILE=$PWD/.superpowers/acceptance/c36-browser-payments/database-url TEST_REDIS_URL_FILE=$PWD/.superpowers/acceptance/c36-browser-payments/redis-url go -C backend test ./tests -run '^TestNativeMiniBrowserPayments$' -count=1 -timeout=10m`
Expected: FAIL at missing Other payment methods/header public login, not missing infrastructure. Native immutable RUB helper changes are test inputs, not product behavior.

- [x] **Step 3: Implement the minimum UI**

main imports existing openMiniBrowser; one derived browserLogin URL feeds header and button. Inside ready Mini children append section only for five payment routes, including existing cabinet root aliases. Native button calls helper directly; accessible help and setup anchor use existing card/account-links/primary styles. MiniApp copy exact spec ru/en; own HTTP interceptor adds exact /login. No async read or auth state added.

Run: `npm --prefix web run typecheck`
Expected: exit0; existing SDK/helper/type contracts agree.

- [x] **Step 4: Prove GREEN and connected UI consumers**

Run: same focused Mini command, then same native command, plus `npm --prefix web run test:e2e -- tests/account-identity.spec.ts tests/stars.spec.ts`.
Expected: all PASS, exact same UUID/real TLS panel, external pending/no proof preserved, existing Stars/identity UI unchanged. Read actual test outputs; don't count provider form interception as real money/default OS browser.

- [x] **Step 5: Full current source verification**

Run: `npm --prefix web run test:e2e`; `npm --prefix web run typecheck`; build is included in Playwright.
Run: `env RUN_BROWSER_TESTS=1 TEST_DATABASE_URL_FILE=$PWD/.superpowers/acceptance/c36-browser-payments/database-url TEST_REDIS_URL_FILE=$PWD/.superpowers/acceptance/c36-browser-payments/redis-url go -C backend test -race -count=1 -timeout=20m ./...` with NATIVE_DOCKER_STATE unset (exact Behavior CI transport); real3.7 proof from Step4 retained.
Run: same database files `poetry run python -m unittest discover -s tests -v`; `go -C backend vet ./...`; `make -C backend generate`; `npm --prefix web run api:generate`; `python3 deploy/acceptance/check_names.py`; `git diff --check`.
Expected: actual full suites PASS, generators keep all39 existing generated files identical, no backend/runtime diff. Record real counts/exit/source hashes and old failures honestly; no unchanged expensive retry.

- [x] **Step 6: Commit, ledger and single final review**

Commit verified scoped source/tests/evidence with Conventional Commit + Co-Authored-By; task-done uses focused Mini+native command after all required checks. Produce full branch package; one fresh Astra/high reviewer gets literal5 Review Focus items, spec/plan/ledger/exact base/source. Root re-grades findings and all declined behavior, applies one Important/Critical RED→GREEN fix pass with whole green suites, defers Minor; no re-review.

Expected: one accessible review and complete actual evidence; no issue Done yet.

- [ ] **Step 7: Deliver and continue**

After whole-green archive every Ruling/cost/minor/review/check/hash, delete only this plan SDD. Verify branch/upstream/SSH, push; PR→v2, attach it. All exact-source Platform and PR images must PASS. Check fresh target, actual dependency graph and exact source; manual source-SHA guarded merge, verify2parents/tree. Verify actual v2 prerelease/annotated tag/3OCI indexes+6source/version labels. Canonical completion,5DoD checked/#34closed/ProjectDone; continue next roadmap scenario. Target movement alone never restarts CI/source.

Expected: source/local/CI/merge/release have separate proofs; production remains excluded.
