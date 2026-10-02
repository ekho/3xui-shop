# С04: локальная приёмка подключения устройства

Реализация `41a2ade`; первый native browser run при checkout `0a14733`.
Итоговая приёмка после С06 **Pending**. Установленный Happ и системный VPN
исключены решением владельца; проверяется браузерный интерфейс и собственный
Docker VPN, а автоматическая навигация `happ://` перехватывается до запуска.

| AC | Требование | Доказательство | Остаток |
| --- | --- | --- | --- |
| 1 | Пять платформ, регионы iOS, ru/en,375px, клавиатура/fallback | Реальный `deploy/s04/browser.mjs`; focused С04 Playwright | Итоговая сборка после С06 |
| 2 | Copy/QR/deep link содержат только собственный key, QR локален | Native key readback, перехват clipboard в page memory, local canvas, отсутствие внешних QR requests; два реальных owner contexts с разными keys | Итоговая сборка после С06 |
| 3 | Denied states/foreign/restricted скрывают key; expired допускает явный импорт | Backend/auth fixtures и Playwright С04; native none/logout/disabled/exhausted/ban/key denial, expired own key200 и reciprocal foreign guard | Restricted UI остаётся fixture proof; итоговый regression |
| 4 | Hide/TTL/logout/pagehide/late result/session error очищают всё; fallback работает | Шесть focused С04 Playwright cases; real hide/logout | Итоговый общий regression |
| 5 | Native3.7.0/browser, неизменный target/one Grant, без запуска Happ | Расширенный native driver exit0, positive up/down, восстановленные client/target/one Grant и прежний owned VPN proxy; независимый postflight health/images/digest | Итоговая ревизия после С06 |

`npm --prefix web run test:e2e -- s03.spec.ts s04.spec.ts`:10/10.
`node deploy/s04/browser.mjs`: exit0. Внешние install links сверяются как значения
DOM; RU App Store ранее вернул404, поэтому доставка этого приложения не объявлена
проверенной и UI сохраняет явный fallback на каталог/глобальный магазин.
