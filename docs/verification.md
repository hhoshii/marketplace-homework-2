# Проверки упрощённой версии

Проверено 20.10.2026. User и Commerce остаются отдельными сервисами; один PostgreSQL содержит две изолированные БД. [Архитектурное решение](adr-003-commerce-boundary.md).

## Фактические результаты

- `make test`: PASS, 9 групп unit-тестов, race detector и go vet. В том числе gateway только пересылает запросы и сохраняет формат ошибки 503.
- `make e2e`: PASS дважды, 9 групп, 39.650 и 39.456 с. Финальный запуск с --no-deps не перезапускает миграции. HTTP проходит реальные Gateway/User/Commerce; ответы проверяются по OpenAPI, инварианты — SQL.
- Docker build/up: PASS, все три приложения и PostgreSQL healthy; оба Flyway завершились с кодом 0.
- Чистый запуск: PASS на отдельном пустом томе. init создал БД/роли, затем Flyway применил users V1 и commerce V1 с нуля.
- Изоляция: PASS. Роли users/commerce — не superuser/createdb/createrole; подключение к чужой БД возвращает SQLSTATE 42501. Чужие HTTP-маршруты недоступны.
- Отказ User: PASS. Commerce читает товары, создаёт/отменяет заказ по выданному JWT; auth возвращает JSON 503. Скрипт восстанавливает User и проверяет его готовность.
- `make demo`: PASS после восстановления User; сумма 246.90, stock 10 → 8 → 10. Первый запуск во время тестового отключения User ожидаемо получил 503.
- Повторная кодогенерация: PASS, SHA-256 generated-файла совпал побайтно.
- govulncheck: 0 уязвимостей в вызываемом коде и импортируемых пакетах; 1 advisory в неиспользуемой части зависимого модуля.
- SQL: negative_stock=0, invalid_promo_uses=0, duplicate_active_users=0.
- Runtime-конфигурация: приватный JWT-ключ только у User, открытый — у Commerce. У gateway нет JWT-ключей/DSN; у Commerce нет зависимости от User API.
- JSON-логи: 510 API-записей, пропущенных обязательных полей и неверных UUID нет; все 51 значения паролей/токенов замаскированы.

E2E-клиент запускается с race detector; серверы в Compose — обычные собранные бинарники. Девять групп: CRUD/RBAC; транзакции/snapshots; rollback/promo; lifecycle; auth/refresh; validation; concurrent stock/promo/active/refresh; concurrent update/cancel; service boundaries.

## Критерии 1–10

| № | Где реализовано |
| --- | --- |
| 1 | Product CRUD, пагинация, точные фильтры и soft delete — commerce/products.go |
| 2 | ProductCreate/Update/Response, ограничения и типы — api/openapi.yaml |
| 3 | make generate, oapi-codegen v2.4.1, generated DTO/интерфейсы/маршруты; ручных DTO нет |
| 4 | PostgreSQL + два Flyway-набора, индексы и автоматические timestamps |
| 5 | Generated Error, обязательные коды и объект details; дополнительно DEPENDENCY_UNAVAILABLE |
| 6 | OpenAPI middleware до обработчика, details.violations, лимит тела запроса |
| 7 | Локальная pgx.Tx, checkout_locks, блокировки товаров/промокода, snapshots и rollback |
| 8 | JSON-логи запросов в User/Commerce, UUID, status/duration/user_id/timestamp, маскирование паролей/токенов |
| 9 | Access 15 минут, refresh 14 дней, EdDSA, bcrypt, hash и одноразовая ротация refresh |
| 10 | USER/SELLER/ADMIN, владение товаром/заказом; seller_id нового товара берётся из JWT, ADMIN меняет любые товары |

## Сохранение данных и ограничения

До новых тестовых записей сверены количество и MD5 всех десяти таблиц: две истории Flyway, users (70), refresh_tokens (39), products (23), promo_codes (8), orders (20), order_items (20), user_operations (28), checkout_locks (10). Всё совпало. UUID, парольные хеши и refresh-записи сохранены; JWT-ключи и схемы таблиц не менялись.

Старые остановленные контейнеры БД удалены без удаления именованных томов users-data, commerce-data и postgres-data. Резервные дампы находятся в закрытом каталоге /private/tmp/marketplace-simplify.eo3LfL; в репозиторий и архив они не входят. Предыдущая версия исходников сохранена рядом в marketplace-homework-2-before-simplification.zip.

Один PostgreSQL — общий домен отказа для обеих БД; это учебное упрощение. Генерация и сборка требуют доступа к Go proxy/registry: полностью офлайн-сценарий не поддерживается. Публичные credentials из .env.example предназначены только для localhost.

GitHub Actions подготовлены, но удалённо не запускались. OS/container vulnerability scan и нагрузочные испытания не выполнялись. Публикации, коммитов и push не было.
