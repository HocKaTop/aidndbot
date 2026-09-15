.PHONY: up down test generate frontend tunnel tunnel-down tunnel-logs
up:
	docker compose up -d --build

down:
	docker compose down

tunnel:
	python3 scripts/start_tunnel.py

tunnel-down:
	docker compose -f docker-compose.yml -f compose.tunnel.yml stop tunnel

tunnel-logs:
	docker compose -f docker-compose.yml -f compose.tunnel.yml logs -f tunnel

test:
	cd backend && go test ./... && go vet ./...
	cd frontend && npm run build
	docker compose -f docker-compose.yml -f compose.test.yml run --build --rm tests

generate:
	cd backend && sqlc generate

frontend:
	cd frontend && npm run dev
