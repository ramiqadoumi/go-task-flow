.PHONY: build test test-integration lint lint-format proto docker-up docker-down migrate bench clean

MODULE   = github.com/ramiqadoumi/go-task-flow
SERVICES = api-gateway dispatcher worker scheduler

VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILDTIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# don't override user values
ifeq (,$(VERSION))
  VERSION := $(shell git describe --tags --always)
  # if VERSION is empty, then populate it with branch's name and raw commit hash
  ifeq (,$(VERSION))
    VERSION := $(BRANCH)-$(COMMIT)
  endif
endif

LDFLAGS   := -X $(MODULE)/internal/version.Version=$(VERSION) \
             -X $(MODULE)/internal/version.GitCommit=$(COMMIT) \
             -X $(MODULE)/internal/version.BuildTime=$(BUILDTIME)

build:
	@for svc in $(SERVICES); do \
		echo "Building $$svc..."; \
		go build -ldflags="$(LDFLAGS)" -o bin/$$svc ./cmd/$$svc/; \
	done

test:
	go test -race ./...

test-integration:
	go test -tags=integration -race -v -timeout=120s ./tests/integration/...

lint:
	golangci-lint run ./...

lint-format:
	golangci-lint run ./... --fix

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
	       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	       proto/task/v1/task.proto

docker-up:
	docker compose up -d

docker-down:
	docker compose down

migrate:
	@psql "$(POSTGRES_DSN)" -f internal/postgres/migrations/001_create_tasks.sql
	@psql "$(POSTGRES_DSN)" -f internal/postgres/migrations/002_create_executions.sql

bench:
	go test -bench=. -benchmem -count=3 ./internal/...

clean:
	rm -rf bin/ coverage.out
