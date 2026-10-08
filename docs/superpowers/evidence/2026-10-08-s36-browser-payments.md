# С36 — локальная приёмка перехода к внешней оплате

Владелец [#34](https://github.com/ekho/3xui-shop/issues/34), решение `2026-10-08-s36-browser-payments-v1` / [comment6056519052](https://github.com/ekho/3xui-shop/issues/34#issuecomment-6056519052).
Ветка `feature/s36-browser-payments`, база свежей `origin/v2` — `0d19b02669a90c335a7ac62d5062de4715687706`.
Метод Native: один cohesive task, одно fresh Astra/high whole-branch review, один author Critical/Important pass; без повторного ревью.

## Поведение

На готовых Mini cabinet/catalogue/renew/change-plan/order экранах отдельная кнопка открывает публичный `/login` через существующий `openMiniBrowser`. Cabinet root aliases тоже поддержаны. URL содержит только origin, login и выбранный `lang=en`; входные query/hash, аккаунт, заказ, сумма, Telegram bearer и VPN-ключ исключены. Header использует тот же login URL, включая состояние завершённого Mini-сеанса.

Постоянная помощь предлагает email/password этого аккаунта. Setup-ссылка ведёт в существующий AccountIdentity. Добавление первого email, отзыв сессий и самостоятельный browser login остаются у Accounts; текущие quote/order/payment/Stars/access правила остаются у прежних владельцев. Изменённый продуктовый код ограничен `web/src/main.tsx` и `web/src/MiniApp.tsx`.

## Проверки

| Проверка | Фактический результат |
| --- | --- |
| Mini rendered RED до product edits | exit1, 16 failed/16 passed, 238.481s; отсутствующий CTA и прежний header `/cabinet` |
| Native RED после исправления test fixture | exit1, 35.741s; signed session + trial дошли до `public_navigation`, CTA отсутствовал |
| Mini/identity/Stars rendered GREEN | 84/84, exit0, 39.715s |
| Current native + browser + real TLS3X-UI3.7.0/SMTP | exit0, 12.707s; Go package10.980s |
| Task-done after verified commit79b4399 | Mini34/34 + actual native10.128s, exit0/35.334s |
| Полный web | 418/418, exit0, 180.994s |
| Полный Python | 110/110, exit0, 15.331s; без skips/failures |
| Go vet, TypeScript, semantic names, runtime config | exit0 |
| Go/sqlc + TypeScript API generation | exit0; все39 generated files побайтно совпали с исходным HEAD727c305 |
| Полный Go race/JSON | 1216 individual tests/13tested packages,0fail/0skip; exit0,751.983s; HTTP750.110s |
| `git diff --check` | exit0 для текущих изменений |

Настоящий native сценарий начинался с cookie другого owned аккаунта в обоих независимых browser contexts. Signed Mini-сессия получила собственный UUID и настоящий триал. Первый email подтверждён кодом из фактически доставленного TLS SMTP письма; пароль и оба согласия заданы через UI. Старый Mini bearer получил401, private CTA исчез, header продолжил открывать login. Явный browser login вернул исходный UUID; SDK в обычном браузере не загружался.

В браузере доступен включённый YooMoney: настоящий заказ RUB12345/PC, provider POST проверен и перехвачен до внешней сети. Возврат по successURL оставил `pending`/`not_started`, без receipts и purchase access. UUID/vpn_id/sub_id/panel_key,1trial grant/1operation и настоящий frozen panel readback сохранены. Чужой аккаунт и его cookie/session не изменились; всего2accounts/1order.

Полный web проверяет ru/en, доступное имя/помощь, Enter/Space,375px, чистый URL при грязном query/hash, повтор без writes, SDK missing/throw fallback, logout/expired и обычный browser. Старые private/session/external checkout/Stars guards сохранены.

## Диагностика и ограничения

- Первый native run завершился до UI: новые test-only SQL references ошибочно использовали `email` вместо существующего `email_key`. Миграции/действующие tests прочитаны; обе ссылки исправлены, product code не менялся. Этот запуск не засчитан как product RED.
- Docker metadata preflight сначала ожидал bare tag; фактическая панель имеет существующий `3.7.0@sha256` pin. Проверены immutable ID, compose source, owned labels и running state; среды не менялись.
- Первое перечисление generated baseline нашло38Go-файлов и пропустило иной header web schema; snapshot не был записан. После чтения header все39 хешей восстановлены из pre-existing HEAD и сверены с результатом обоих генераторов. Команды генерации не повторялись.
- Runtime config supplemental check сначала вызван из корня, хотя использует cwd-relative script. Фактическая CI-команда выполняется внутри web; именно там проверка прошла. Начальный combined shell exit0 принадлежал позднему чтению header и не считался успехом проверки.
- Fixture signatures, cookie/email/code/password и database access находятся только в private files0600/owned test processes. Real reporter выводит безопасные checkpoint names, не ошибки с credentials.
- Provider form и Telegram SDK — заглушки; Go HTTP/jobs, SMTP с TLS и панель3X-UI3.7.0 — настоящие локальные потребители. Это не подтверждение реальных денег, live Telegram/OS-default browser, внешнего SMTP или production. Live Happ/VPN/Mac trust не затрагивались.

## Решения с их ценой

1. Новая ветка первоначально не имеет upstream: явный первый push устанавливает собственную feature-ветку после проверки SSH remote. Цена ошибки — последующий push должен отказать при другом upstream.
2. Spec/plan сохранены по принятому роадмапу даже для небольшой UI-доработки, без дополнительного approval или per-task agent. Цена — документы могут быть больше самого изменения, поэтому задача одна.
3. Использована постоянная setup-помощь вместо дополнительного identity reader. Цена — пропустивший настройку пользователь возвращается по явной ссылке; автоматическое создание/merge отсутствует.
4. Существующий `/mini-app/` тоже считается cabinet, как в main/BackButton. Цена — публичная кнопка есть ещё на одном уже поддержанном URL.
5. Старые negative payment-button assertions ограничены `.purchase-order`, checkout form/manual instructions проверки сохранены отдельно. Цена — будущий payment action вне этого компонента требует своего negative guard.
6. Полный Go-набор добавляет стандартный `-json` для точного подсчёта individual tests/packages/skips при прежних race/count1/timeout20m входах. Цена — больший private output, без изменения поведения тестов.

7. Step7 — post-review delivery, отдельно от task-done локальной реализации. Цена — локальное complete нельзя показывать как Issue/Project/roadmap Done.
8. Reviewer не оценивал live Telegram/OS browser/popup blockers: local criterion доказывает попытку SDK/fallback, не создание живого окна или login. Цена — отдельная проверка реальных клиентов может выявить блокировку открытия.
9. Reviewer не оценивал реальные деньги/provider callbacks/внешний SMTP: применены разрешённые owned stubs. Цена — реальная доставка и провайдер остаются непроверенными до отдельных ресурсов.
10. Reviewer не оценивал production/live Happ/VPN/Mac trust: исключено из scope, live Happ switching запрещено. Цена — локальный результат не доказывает production/live VPN трафик.
11. Reviewer не оценивал фактическую доставку С36/повторную внешнюю аттестацию С35: root проверяет current CI/manual merge/prerelease/tag/OCI перед Done, старые доказательства остаются историческими. Цена — внешнее состояние или публикация могут измениться и оставить delivery pending.

Deferred minors: нет.

## Доставка

Task completion прошёл на79b43995108fc7091e8ea179d114794dfc67e826. Одно fresh Astra/high whole-branch review:0Critical/0Important/0Minor, verdictYes; все5 focus items и raw evidence прочитаны, author fix passes0, no re-review. Точные PR CI, ручное guarded merge и фактический v2 prerelease/OCI proof ещё ожидаются. Issue/Project пока не Done. Продолжение после доставки — С25 рекламные приглашения.
