# С31 — локальная приёмка способов входа

Owner #29, contract `2026-10-07-s31-account-identity-v1`.
Base `f9ddf123b3b6be92d2e83b0b15b3b8909a29c5cd`;
product checkpoint `6fd9ec1b7d06e0fc931bbd2539383c1d61e671db`.
Native inline: четыре авторские задачи, затем один свежий whole-branch
Astra/high reviewer, один авторский Critical/Important RED→GREEN pass;
Minor откладываются, повторного ревью нет. На этом checkpoint ревью и доставка
ещё предстоят. Состояние GitHub/PR/выпуска фиксируется отдельно после публикации.

## Что проверено

Telegram-only клиент добавляет подтверждённый email/пароль на тот же UUID.
Независимый web-клиент подтверждает текущий пароль и связывает Telegram
одноразовым кодом через signed Mini App. Отвязка резервирует прежний Telegram ID
за владельцем. Оператор с действующей ролью/паролем, причиной и подтверждением
начинает восстановление: старый Telegram немедленно блокируется, подтверждение
письма выдаёт первый независимый вход на том же аккаунте.

Сохраняются source, legacy ID, ограничения, история, подписка, VPN ID, sub ID,
panel key и финансовые guards. Занятые каналы не объединяются. Отзыв роли
оператора навсегда отзывает выданные им recovery proofs; повторная выдача требует
нового действующего решения. Истечение proof или ошибка SMTP не снимают
quarantine. Recovery не выполняет автоматический login.

| Проверка | Результат |
| --- | --- |
| Полный Go `go test ./... -count=1 -race -timeout=20m -json` с TestKit, real-browser и native Docker | 922 PASS, 0 FAIL; все 13 пакетов с тестами PASS |
| `go vet ./...` | PASS |
| Полная обычная browser suite, отдельно от build/embedded browser, собственный output | 353 PASS, 2.7 min |
| C31 browser identity/recovery cases | 19 PASS в полном прогоне; RU/EN, keyboard, expiry/conflict, late responses, logout и idempotency |
| `poetry run python -m unittest discover -s tests -v` | 110 PASS, 15.749 s |
| `npm run typecheck`, production `npm run build` | PASS |
| Штатная SQL/API генерация | повторная генерация не меняет generated sources |
| OpenAPI сравнение с base | прежние 77 paths / 126 schemas deep-equal; добавлены 8 paths / 11 schemas |
| Миграция 24→25 / пустой Down / отказ потери identity facts | PASS; старые account/session/source/hash/consents сохранены, отказ атомарен |
| Native trial → recovery с реальной 3X-UI 3.7.0 и TLS SMTP → реконструкция lifecycle | PASS, 6.479 s |
| Собственный compiled backend: stop/start между quarantine и подтверждением | PASS; тот же UUID, activation operation, panel client и subscription grant |
| HTTPS/Caddy: 6 фактических маршрутов, no-store/no-referrer/CSP и runtime config | PASS |

Полный Go использует файл конфигурации TestKit и абсолютный путь состояния
native Docker. Обычный browser suite выполняется последовательно относительно
сборок и Go embedded real browser. В fixture-native Telegram отключён, письмо
доставляет собственный TLS Mailpit. Собственный оператор после compiled-process
проверки лишён временной роли. Внешние провайдеры используют только заглушки.

Фактические HTTPS переходы: `/recover-account`, `/cabinet/identity`,
`/mini-app/cabinet/identity`, `/config.json` — 200;
`/internal/nope` — 404; anonymous `/api/v1/me/identity` — 401.
Прежний hash bootstrap/CSP сохранён; web и Mini имеют свои frame-ancestors.
Публичный runtime config содержит только пять прежних публичных параметров.

## Наблюдённые RED→GREEN

- Task 1: отсутствующие initial-email HTTP операции дали 404 вместо 200/202
  (4.192 s); после реализации connected/race и реальные TLS письма PASS.
- Task 2: отсутствующий web link-proof дал 404 (2.682 s), браузер не имел
  выбора существующего кабинета; после реализации link/replay/retirement
  guards, signed initData negatives и browser cases PASS.
- Task 2 audit: принятые legal versions отсутствовали в reason (3.204 s);
  минимальный audit helper записывает публичные версии, GREEN 3.212 s.
