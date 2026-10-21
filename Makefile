GO ?= go

.PHONY: generate build test e2e integration up down demo db-show jwt-keys

generate:
	$(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.4.1 --config api/codegen.yaml api/openapi.yaml

build: generate
	$(GO) build -trimpath -o bin/user-service ./cmd/user-service
	$(GO) build -trimpath -o bin/commerce-service ./cmd/commerce-service
	$(GO) build -trimpath -o bin/gateway ./cmd/gateway

test: generate
	$(GO) test -race ./...
	$(GO) vet ./...

integration: generate
	$(GO) test -race -tags=integration -count=1 -v ./tests/...

e2e:
	docker compose --profile test run --rm --no-deps e2e

jwt-keys:
	$(GO) run ./cmd/keygen -out .env.jwt

up:
	docker compose up --build -d --wait

down:
	docker compose down

demo:
	python3 scripts/demo.py

db-show:
	docker compose exec db psql -U users -d users -c 'SELECT id,email,role FROM users;'
	docker compose exec db psql -U commerce -d commerce -c 'SELECT id,name,stock,status FROM products;' -c 'SELECT id,user_id,status,total_amount,discount_amount FROM orders;' -c 'SELECT code,current_uses,max_uses FROM promo_codes;' -c 'SELECT user_id,operation_type,created_at FROM user_operations;'
