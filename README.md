# Marketplace — ДЗ №2

**User** отвечает за учётные записи и JWT, **Commerce = Catalog + Order** — за товары, заказы и промокоды. Тонкий Go gateway даёт один адрес API. Один PostgreSQL-контейнер содержит две отдельные БД с разными пользователями и паролями; доступ к чужой БД запрещён.

Стек: Go 1.27.3, PostgreSQL 17.11, Flyway 11.8.2, OpenAPI 3.0.3. Feed, Payment и Notification остаются проектными компонентами [ДЗ №1](docs/homework-1.md): их реализаций в исходном репозитории не было.

## Запуск

Нужны Docker и Compose v2+, для демонстрации — Python 3. На ARM Flyway запускается через эмуляцию amd64.

```sh
cp .env.example .env                 # только при первом запуске
docker compose up --build -d --wait
curl http://localhost:8080/health
make demo                            # товар → заказ → отмена, stock 10 → 8 → 10
make db-show                         # SELECT из обеих БД
```

Доступен только localhost:8080; БД и сервисы не публикуют порты. Gateway направляет /auth/* в User, остальное — в Commerce. JWT, OpenAPI-валидация и JSON-логи запросов с маскированием паролей выполняются в сервисах. Gateway создаёт X-Request-Id, пересылает запрос и возвращает JSON 503 при недоступном сервисе.

В .env.example публичные учебные credentials. Для общего развёртывания замените пароли и ключи. `make jwt-keys` создаёт .env.jwt без перезаписи; запуск: `docker compose --env-file .env --env-file .env.jwt up --build -d --wait`. Приватный ключ получает только User, открытый — только Commerce; gateway ключи не нужны. Пароли БД из .env применяются при создании пустого тома; для существующей БД пароль меняется через ALTER ROLE.

ADMIN создаётся через BOOTSTRAP_ADMIN_EMAIL/PASSWORD; существующий аккаунт не перезаписывается и не повышается в роли. Публичная регистрация разрешает USER и SELLER.

## Проверки

```sh
make generate                        # DTO, интерфейсы, маршруты из OpenAPI
make test                            # unit + race detector + vet, нужен Go
make e2e                             # реальные контейнеры, HTTP и SQL-проверки
python3 scripts/check-service-isolation.py  # Commerce работает при остановленном User
docker compose logs -f user-service commerce-service
docker compose down                  # остановка без удаления данных
```

E2E использует только запущенный Compose-стенд и добавляет тестовые записи в локальные БД. Проверку отказа запускайте отдельно, после E2E. [Отчёт о проверках](docs/verification.md).

## API и решения

Контракт — [api/openapi.yaml](api/openapi.yaml). `make generate` запускает pinned oapi-codegen v2.4.1; ручных транспортных DTO нет. Generated-файл игнорируется и создаётся при сборке. Схемы таблиц применяет Flyway отдельно к [User DB](db/users/migration/V1__users.sql) и [Commerce DB](db/commerce/migration/V1__commerce.sql). db/init.sh создаёт только БД и роли.

Auth: POST /auth/register, /auth/login, /auth/refresh. Access JWT EdDSA — 15 минут, refresh — 14 дней с одноразовой ротацией. Авторизация: `Authorization: Bearer <token>`.

Products: POST/GET /products, GET/PUT/DELETE /products/{id}; page=0&size=20, точные фильтры status/category, DELETE → ARCHIVED. Владелец нового товара берётся из JWT; seller_id в запросе не принимается. SELLER меняет свои товары, ADMIN — любые.

Orders: POST /orders, GET/PUT /orders/{id}, POST /orders/{id}/cancel. Promo: POST /promo-codes. Для проверки lifecycle ADMIN использует POST /orders/{id}/status.

Заказ, позиции, остатки, промокод и журнал операций меняются в одной транзакции Commerce. Деньги — decimal/NUMERIC(12,2). [Архитектурное решение и спорные требования](docs/adr-003-commerce-boundary.md), [использованные навыки](docs/skills.md).
