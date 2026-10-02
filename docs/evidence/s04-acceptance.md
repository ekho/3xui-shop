# С04: локальная приёмка подключения устройства

Реализация `41a2ade`; первый native browser run при checkout `0a14733`.
Все5 AC локально приняты; свежий whole-branch review и общий fix pass
завершены. Core source Connection/панели/подписки не менялся после native
snapshot; текущий web64 GREEN. Ревизии и команды — в [сводном evidence](s03-s06-progress.md#итоговое-закрытие-локальной-приёмки).
Установленный Happ и системный VPN
исключены решением владельца; проверяется браузерный интерфейс и собственный
Docker VPN, а автоматическая навигация `happ://` перехватывается до запуска.

| AC | Требование | Доказательство | Статус / граница доказательства |
| --- | --- | --- | --- |
| 1 | Пять платформ, регионы iOS, ru/en,375px, клавиатура/fallback | Реальный `deploy/s04/browser.mjs`; focused С04 Playwright | PASS; ссылки DOM, внешняя доставка install links не заявлена |
| 2 | Copy/QR/deep link содержат только собственный key, QR локален | Native key readback, перехват clipboard в page memory, local canvas, отсутствие внешних QR requests; два реальных owner contexts с разными keys | PASS, native snapshot `0a14733` |
| 3 | Denied states/foreign/restricted скрывают key; expired допускает явный импорт | Backend/auth fixtures и Playwright С04; native none/logout/disabled/exhausted/ban/key denial, expired own key200 и reciprocal foreign guard | PASS; restricted UI — fixture |
| 4 | Hide/TTL/logout/pagehide/late result/session error очищают всё; fallback работает | Шесть focused С04 Playwright cases; real hide/logout | PASS; browser64 regression `df34ef5` |
| 5 | Native3.7.0/browser, неизменный target/one Grant, без запуска Happ | Расширенный native driver exit0, positive up/down, восстановленные client/target/one Grant и прежний owned VPN proxy; независимый postflight health/images/digest | PASS; core source неизменён до `df34ef5` |

`npm --prefix web run test:e2e -- s03.spec.ts s04.spec.ts`:10/10.
`node deploy/s04/browser.mjs`: exit0. Внешние install links сверяются как значения
DOM; RU App Store ранее вернул404, поэтому доставка этого приложения не объявлена
проверенной и UI сохраняет явный fallback на каталог/глобальный магазин.
