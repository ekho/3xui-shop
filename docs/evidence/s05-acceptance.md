# С05: локальная приёмка поддержки

Client/backend интегрированы в `745250f`. Общий статус **Pending**: настоящая
двухсторонняя browser поверхность и restore проверяются вместе с С06.

| AC | Требование | Focused evidence | Остаток |
| --- | --- | --- | --- |
| 1 | Text/file/page50/close/reopen/auto-reopen, сохранение после reload | `TestSupportConversationFlow/ConcurrentDuplicateAndPaging`, client browser | Actual оператор + два клиента |
| 2 | Lost/concurrent retry одна запись, mismatch409, draft сохранён | Service race и client failure/429/503 Playwright cases | Actual lost-response retry |
| 3 | Stored → delivered только recipient ack; чужой/invalid sequence отказал | Service ack checks; browser regression operator3/customer4 отправляет3 | Симметричный operator ack и actual browser |
| 4 | Ban отделён от VPN/restricted/history; revoked role закрывает запись | `TestSupportBanRoleAndQuota`, role/ban lock race test | CLI revoke С06 и actual subscription/key invariance |
| 5 | Foreign/forged actor/CSRF/Origin/file/quota/rate отказали | Support service/HTTP boundary tests: NUL400, download-only headers, quotas, denied inputs | Actual download и direct HTTP без роли |
| 6 | Text/bytes/history/role переживают PG restore; sessions/proofs отозваны | Source bytea/FK и прежний restore механизм С01/С02 | Настоящий dump/restore с новыми С05/С06 данными |
| 7 | Client/operator ru/en/mobile/keyboard с actual API, `/info` эквивалент | Client Playwright10/10/typecheck; operator принадлежит С06 | Общая browser приёмка С06 |

Coordinator GREEN: `go test ./internal/s01 ./internal/httpapi -race -count=1
-run Support` (оба пакета exit0); `npm run test:e2e -- s05.spec.ts` (10/10).
Перед интеграцией воспроизведены и исправлены NUL → PostgreSQL503 и ошибочный
recipient ack по sequence собственного сообщения. Исходные RED/GREEN логи
сохранены локально; message bodies/files/actor identities в evidence не раскрыты.
Telegram relay/история старого бота остаются С37/С46 и здесь не выдумываются.
