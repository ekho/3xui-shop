# С15 — локальная приёмка Heleket

Владелец [#22](https://github.com/ekho/3xui-shop/issues/22), контракт
`2026-10-06-s15-heleket-v1`; [spec](../superpowers/specs/2026-10-06-s15-heleket-design.md),
[Native plan](../superpowers/plans/2026-10-06-s15-heleket.md), [решения](s15-decisions.md).
Backend checkpoint e39eb76, включение проверенной migration19 c11949f, UI ce24c83.
Текущие product/fixture inputs проверяются по SHA256. Финальный source, review,
exact CI/manual merge/actual preview фиксируются отдельно в PR и #22;
delivery пока открыт.

Heleket имеет собственные API/ключ/IP/checkout hosts, immutable checkout,
River kind и receipt/proof namespace. Только authenticated final paid/paid_over
с точным USD principal и достаточной crypto оплатой разрешает доступ.
Unknown USD-net остаётся NULL, surplus не увеличивает срок. Подписанный webhook
и возврат браузера сами не подтверждают деньги. Shared private workflow
проверяется для обоих конкретных провайдеров.

| Проверка | Наблюдение |
| --- | --- |
| Connected crypto/config |33 roots PASS126.549s: оба провайдера, signature/API authority, exact decimal/time/NULL-net, recovery/expiry/drift/disable, race/replay; foreign key/IP/host/proof и одинаковый invoice UUID в разных namespaces. |
| Старые внешние методы |44 HTTP roots PASS101.935s: YooMoney/manual/YooKassa и purchase regression. |
| Полный Go race |313 roots/13 test packages PASS в совокупности. Первый full run512.358s: 12packages304roots PASS, единственный FAIL в backend/tests gateway fixture — новая Heleket route попадала в SPA200. После исправления весь затронутый package9roots PASS102.183s; остальные product inputs неизменны, skip/race отсутствуют. |
| Полный web |207 PASS101.842s. Focused все оплаты96 PASS32.692s: RU/EN keyboard, USD/period60 exact POST и сохранение метода, lost-response body/key, оба documented hosts, foreign provider/type/host/owner/quote/expiry/review deny. Behavioral RED: missing radio66.088s; legacy checkout/period choice26.309s. |
| Python consumer |105 PASS20.240s с защищёнными TEST FILEs; текущий legacy behavior сохранён. |
| Generators/vet/typecheck |PASS; generated Go/TS не отличаются от checkpoint. Product код не содержит временных diagnostic markers. |
| Stub self-check |Два провайдера PASS1.172s: own host/signature, unique order/frozen bytes,500, persistent invoice/info и paid/paid_over. RED1.187s обнаружил чужой Cryptomus host для Heleket. |
| Affected old Cryptomus native |Canonical startup25.504s/check50.161s PASS: new/paid, trial/paid_over, backend+stub restart, same bytes/invoice, one receipt/job/access при info replay. Own cabinet-c14 затем остановлен. |
| Own Heleket native |Startup18.804s/check40.115s PASS: ordinary Go/web images, cabinet-c15, TLS Mailpit/API, native3X-UI3.7.0, real River create/info/fulfillment, new/paid/trial/paid_over; ID/expiry+30days/limits сохранены, restart/replay не выдали второй доступ. |
| Paid-pending restore |PASS22.757s: исходный writer остановлен, dump/read-only restored DB, checkout/proof/financial digest сохранены; auth maintenance idempotent, restored writers не запускались. Исходный backend выдал один native access после запуска. |

Первый check сохранённого C14 runtime завершился по paid-wait timeout70.441s.
M19, merchant/key, Go DB Scan/transport/paid JSON подтвердились отдельно;
после пересоздания своего backend существующий job завершился. Причина первого
состояния не установлена и не объявлена исправленной. Последующая полная проверка
обычного образа, отличного от diagnostic image, прошла включая оба перезапуска;
Heleket check/restore также прошли. Failure/diagnostic records сохранены.
Временный код удалён до canonical acceptance и commit.

| AC / Review Focus | Исполняемое доказательство |
| --- | --- |
| AC01; совместимость |HeleketOrderAtomic/OrderGuards, retained credential config, old44 roots и оба crypto matrices; migrations15–18 неизменны. |
| AC02; frozen recovery |RequestAndRecovery/ExpiryAndDrift/ProviderHTTPFailures; оба native flows500/backend+stub restart и same request/invoice. |
| AC03; деньги/повторы |FundingBoundary30subcases/provider, PendingNullableDates, PaymentOffset, ObservationRace, ChangedFactsBlockPreparedAccess, ReviewCannotReconcile; native paid/paid_over/replay/restore. |
| AC04; HTTP граница |HTTPBoundary/HTTPAuthoritativeStatus: source/forgedXFF/signature/ordered Unicode/slash/numbers/duplicates/tamper/unknown/body limits; paid callback+pending info не выдаёт доступ. ProviderIsolation проверяет разделение двух провайдеров. |
| AC05; кабинет |Два concrete wrapper suites поверх общих rendered cases, full207. Legacy/new Heleket hosts разрешены, Cryptomus host чужой; same-currency period сохраняет метод. |
| AC06; backup/один writer |heleket-local.py check/restore, own TLS SQLite stub/native3X-UI3.7.0; own checkout digest, readonly restored DB, один исходный writer. |
| AC07 |Локальная matrix завершена; fresh Astra/high review, exact CI/manual v2 merge/actual preview остаются следующими gate. |

Private logs/durations/source SHA256, четыре concrete image IDs для каждого
native пути, reports/dump сохранены в `.superpowers/acceptance/c15-provider`.
Credentials, email и VPN links/IDs не публикуются. Source guard различает
первоначальный full Go и обновлённый test-only gateway mirror.

Команды с protected TEST_DATABASE_URL_FILE/TEST_REDIS_URL_FILE:
`RUN_BROWSER_TESTS=1 go -C backend test -race -v ./... -count=1`;
`poetry run python -m unittest discover -s tests -v`;
`npm --prefix web run test:e2e`. [Own Docker commands](../../deploy/purchase/README.md)
используют cabinet-c15, subnet10.253.15.0/28 и loopback ports.

Native info/replay не доказывают доставку webhook с vendor IP. Настоящие
merchant/API/hosted checkout/кошелёк/перевод/net/fiscal/public delivery и
production непроверены до ресурсов и С45–С47. Живой Happ/VPN/Mac trust и
Telegram/SMTP вне своего стенда не менялись.