- Task 3: отсутствующая recovery операция дала 404 (3.094 s);
  UI recovery/selected-client отсутствовал. Неудавшийся web logout терял CSRF,
  поэтому повтор получал 403. Новые формы и корректный lifetime CSRF дают GREEN.
- Recovery 503 диагностирован до завершения: ошибочный credential challenge ID
  использовался в audit FK на trial request. Убрана только чужая FK-ссылка,
  сохранён account/operator/reason/legal audit; focused backend GREEN.

Ошибки compile/schema/fixture не выдаются за поведенческий RED:
generated enum cast/import, реальный публичный GetIdentity, формат старого sub ID,
policy acceptance triple, правильный результат pendingMail, поиск точного
получателя/назначения письма, абсолютный путь native state и корректный cookie
processor. Их исправления меняли только выявленные входы проверки.

## Review Focus

1. Concurrent email claim/registration versus same-account enrollment:
   один owner, без частичных credentials — Task 1 race test.
2. Detached TG login racing link/unlink/recovery:
   reservation/quarantine запрещают новый аккаунт — Task 2/3 connected tests.
3. Used/expired link proof после logout/credential change/другого TG:
   никаких повторных grants — Task 2 replay/revocation tests.
4. Revoked/restricted recovery operator после отправки письма:
   подтверждение не выдаёт credentials и quarantine остаётся — Task 3 role test.
5. Late HTTP response при смене клиента/unmount и failed logout:
   другой клиент/CSRF/секрет не меняются — Task 3 browser tests.

## Native rulings и цена решения

1. Добавить account-identity.spec.ts в существующий явный Playwright testMatch:
   иначе новые тесты вообще не выбирались (No tests found, не product RED).
   Цена ошибки: один дополнительный файл/время обычного suite; real-mode
   selection остаётся прежним.
2. Записывать только принятые публичные legal versions в identity audit:
   исторический consent должен переживать последующие обновления.
   Цена: короткий reason без нового audit metadata framework.
3. Recovery restricted target выдаёт credentials, сохраняя restriction:
   принятая спецификация запрещает обход ограничения, а не подтверждение почты.
   Цена: клиент всё равно получает 403 при входе до отдельного разрешённого
   снятия ограничения; byte-equivalent unrelated facts и 403 проверены.
4. Отзыв роли оператора отзывает его recovery proofs под существующим email
   guard, даже после regrant. Цена: поддержка выдаёт новое подтверждение,
   quarantine остаётся; отдельная role-epoch таблица не нужна.
5. Удалить неверную trial-request FK из credential recovery audit, сохранив
   account/operator/reason/legal, вместо расширения shared audit schema.
   Цена: нет прямого join по credential challenge в этом trial-only FK.
6. Сохранить исходное JSON представление OpenAPI файла с расширением .yaml:
   временная YAML сериализация создавала только форматный churn.
   Цена: расширение остаётся исторически неточным; семантика старых maps проверена.
7. Сериализовать сборки и проверки, использующие web/dist и test-results:
   preview читает dist во время тестов. Цена: один ограниченный повтор полного
   browser suite в отдельном output; причинность первого timeout не доказана.

## Отклонение проверки и пределы доказательств

Coordinator запустил первый полный browser suite одновременно с production build
и Go embedded browser, хотя они используют общие dist/output. Первый прогон:
352 PASS, один прежний purchase Select-plan timeout; все 19 C31 случаев PASS.
Incident `c31-local-web-dist-overlap`, owner coordinator, C10; загруженные правила
TradeOS 2.0.0 agent-workflow. Гипотеза: сборка меняла assets, обслуживаемые preview;
удалённый shared test-results не позволил подтвердить точную причинность.
Исправлено собственное исполнение: exclusive dist, отдельный output,
C07 разрешил один повтор с этими изменёнными входами. Он дал 353 PASS/0 FAIL.
Product patch из этого timeout не выводится. Предыдущие независимые Python,
native SMTP/panel и compiled restart доказательства сохранены.

Полная внешняя SMTP/performance приёмка и реальные платёжные callback остаются
за владельцами С45–С47. Реальные деньги, внешние Telegram/SMTP, production,
живой Happ/VPN и доверие Mac не использовались. Down намеренно отказывает при
новых identity facts: восстановление старой версии требует отдельного runbook
с сохранением этих данных.

