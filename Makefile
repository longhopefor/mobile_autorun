.PHONY: build test vet check-public frontend sqlc
build:
	go build -o bin/server ./cmd/server
	go build -o bin/model-connector ./cmd/model-connector
	go build -o bin/model-probe ./cmd/model-probe
	go build -o bin/media-connector ./cmd/media-connector
	go build -o bin/device-connect ./cmd/device-connect
test:
	go test -race ./...
vet:
	go vet ./...
check-public:
	python3 scripts/check-public.py
frontend:
	cd frontend && npm ci && npm run build
sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate
