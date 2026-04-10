VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/elliottregan/cspace/internal/cli.Version=$(VERSION)"
GOBIN   := ./bin/cspace-go

.PHONY: build build-linux clean test vet sync-embedded

# Sync lib/ contents into internal/assets/embedded/ for go:embed
sync-embedded:
	@rm -rf internal/assets/embedded
	@mkdir -p internal/assets/embedded
	@cp -r lib/templates internal/assets/embedded/
	@cp -r lib/scripts internal/assets/embedded/
	@cp -r lib/hooks internal/assets/embedded/
	@cp -r lib/agents internal/assets/embedded/
	@cp -r lib/agent-supervisor internal/assets/embedded/
	@rm -rf internal/assets/embedded/agent-supervisor/node_modules
	@cp -r lib/skills internal/assets/embedded/
	@cp -r lib/core internal/assets/embedded/
	@cp lib/defaults.json internal/assets/embedded/

build: sync-embedded
	go build $(LDFLAGS) -o $(GOBIN) ./cmd/cspace

build-linux: sync-embedded
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(GOBIN)-linux-amd64 ./cmd/cspace
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(GOBIN)-linux-arm64 ./cmd/cspace

test: sync-embedded
	go test ./...

vet: sync-embedded
	go vet ./...

clean:
	rm -f $(GOBIN) $(GOBIN)-linux-amd64 $(GOBIN)-linux-arm64
	rm -rf internal/assets/embedded
