# С07 — операторские изменения подписки и компенсации

Статус: автономная спецификация по разрешению пользователя; выполняется после С48/С09.
Основание: [роадмап](../../roadmaps/2026-10-01-platform-roadmap.md),
`app/bot/services/vpn.py` и `/comp` в `app/support_bot/routers/admin.py`.

## Результат и операции

Карточка клиента React-admin выполняет отдельные действия с обязательной
причиной: начислить1..365 дней, назначить конкретный тариф/revision/период,
вернуть стартовый триал, сбросить трафик. Это операторские изменения доступа,
не платёжные факты; история покупок и оплаченный снимок не переписываются.

Компенсация сохраняет devices, текущий traffic limit, profile, счётчики и
UUID/sub_id/panel_key/server. Срок = max(подтверждённый expiry, now)+N суток.
Unlimited, perpetual expiry=0, VPN-banned и недоступный назначенный сервер
отклоняются, как существующий `/comp`. Account restriction/support-ban —
самостоятельные состояния и не снимаются этой операцией. При отсутствии
клиента разрешена выдача bonus devices/N дней без дополнительного traffic cap
на configured доступной панели; нельзя перенести identity на другой сервер.

Назначение тарифа сохраняет выбранную revision/период и применяет лимиты,
профиль, now+period и reset. Скрытый unlimited назначается отдельным С08,
не этой формой. Стартовый триал использует текущие configured period/devices/
traffic, regular и reset; требует существующего клиента, не повторяет гейт
первого бесплатного триала и не снимает VPN-ban. Сброс трафика не меняет
expiry/devices/лимит/profile и явно перенакладывает ban после panel reset.

## Исполнение и восстановление

Новый тип persistent access-operation использует существующие pgx/River,
idempotency, account/role locks и bounded panel transport; новая очередьвая
система и новый 3X-UI клиент не добавляются. В операции сохраняются actor,
reason, kind, неизменяемый snapshot и конкретный desired target. До первой
записи панели сохраняются абсолютный expiry и прежние identities; повтор
не вычисляет «ещё N дней» заново. Active операция сериализует все С07/С08
действия аккаунта и initial trial provisioning, не только один endpoint.

Конечные состояния pending/provisioning/applied/needs_review. Успех возвращает
ссылку на operation, не выдумывает синхронную гарантию завершения. Snapshot
подтверждается readback панели, С03 показывает последний применённый результат
и состояние незавершённой операции. Grant первого триала и его историю новая
операция не удаляет. Источник актуальных лимитов/expiry после новой операции
не остаётся ошибочно initial trial_operation.

3X-UI3.7.0 записывается только для совпавших panel_key/UUID/sub_id/server.
Установка абсолютных лимитов/срока допускает безопасную сверку/повтор desired
target; потеря ответа reset не разрешает слепой повтор, который сотрёт уже
новый трафик. Неоднозначная запись, частичный reset/membership или mismatch
переходит в needs_review, блокирует новые conflicting writes и сохраняет
пройденные шаги. Операторская сверка с причиной читает состояние и завершает
совпавший desired target; повтор неоднозначного destructive reset требует
явного решения с описанным последствием, а не автоматического retry.

## API и UI

POST `/api/v1/operator/clients/{id}/access-operations`: typed kind
compensate/assign_plan/starter_trial/reset_traffic, reason, поля только своего
kind и Idempotency-Key. plan_id/revision/period выбираются из С09 и проверяются
сервером; цена не принимается от клиента. GET operation и POST reconcile
защищены web operator session; actor только из неё. Unknown fields,
неоператор/revoked role/restricted actor, Origin/CSRF и oversized body отказали.
HTTP409 сообщает конфликт операции/версии/body/неприменимый вид действия.
Ответ и audit содержат UUID/метаданные, не credential/raw panel response.

UI объясняет разницу: «Добавить дни» не сбрасывает трафик; назначение тарифа
и стартовый триал начинают новый период и сбрасывают счётчики. Reset и
назначение требуют подтверждения. Во время POST inputs disabled; ошибки
сохраняют причину/ключ retry. RU/EN,375px, keyboard и статусы операции обязательны.

## Приёмка

1. Активному/истёкшему finite клиенту начислено1..365 дней от правильного
   основания; devices/limit/counters/profile/identity не изменились.
2. Unlimited/perpetual/banned/unreachable отказы не меняют БД/панель; новый
   bonus client создаётся один раз на разрешённой панели без переноса identity.
3. Assign revision и starter trial применяют правильные period/devices/traffic/
   groups/reset, сохраняют ban, оплаченный snapshot и UUID/sub_id/server.
4. Manual reset обнуляет счётчики, сохраняет expiry/лимиты и ban; потерянный
   response/partial effect честно становится needs_review без слепого reset.
5. Concurrent calls/lost response/restart/double worker и initial-trial race
   оставляют одну операцию/эффект; компенсация не начисляет N дней повторно.
6. Сверка mismatch/partial write сохраняет шаги/audit и защищает от второй
   conflicting mutation; совпавший readback завершает ту же operation.
7. Серверная role/CSRF/input/foreign-account защита и UI pending/error/retry,
   RU/EN/mobile/keyboard пройдены; С03 показывает актуальную подписку.
8. Actual own Docker3X-UI3.7.0/API/browser подтверждают изменения и identities;
   С01–С06/С48/С09 regression и backup/restore новых операций проходят.
