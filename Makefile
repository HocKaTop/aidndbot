.PHONY: up down test generate frontend tunnel tunnel-down tunnel-logs
COMPOSE = docker compose -f docker-compose.yml
ifeq ($(shell uname -s),Darwin)
COMPOSE += -f compose.mac.yml
endif

up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down

tunnel:
	python3 scripts/start_tunnel.py

tunnel-down:
	$(COMPOSE) -f compose.tunnel.yml stop tunnel

tunnel-logs:
	$(COMPOSE) -f compose.tunnel.yml logs -f tunnel

test:
	cd backend && go test ./... && go vet ./...
	cd frontend && npm run build
	$(COMPOSE) -f compose.test.yml run --build --rm tests

generate:
	cd backend && sqlc generate

frontend:
	cd frontend && npm run dev
