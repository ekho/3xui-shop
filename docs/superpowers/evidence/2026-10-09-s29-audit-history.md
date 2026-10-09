# С29 — локальная приёмка журнала действий

Дата: 2026-10-09. Issue [#39](https://github.com/ekho/3xui-shop/issues/39).
Решение 2026-10-09-s29-audit-history-v1; база a093add4e76bb36039d21ba8ba00ebfc0f788d00.
Native execution, один свежий финальный reviewer и один авторский проход исправлений.

## Реальный локальный путь

Существующий audit_reports владеет native, legacy и system history. Операторский
HTTP reader проверяет действующие права в caller transaction, не заимствуя второе
соединение. React-admin читает общий журнал и лениво открывает тот же компонент
в карточке клиента. Private legacy payload не возвращается в API или DOM.

Свежий compiled cmd/server обслуживал HTTP, TLS SMTP, импорт CLI и scheduler.
Fake Bot API проверил negative supergroup/General, plain text и отсутствие reason,
имени, email, body/attachment/key. Main Telegram отключён. Запись claim фиксируется
до сети; rollback не отправляется, потерянный ACK/429/остановка процесса не повторяют
попытку. После трёх перезапусков читается тот же подтверждённый журнал.

Compiled CLI прошёл dry-run/apply/replay/конфликт с атомарным отказом. Большие
source/TG IDs и signed actor ID сохраняются точно; current Telegram binding другого
аккаунта не переназначает legacy history. Daily prune сохранил один receipt с
фактическими counts 1/1/1. Повтор пакета после prune не восстановил private history.
Непустые financial/access/support/identity snapshots не изменились.

Настоящий Chromium прочитал compiled HTTP API, сменил фильтр при задержанном
старом ответе, сохранил текущий аккаунт, отобразил имена/причины обычным текстом и
не получил payload. SMTP и 3X-UI 3.7.0 в полной Docker selection — настоящие локальные
сервисы; только Telegram provider имитируется. Happ/VPN и доверие macOS не менялись.

## Наблюдаемые проверки

| Проверка | Результат |
| --- | --- |
| Task1 focused race: HTTP/CLI/import/retention/mirror/roles/boundaries | 165 тестов/подтестов, 55 верхнеуровневых, 7 пакетов; 0 ошибок/пропусков |
| Task2 rendered journal | 13 тестов; 0 ошибок/пропусков |
| Integration: account restrictions + journal | 20 тестов; 0 ошибок/пропусков |
| Whole Go race, RUN_BROWSER_TESTS=1 | 1474 теста/подтеста, 534 верхнеуровневых в 15 тестовых пакетах; 11 пакетов без тестов отдельно; 0 ошибок/пропущенных тестов |
| Current connected Go package after the web fix | Все 25 тестов/подтестов, 23 верхнеуровневых; 0 ошибок/пропусков; 230с |
| Whole rendered web | 529 тестов; 0 ошибок/пропусков; 245.840с |
| Whole Python | 112 тестов; настоящий Go HTTP consumer выполнен; 0 ошибок |
| Current full Docker native selection | 15 тестов/подтестов, 13 верхнеуровневых; 0 ошибок/пропусков; 162.655с |
| Native trial/reminder restart | Тот же operation/grant/keys, настоящий panel readback и TLS SMTP; прошли |
| Generation/vet/names/types/runtime/production and test web builds | Прошли; additive API и генераторы воспроизводимы |
| Three local images / current web image / current HTTPS smoke | Прошли; routing, deploy-time public config, secret-file access, migrations, rollback и provision-only restore |

Полный Go-прогон занял 1083.204с. Во время общих проверок выявлен общий React key
журнала и существующей restriction form: пять прежних тестов нашли дублированные
контролы. Общий mount получил отдельный audit key. Первый whole-Go manifest сохраняется
исторически: сравнение 713 файлов обнаружило только эту строку Admin.tsx.
Все остальные Go-пакеты имеют те же входные байты; целиком повторён ./tests,
потребляющий текущий web, вместе с full web/types/build/runtime/current web image/
smoke/native. Notification browser check импортирует неизменённый NoticeBody.
1474 — объединённый набор пакетов с заменой результата ./tests, без двойного счёта.

Ранние ошибки native fixture (addressable int64, повторяющийся reason locator,
relative-child locator) исправлены по реальному выводу; проверки не ослаблены и
таймаут native CLI не повышался. Первоначальный Task2 proof после key fix исторический.

Полные приватные логи, результаты и SHA-индексы сохранены вне Git в
.superpowers/acceptance/c29-audit-history. Current task3 proof проверяет 713 runtime/
test/build/API inputs, 18 успешных result/log pairs и native JSON без test skips.

## Состояние доставки

Реализация и локальная приёмка выполнены. Единственное финальное ревью, exact-source CI,
PR → v2, ручное слияние, annotated prerelease и три OCI indices/шесть platform labels
ещё не подтверждены; их состояние фиксируется отдельно в issue.

Production, live Telegram, внешний SMTP и настоящие деньги не проверялись.
С13/#18 CLOSED для реализации; внешняя денежная приёмка остаётся pending.
С37/#40 продолжает support topics/media/incoming, С46/#53 — полный импорт,
С47/#54 — разрешённый перенос и удаление Python после полного покрытия.
