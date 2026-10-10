.PHONY: build test vet
build:
	go build -ldflags "-X main.version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o neighbeats ./cmd/neighbeats
test:
	go test -race -count=1 ./...
vet:
	go vet ./...
