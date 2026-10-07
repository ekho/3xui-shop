# С20 — Локальная приёмка разбора оплаты и возврата

Владелец [#27](https://github.com/ekho/3xui-shop/issues/27), контракт
`2026-10-07-s20-v1`. [Спецификация](../specs/2026-10-07-s20-payment-resolution-design.md),
[Native-план](../plans/2026-10-07-s20-payment-resolution.md).
База `origin/v2`: `76bfe63fbebd8fa3b2b49a5280dfe7ac83c41894`.
Реализация и исправления до финального ревью: `ca6ad355bcf31cbeb66eb336b2f21af04299469b`.
Локальные наборы прошли на неизменных входах реализации. Один свежий финальный
review `76bfe63..a33d498` завершён; два Important исправлены одним авторским
проходом RED→GREEN. Доставка pending; задача остаётся открытой.

## Проверяемые свойства

- Выбранный старый заказ/поступление читается с проверкой владельца, роли,
  Origin/CSRF и no-store. Возврат имеет источник `operator`, явные согласия,
  внешний reference, причину, тот же UUID при повторе и один audit/ledger commit.
- Все пять внешних методов сохраняют исходные quote/hash/PAID/receipt/proof.
  RUB gross, неизвестный net и точные decimal crypto amounts остаются разными
  фактами; смена актива, защищённые/незачисленные/нулевые деньги отвергаются.
- Общий funding guard закрывает последующую выдачу; только не-applied операция
  funding-поступления становится skipped. Extra/late receipt рассматривается
  отдельно. Applied-доступ сохраняется; новые продажи разрешаются серверной
  политикой после полного возврата невыданного заказа.
- UPDATE/DELETE подтверждённого журнала и Down с сохранёнными возвратами
  отвергаются. Отзыв роли проверяется заново даже при replay; активный
  account-access executor препятствует записи возврата.
- Browser проверяет RU/EN, клавиатуру/mobile/focus, точные деньги, consent,
  смену клиента/поступления при позднем ответе, потерянный ответ с тем же key,
  изменённый ввод с новым key, конфликт и курсор refunds. Защита target не
  отнимает роль у действующего оператора; настоящий отзыв роли скрывает данные.

## Фактические проверки

| Проверка | Результат |
| --- | --- |
| Focused API/payments/boundaries | PASS; новая HTTP-дорожка дала настоящий RED404 до реализации |
| Общий purchase projection при MaxConns=1 | RED SERVICE_UNAVAILABLE после2s; GREEN после удаления pool re-entry query |
| Browser payment resolution | Task2:9/9; protected-target refusal отдельно RED→GREEN |
| Весь browser-набор | До review:310/310; после исправлений:315/315 PASS, 145.877s |
| Python regression | 110/110 PASS, 17.176s |
| Generation / names / runtime config / go vet | PASS; generated drift отсутствует |
| Container build backend/web/bot | PASS, 32.055s; после UI-исправлений обновлён gateway PASS15.766s |
| Local HTTPS/container smoke | PASS, 26.988s; после UI-исправлений PASS27.182s: runtime config, ingress, file secrets, повторные миграции, rollback/restore |
| Native Go с реальными TLS SMTP/3X-UI3.7.0 | PASS, 42.761s; Telegram API simulated |
| Новый native refund/restart сценарий | PASS, 35.693s; prepared и applied ниже |
| Retained renewal/plan-change migration tests | Реальный RED; GREEN4 subcases после raw-byte проверки и восстановления текущей схемы |
| Общий Go race + connected browser | 820/820 PASS, 13 пакетов с тестами, 800.978s; без пропущенных тестов, timeout и изменения входов |
| Connected real backend/browser после исправлений UI | TestWebTrialFlowAndFailures PASS, 22.594s |

Native-команда: `python3 deploy/purchase/payment-resolution-local.py check`,
с собственным `LOCAL_STATE_DIR`/проектом `cabinet-c20`. Требования и запуск —
[purchase README](../../../deploy/purchase/README.md). 3X-UI3.7.0 закреплена digest
`3b3131f1876e6bf35063a9ec4dd1c594e4525180bfc2e1c477dcc8a3c9550ca1`.
Credentials и исходные proof/logs остаются в закрытом каталоге приёмки.

| Native fixture | Наблюдение после возврата и двух фактических рестартов |
| --- | --- |
| Prepared purchase поверх текущего trial | Сохранённый target/desired/steps/write-reset flags прежние; операция skipped, последующее задание завершилось без выдачи; исходные деньги/receipt прежние; одна операция и один возврат |
| Уже applied purchase | Applied остаётся applied; target/desired/steps и исходные деньги/receipt прежние; одна операция и один возврат |
| Обе fixture | Один image и три разных StartedAt на fixture; прежние client UUID/subscription identity, expiry, limitIp, traffic limit, memberships и ненулевые up/down counters |

Первый общий Go-запуск завершился timeout и не считается PASS. Реальный
one-connection regression выявил дополнительный pool query в общей проекции
заказа; флаг fully_refunded теперь вычисляется в существующем SQL scan.
Следующий полный запуск завершился за863.427s без timeout, но выявил старые
migration-тесты: после частичного Down новый пустой refund table уже отсутствовал.
Проверка raw stored order bytes до Up сохранена; текущий binary проверяется после
восстановления своей схемы. Production миграции и их history guards не ослаблены.
Финальный `RUN_BROWSER_TESTS=1 go test ./... -count=1 -race -timeout=20m -json`
прошёл: SHA-256 входов `6874181cc67a0cb86a9448334cf5e2b3f189511491cbc3e04054f793213b49fe`,
лога `ce07efef181c8c55fb61d1000c1ff3ac2047348c34e7a09532e2081014591bb3`.
После UI-исправлений входы backend/Python проверены повторно и не изменились;
общий browser-набор, connected consumer, typecheck/build выполнены на новых
входах UI. Приёмка не выводится из одного старого SHA.
После review SHA-256 входов browser-набора
`cd8e2c1b616b2f765f66e0675655f0067e1d733118fa04324f875a09b2339874`,
лога `35181a5ce49f92a57712bed924ce5e7a060f98548771441e544b951ecff11b2e`.

## Финальный review и один исправительный проход

Fresh reviewer `c20_final_review`, Astra/high: Critical нет, F1/F2 Important,
M1 Minor. F1 воспроизведён двумя browser-тестами: при обновлении order-case с
поступления A на B согласия/ввод A оставались активными; подтверждение A могло
выдаваться за возврат B. Draft, согласия, retry key и saved теперь привязаны к
фактическому receipt ID. При неизменном ID повтор сохраняет прежний key.
F2: `pending` после payment-method-mismatch и полного возврата блокировал
каталог при `can_purchase=true`. Fully-refunded exemption теперь не зависит
от старого payment status; false/отсутствующее разрешение сервера проверены.
Наблюдались три настоящих RED и 20 focused GREEN, затем 315/315 suite GREEN.
Повторного review нет.

Deferred minor M1: выбранная карточка поступления показывает цену, полный
исторический срок/устройства/traffic/profile доступны в истории заказов.
Это дополнительная навигация, денежные факты и сохранение доступа не скрыты.

## Native rulings и цена ошибки

1. Audit ID равен refund ID; operation_id сохраняет trial namespace.
   Цена ошибки: С29 должен связывать возврат по ID журнала.
2. Использованы реальные PurchaseOrder.tsx/payment-history.spec.ts вместо
   предполагаемых Purchase.tsx/e2e. Цена ошибки: перенос двух ссылок.
3. Catalogue учитывает полностью возвращённый невыданный заказ, сохраняя
   server can_purchase и прежний applied gate. Цена ошибки: неверная доступность
   новой покупки; серверная проверка остаётся обязательной.
4. Final review/delivery вынесены после завершения implementation tasks по
   Native-контракту. Цена ошибки: локальную готовность можно принять за доставку;
   issue остаётся открытой до фактических CI/merge/release.
5. Migration-тесты проверяют сырые сохранённые данные до Up и текущий сервис
   после Up, поскольку Down steps независимы. Цена ошибки: можно скрыть
   несовместимость; прежний history guard и сравнение исходных bytes сохранены.
6. Внешний payout не оценивается review: принят operator attestation уже
   выполненного перевода. Цена ошибки: ledger не является provider proof.
7. Partial external/native Stars refund вне С20 по принятым границам.
   Цена ошибки: таким запросам понадобится отдельный сценарий.
8. Автоматический отзыв доступа/дней исключён: принято keep-access и отдельное
   действие С07/С08. Цена ошибки: оператор должен ограничить доступ отдельно.
9. Реальные callback/public HTTPS/SMTP/Telegram/production не оцениваются
   локальным review. Цена ошибки: PASS не доказывает готовность С45–С47.
10. Audit operation_id не служит refund linkage: сохраняется trial FK, ID
    журнала связывает возврат. Цена ошибки: С29 должен идти по refund ID.
11. Текущий binary после частичного Down не является режимом эксплуатации.
    Цена ошибки: развёртывание требует совместимой схемы/binary/backup.
12. CI/merge/dev release/images проверяет координатор после review.
    Цена ошибки: clean review сам по себе не является доставкой.

## Ограничения

YooMoney notification и подтверждение возврата синтетические. Реальный перевод,
внешний HTTPS callback/SMTP/Telegram, provider payout и production не проверены.
Текущий доступ не отменяется автоматически; partial external refunds и native
Stars-refund остаются отдельными сценариями. Живой Happ/VPN и системное доверие
сертификатам не менялись. Откат схемы с сохранёнными возвратами запрещён; нужен
совместимый binary/backup и разрешённое окно обслуживания.
