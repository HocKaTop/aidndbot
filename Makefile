.PHONY: up down test generate frontend
up:
	docker compose up -d --build

down:
	docker compose down

test:
	cd backend && go test ./... && go vet ./...
	cd frontend && npm run build
	docker compose -f docker-compose.yml -f compose.test.yml run --build --rm tests

generate:
	cd backend && sqlc generate

frontend:
	cd frontend && npm run dev
