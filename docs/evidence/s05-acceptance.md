# С05: локальная приёмка поддержки

Client/backend интегрированы в `745250f`. Техническая приёмка завершена:
реальная двухсторонняя browser поверхность и PG restore прошли вместе с С06
на `d6ae035` (30 PASS, 0 FAIL/BLOCKED). Свежий review выявил F1/F2 в общей
frontend-границе. F1/F2 исправлены с browser RED→GREEN, полный web64/64 GREEN;
окончательный статус **локально принято, все7 AC**. Focused actual API/UI
на `df34ef5`:6 PASS, 0 FAIL/BLOCKED, exit0; оба callers восстановили104 сообщения
без reload после управляемого lifecycle, с настоящими global sequence gaps.
Задержанный POST проверен для обеих сторон; физическое скрытие вкладки не заявлено. Команды/ревизии и findings —
в [сводном evidence](s03-s06-progress.md#финальный-review-и-один-проход-исправлений).

| AC | Требование | Доказательство | Статус / граница доказательства |
| --- | --- | --- | --- |
| 1 | Text/file/page50/close/reopen/auto-reopen, сохранение после reload | `TestSupportConversationFlow`, `TestSupportConcurrentDuplicateAndPaging`; actual оператор + два клиента, 52 сообщения и страницы50/2 | PASS; F1 browser RED→GREEN + actual104/104 на обеих страницах, controlled lifecycle |
| 2 | Lost/concurrent retry одна запись, mismatch409, draft сохранён | Service race и client failure/429/503 Playwright; actual повтор201/200 и mismatch409, одна запись | PASS; browser RED→GREEN, actual composer locked при POST обеих сторон, следующий binary file byte-exact200 |
| 3 | Stored → delivered только recipient ack; чужой/invalid sequence отказал | Service ack checks; browser operator3/customer4 отправляет3; actual симметричный render ack, sender own-ack409 | PASS, fixtures + actual browser |
| 4 | Ban отделён от VPN/restricted/history; revoked role закрывает запись | `TestSupportBanRoleAndQuota`, `TestSupportRevocationAndBanWinBlockedWrite`; actual CLI revoke403 и active subscription/key200 при support403 | PASS; native target/VPN сохранены |
| 5 | Foreign/forged actor/CSRF/Origin/file/quota/rate отказали | `TestSupportHTTPBoundaries`, quota/rate/Redis tests; actual download byte-exact200/foreign404, singleton nosniff/no-store, input400/CSRF403/Origin403 | PASS; oversize/quota/race — real-PG/Redis fixtures |
| 6 | Text/bytes/history/role переживают PG restore; sessions/proofs отозваны | Actual `pg_dump/restore`: row digests, bytea/receipts/роль/actor совпали; old session401, fresh login и file/key200; maintenance SQL отзывает proofs | PASS; новые локальные данные, без legacy transcript |
| 7 | Client/operator ru/en/mobile/keyboard с actual API, `/info` эквивалент | Actual RU/EN client/operator375px, Enter, card; client10 и operator10 в общем browser59 | PASS; F1/F2 исправлены, web64 GREEN и focused actual UI6 PASS, все новые роли отозваны |

Coordinator GREEN: `go test ./internal/s01 ./internal/httpapi -race -count=1
-run Support` (оба пакета exit0); `npm run test:e2e -- s05.spec.ts` (10/10).
Перед интеграцией воспроизведены и исправлены NUL → PostgreSQL503 и ошибочный
recipient ack по sequence собственного сообщения. Исходные RED/GREEN логи
сохранены локально; message bodies/files/actor identities в evidence не раскрыты.
Telegram relay/история старого бота остаются С37/С46 и здесь не выдумываются.
