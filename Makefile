.PHONY: generate check test build compose-up compose-smoke

generate:
	go generate .

check:
	go run ./tools/contractgen -check
	go vet ./...
	cd platform && go vet ./...

test:
	go test -count=1 ./...
	cd platform && go test -count=1 ./...

build:
	cd apps/web && npm ci && npm run build
	cd platform && go build ./cmd/...

compose-up:
	docker compose up --build -d --wait

compose-smoke:
	sh scripts/compose-smoke.sh
