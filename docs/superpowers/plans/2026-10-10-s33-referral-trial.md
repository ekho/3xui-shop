# С33.Р7 — план реализации

**Goal:** один Telegram trial с реферальным сроком и прежними лимитами.
**Architecture:** bonuses выбирает и сохраняет benefit в транзакции обычного
subscriptions trial; прежний VPN worker подтверждает результат. No DDL.
**Tech Stack:** Go, pgx/sqlc, PostgreSQL, River, native Telegram SDK.
**Spec:** [контракт](../specs/2026-10-10-s33-referral-trial-design.md).

## Ограничения и review focus

Один процесс/go.mod/исполнитель; base origin/v2, PR v2; Python до #54.
Не менять общие fixtures или production. Сохранять constructor и callbacks.
Проверить consumed benefit даже при disabled flag; original web после link;
rollback финального audit после panel write; pending snapshot после restart;
нулевые/unlimited лимиты и отсутствие purchase rewards на trial.

## 1. Единственный реферальный триал

Files: bonuses `referred_trial.go`, `internal/queries/referred_trial.sql` и
generated store; subscriptions `service.go`, `trial.go`, `data.go`;
app `config.go`, `modules.go`, `referred_trial_config.go`;
deploy/acceptance/compose.acceptance.yml.

Interfaces: `ReserveTelegramTrialTx(ctx,tx,account,request,config)`
возвращает выбранный полный срок либо 0 для ordinary trial;
`RecordTelegramTrialAppliedTx(ctx,tx,account,request,operation)` подтверждает
только сохранённый резерв. Config hooks подключены единожды в composition.

- [x] Добавить failing `TestReferredTelegramTrialReservation`: configured
  7 days, один grant/job/referral reservation при восьми разных ключах,
  replay сохраняет IDs/period; ноль paid orders/referrer rewards.
- [x] Добавить flags/source/used/legacy и atomic queue/audit rollback tests.
- [x] Проверить RED, затем реализовать два bonuses hooks и существующие
  subscriptions transaction/outcome calls. Config defaults false/7,
  безопасные bool/int/overflow errors; period/traffic/devices snapshot.
- [x] `make -C backend generate`; focused Go-race/app boundary/config tests.
  Ожидание: PASS, нет generation drift, no-DDL.

## 2. Native приёмка и доставка

Files: `backend/tests/native_referred_trial_test.go`, config tests,
`docs/evidence/s33-referral-trial-acceptance.md`.

- [x] Signed Mini App + owned Bot API → actual HTTP trial + River → TLS
  panel: configured limits, concurrent replay, lost response/final audit
  rollback, module/worker restart на retained DB и operator reconcile.
- [x] Actual web/TG linking/unlink/alternative alias и immutable inviter;
  original web manual ordinary trial; disabled flags и unknown/self source.
- [x] Focused connected native/race + original #31/#49/#51 regressions;
  existing trial/referral ru/en browser checks, typecheck/build/vet/generation.
- [ ] Independent ordinary read-only whole diff review; исправить findings
  и повторить затронутую проверку. Записать реальные ограничения evidence.
- [ ] Conventional Commit + Co-Authored-By; проверить SSH remote/branch/
  upstream, push, PR v2. Один principal full Platform + три Image checks
  на final source. Duplicate cancellation только с checkout/tree receipt.
- [ ] Перед manual merge проверить exact HEAD, fresh target, dependencies,
  gates/effects; merge, issue Closed + Project Done, own fixture cleanup,
  передать root PR/SHA/CI/review/evidence без self-archive.
