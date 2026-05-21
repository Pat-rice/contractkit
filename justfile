# Auto-load DATABASE_URL etc. from .env
set dotenv-load

# Docker image pins (override via env)
golang_image       := env_var_or_default("GOLANG_IMAGE", "golang:1.26-alpine")
node_image         := env_var_or_default("NODE_IMAGE", "node:22-alpine")
sqlc_image         := env_var_or_default("SQLC_IMAGE", "sqlc/sqlc:1.30.0")
lint_image         := env_var_or_default("LINT_IMAGE", "golangci/golangci-lint:v2.11.4-alpine")
schemathesis_image := env_var_or_default("SCHEMATHESIS_IMAGE", "schemathesis/schemathesis:latest")
migrate_image      := env_var_or_default("MIGRATE_IMAGE", "migrate/migrate:v4.19.1")

# Common docker run wrappers
docker_run    := "docker run --rm -v " + justfile_directory() + ":/work -w /work"
docker_run_go := docker_run + " -v aiplayground-gomod:/go/pkg/mod " + golang_image

# Show available recipes
default:
    @just --list

# Full code generation pipeline (TypeSpec + oapi-codegen + sqlc)
generate: generate-typespec generate-api generate-db

# Re-run code generation and fail if any tracked artifact changes. Wire this
# into CI to catch a model.tsp / queries.sql edit that wasn't committed with
# its regenerated output.
verify-generate: generate
    @git diff --exit-code -- api/openapi-spec internal/api internal/db || \
        (echo "Generated artifacts out of date — run 'just generate' and commit the result" && exit 1)

# TypeSpec -> OpenAPI YAML (emits all versions under api/openapi-spec/<version>/)
generate-typespec:
    docker run --rm -v {{justfile_directory()}}:/work -w /work/api/typespec {{node_image}} sh -c "npm install && npx tsp compile ."

# OpenAPI YAML -> Go server code. Pass version to select a spec, e.g. `just generate-api 2.0`
generate-api version="1.0":
    {{docker_run_go}} go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
        -config oapi-codegen.yaml api/openapi-spec/{{version}}/openapi.yaml

# SQL -> Go database code
generate-db:
    {{docker_run}} {{sqlc_image}} generate

# Build the API binary
build:
    go build -o bin/api ./cmd/api

# Build and run the API locally (requires DATABASE_URL)
run: build
    ./bin/api

# Run unit tests only (no Docker required)
test-unit:
    go test -short ./...

# Run integration tests (spins up Postgres via dockertest)
test-integration:
    go test -run Integration ./...

# Run unit + integration tests
test: test-unit test-integration

# Lint with golangci-lint
lint:
    {{docker_run}} {{lint_image}} golangci-lint run ./...

# Fuzz a running server with schemathesis. Regenerates the spec first; pass version to choose the contract.
fuzz version="1.0": generate-typespec
    docker run --rm --network host \
        -v {{justfile_directory()}}/api/openapi-spec:/spec \
        {{schemathesis_image}} run --url http://localhost:8080 /spec/{{version}}/openapi.yaml

# docker run --rm schemathesis/schemathesis run https://example.schemathesis.io/openapi.json

# Start all services with Docker Compose
docker-up:
    docker compose up -d --build

# Stop all services
docker-down:
    docker compose down

# Seed the local database (requires docker-up)
seed:
    docker compose exec -T postgres psql -U postgres -d petstore < db/seeds/pets.sql

# Apply all pending migrations. Same image is intended for K8s Job rollouts.
migrate-up:
    docker run --rm --network host \
        -v {{justfile_directory()}}/db/migrations:/migrations \
        {{migrate_image}} -path /migrations -database "$DATABASE_URL" up

# Roll back the most recent migration
migrate-down:
    docker run --rm --network host \
        -v {{justfile_directory()}}/db/migrations:/migrations \
        {{migrate_image}} -path /migrations -database "$DATABASE_URL" down 1

# Clean build artifacts
clean:
    rm -rf bin/ api/typespec/node_modules/ api/openapi-spec/
