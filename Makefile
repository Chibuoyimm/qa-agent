.PHONY: install db server worker sample web check integration

install:
	go mod download
	cd worker && npm ci && npx playwright install chromium
	cd web && npm ci

db:
	docker compose up -d --wait

server:
	go run ./cmd/server

worker:
	cd worker && npm start

sample:
	cd sample && npm start

web:
	cd web && npm run dev

check:
	go test ./...
	go vet ./...
	cd worker && npm run check && npm test
	cd web && npm run build

integration:
	node scripts/integration.mjs
