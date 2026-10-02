# С02: rollout и восстановление учётных записей

С02 добавляет восстановление/смену пароля, смену email с подтверждением обоих
адресов, завершение других сеансов и выход. Account/VPN/Grant/Operation и лимиты
не меняются. Это инструкция для будущего тестового запуска; production и
переключение legacy-обработчиков бота в этот этап не входят.

## Окно обслуживания и rollout

1. Закрыть ingress кабинета, остановить bot writers, backend и mail workers.
   Xray продолжает обслуживать существующих клиентов. Сохранить согласованный
   PostgreSQL dump, версию приложения и файлы `MAIL_KEY`, `CODE_KEY`, остальных
   исходных секретов. Не печатать URL подключения или содержимое ключей.
2. Запустить additive `migrate`, затем повторный `migrate`; проверить schema6.
   Обычное обновление сохраняет сессии и mail/job IDs. Maintenance SQL ниже
   требуется **после восстановления БД**, а не при обычном rollout.
3. Запустить совместимые backend/web сборки при закрытом ingress. Проверить
   внутренний health, login, reset, почту и API/schema. Открыть ingress после
   успешного smoke. Telegram worker для С02 не требуется; legacy handlers
   остаются у своего владельца.

В поставляемом Compose `CABINET_MAINTENANCE=true` возвращает503 на всех путях
публичного кабинета, включая формы и health. Пересоздать gateway с новым
значением; `--no-deps` не запускает backend. Private adapter и native panel
routes сами не закрываются этим флагом: bot и остальные writers должны быть
остановлены отдельно. Во внешнем deployment допустимо закрыть ingress его
собственным reverse proxy. Ошибка проверки оставляет ingress закрытым.

## Restore: обязательная очистка до reconcile и serve

Старая БД, backend и workers больше не должны писать в ту же панель.
Восстановить dump в новую пустую БД и сохранить исходные шифровальные ключи.
Переключить secret file подключения, выполнить `migrate`, затем при всё ещё
закрытом ingress и остановленных writers/mail workers выполнить
[post_restore_auth.sql](../../backend/db/maintenance/post_restore_auth.sql).

Пример для оператора с заранее настроенными service/password файлами mode0600:

```sh
PGSERVICEFILE=/secure/cabinet/pg_service.conf PGPASSFILE=/secure/cabinet/pgpass \
  psql 'service=cabinet_restore' -X --set=ON_ERROR_STOP=1 \
  --file=backend/db/maintenance/post_restore_auth.sql
```

Service должен указывать именно восстановленную БД. Секреты и DSN не передаются
в аргументах. SQL работает одной транзакцией и возвращает только три счётчика:
удалённые сессии, отозванные credential proofs, очищенные proof payloads.
Повторное выполнение безопасно и возвращает нули. Если SQL не выполнен или
завершился ошибкой, **не запускать reconcile/serve и не открывать ingress**.

Отзываются все восстановленные browser sessions и незавершённые reset/email
proofs, включая первое подтверждение email и dummy-запрос неизвестного адреса.
Зашифрованные credential-письма очищаются; job IDs остаются. Registration
письма, security notices, accounts, password hashes/credential version,
Grant/Operation, UUID/subId/panel ownership и VPN-лимиты сохраняются.

Затем запустить только существующий provision-only `reconcile`, сверить
неоднозначные операции по [инструкции С01](s01-test-rollout.md), остановить
`reconcile`, запустить backend, проверить внутренний health и открыть ingress.
Владелец входит заново с credentials **на момент backup**, получает новую cookie
и прежний VPN-доступ. Старые cookies и credential proofs должны отказать.
Backup не содержит смены пароля/email, сделанной после его времени; обещать
сохранение более нового пароля нельзя. Если это неприемлемо для конкретного
инцидента, ingress остаётся закрытым до решения оператора.

## Проверка на собственном стенде

```sh
go -C backend test ./internal/platform -run '^TestAccountSecurityRestore$' -count=1
python3 deploy/acceptance/local.py restore
```

Go проверяет maintenance дважды на изолированной БД с sessions, reset,
first-confirmed email pair, registration/notice mail и jobs. Docker-проверка
получает настоящие TLS Mailpit-письма, закрывает ingress, делает pg_dump/restore
с running River job/reserved Grant и внешним клиентом 3X-UI3.7.0, применяет тот
же SQL до reconcile, проверяет отказ старой cookie/proofs и новый owner login.
Сверка target, единственный client/Grant и VPN выполняются только в собственном
Docker. Credentials и proofs остаются в памяти, dump — private mode0600.
Установленный Happ и системные настройки VPN/доверия не используются.

## Откат и границы приёмки

При совместимом откате сборки сохранять additive schema и исходные keys; не
делать destructive Goose Down. При несовместимом откате держать ingress
закрытым, восстановить согласованные DB/build, выполнить migrate и maintenance
SQL, затем reconcile/smoke и новый login. Не запускать одновременно двух
владельцев выдачи и не создавать новую operation вместо сверки прежней.

Локальная проверка не доказывает внешнюю доставляемость SMTP или стоимость
Argon2 на целевом сервере. Эти ресурсы предоставляет пользователь до external
acceptance. Приёмка С01 остаётся OPEN; tunnel check установленного Happ запрещён.
Локальные результаты и точные ревизии — в [матрице С02](../evidence/s02-acceptance.md).
