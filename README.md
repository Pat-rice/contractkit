# WIP 
# contractkit — Spec-First, Multi-Version Go API Template

A reference template for building Go HTTP APIs where the **API contract is the source of truth** and **multiple API versions are first-class citizens**.

The design philosophy: write the API once in [TypeSpec](https://typespec.io), version it explicitly with the `@versioned` decorator, and let codegen produce per-version OpenAPI specs and per-version Go server stubs. Handler code, database access, and tests follow the same pattern — generated where possible, hand-written where the business logic lives.

The example domain is a Pet Store with two versions (`1.0` and `2.0`), where `2.0` adds a `tags` field to `Pet` and a new `GET /pets/{petId}/tags` endpoint. The infrastructure to add a `3.0` is already in place — only the TypeSpec changes.

## Why spec-first

- **Single source of truth.** The API contract lives in one place (`api/typespec/`). OpenAPI YAML, Go interfaces, request/response types, validation, and docs are all derived from it.
- **No drift between spec and implementation.** The Go server implements a generated `StrictServerInterface`; adding a new endpoint to TypeSpec causes a compile error until the handler is implemented.
- **Versioning without forking.** `@added(Versions.v2)` and `@removed` decorators let you describe the *delta* between versions in one TypeSpec file. The compiler emits one OpenAPI document per version.
- **Contract-level fuzzing.** Because the spec is authoritative, [schemathesis](https://schemathesis.readthedocs.io) can generate property-based tests directly from it.

## Stack

| Layer | Tool |
|---|---|
| API contract | [TypeSpec](https://typespec.io) with `@typespec/versioning` |
| OpenAPI emission | `@typespec/openapi3` |
| Go server codegen | [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) (strict server, `net/http`) |
| Database codegen | [sqlc](https://sqlc.dev) (pgx/v5 driver) |
| Database | PostgreSQL 16 |
| Migrations | [golang-migrate](https://github.com/golang-migrate/migrate) CLI (run as a one-off Docker step or K8s Job) |
| Request validation | [go-playground/validator/v10](https://github.com/go-playground/validator), driven by TypeSpec `x-oapi-codegen-extra-tags` extensions |
| Error responses | [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) Problem Details (`application/problem+json`) with a project-owned `errors[]` extension for per-field validation |
| Integration tests | [dockertest](https://github.com/ory/dockertest) (ephemeral Postgres per run) |
| Contract fuzzing | [schemathesis](https://schemathesis.readthedocs.io) |
| Lint | [golangci-lint](https://golangci-lint.run) |

## Architecture

```
                ┌──────────────────────────┐
                │  api/typespec/*.tsp      │  Single source of truth
                │  @versioned(Versions)    │  Versions = { v1: "1.0", v2: "2.0" }
                └──────────────┬───────────┘
                               │  tsp compile
                               ▼
        ┌──────────────────────────────────────────────┐
        │  api/openapi-spec/1.0/openapi.yaml           │
        │  api/openapi-spec/2.0/openapi.yaml           │  One spec per version
        └──────────────┬─────────────────┬─────────────┘
                       │                 │
              oapi-codegen          schemathesis
              (API_VERSION)          (per-version fuzz)
                       │
                       ▼
        ┌──────────────────────────────────────────────┐
        │  internal/api/gen.go                         │  Strict server interface,
        │  - StrictServerInterface                     │  request/response types,
        │  - request/response models                   │  embedded OpenAPI spec
        └──────────────┬───────────────────────────────┘
                       │  implements
                       ▼
        ┌──────────────────────────────────────────────┐
        │  internal/server/server.go                   │  Hand-written handlers
        │    └─ uses internal/db (sqlc-generated)      │
        └──────────────────────────────────────────────┘

        db/queries/*.sql    ──sqlc──────────────▶ internal/db/*.sql.go
        db/migrations/*.sql ──migrate/migrate──▶ Postgres (just migrate-up / K8s Job)
                          └──embed.FS─────────▶ integration tests (testutil)
```

### Multi-version mechanics

Versions are declared once in `api/typespec/main.tsp`:

```typespec
@versioned(Versions)
namespace PetStore;

enum Versions {
  v1: "1.0",
  v2: "2.0",
}
```

Changes are then annotated on individual fields, operations, or models:

```typespec
model Pet {
  id: int64;
  name: string;
  // ...
  @added(Versions.v2)
  tags: string[];
}

@added(Versions.v2)
@route("{petId}/tags")
@get
op listPetTags(@path petId: int64): /* ... */;
```

The TypeSpec compiler emits one OpenAPI document per declared version into `api/openapi-spec/<version>/openapi.yaml`. A positional argument on the relevant `just` recipes controls which spec is fed to `oapi-codegen` and to `schemathesis`:

```bash
just generate-api 2.0    # generate Go server bound to v2.0
just fuzz         1.0    # fuzz the v1.0 contract
```

Adding a new version is a three-step process:

1. Add a value to the `Versions` enum in `main.tsp`.
2. Annotate the additions/removals/renames with `@added` / `@removed` / `@renamedFrom`.
3. Run `just generate` — a new `api/openapi-spec/<version>/` directory appears.

### Validation

Field-level constraints declared in TypeSpec are surfaced two ways:

1. **In the OpenAPI document**, where [schemathesis](https://schemathesis.readthedocs.io) enforces them during contract fuzzing.
2. **At runtime in Go**, by attaching equivalent [go-playground/validator](https://github.com/go-playground/validator) struct tags via the `x-oapi-codegen-extra-tags` extension. oapi-codegen copies the tags onto the generated request structs; a strict-server middleware in `cmd/api/main.go` runs `validator.StructCtx` on each decoded request and produces an RFC 9457 problem response listing every failing field.

```typespec
@minLength(1)
@maxLength(255)
@extension("x-oapi-codegen-extra-tags", #{ validate: "required,min=1,max=255" })
name: string;
```

### Error responses

Handler-emitted 4xx/5xx responses follow [RFC 9457 — Problem Details for HTTP APIs](https://www.rfc-editor.org/rfc/rfc9457) and are served as `application/problem+json`. The `Problem` model (defined in `api/typespec/models.tsp`) carries the five canonical fields plus a project-owned `errors[]` extension used for per-field validation failures. Each entry locates the failure with a `name` + `in` discriminator (`body`, `query`, `path`, or `header`) instead of a JSON Pointer, so query and path failures don't need to fake a body location:

```jsonc
HTTP/1.1 400 Bad Request
Content-Type: application/problem+json

{
  "type":     "urn:problem-type:contractkit:validationFailed",
  "title":    "Bad Request",
  "status":   400,
  "detail":   "request validation failed",
  "instance": "urn:uuid:164c2b32-…",
  "errors": [
    { "name": "name",    "in": "body",  "rule": "required", "detail": "is required" },
    { "name": "tags[0]", "in": "body",  "rule": "min",      "detail": "must be at least 1" },
    { "name": "limit",   "in": "query", "rule": "gte",      "detail": "must be >= 1" }
  ]
}
```

Type URIs live under `urn:problem-type:contractkit:` — currently `badRequest`, `validationFailed`, `resourceNotFound`, `internal` (exported as constants from `internal/problem`). The `instance` URN is logged server-side on every problem response so a client-reported `urn:uuid:…` can be grepped from the server logs. `errors[]` is capped at 20 entries (with a trailing truncation marker) to bound the response size against pathological payloads.

Two known exceptions to the contract: Go's stdlib `http.ServeMux` writes plain-text 405 responses for method mismatches on registered routes, and any handler that sets a non-JSON `Content-Type` for a 4xx/5xx response is not rewritten. Everything else routes through `problem.Write` or the `application/json` → `application/problem+json` safety-net middleware in `internal/problem`.

## Project layout

```
api/typespec/             TypeSpec source (the contract)
  main.tsp                  @service, @versioned, Versions enum
  models.tsp                Pet, NewPet, UpdatePet, PetPage, Problem
  routes.tsp                Pets and Health namespaces
  tspconfig.yaml            emits to ../openapi-spec/<version>/

api/openapi-spec/         Generated OpenAPI documents (one folder per version)
  1.0/openapi.yaml
  2.0/openapi.yaml

cmd/api/                  Application entrypoint (HTTP server, signal handling, validator middleware, recover middleware, panic-to-problem)
internal/api/             Generated Go server interface and types (oapi-codegen)
internal/db/              Generated type-safe queries (sqlc)
internal/server/          Hand-written handlers implementing StrictServerInterface
internal/config/          Env-based configuration
internal/problem/         RFC 9457 Problem builder + validator → ProblemError conversion + content-type safety-net middleware
internal/testutil/        dockertest helpers for integration tests

db/migrations/            Up/down SQL migrations (applied via the migrate/migrate Docker image)
db/queries/               SQL queries consumed by sqlc
db/seeds/                 Optional dev seed data
db/embed.go               //go:embed of migrations (used by integration tests) and seeds

justfile                  All generation, build, test, lint, fuzz, migrate recipes
docker-compose.yaml       Local Postgres + API container
Dockerfile                Multi-stage build for the API binary
oapi-codegen.yaml         oapi-codegen configuration
sqlc.yaml                 sqlc configuration
tools.go                  Tool dependencies pinned in go.mod
```

## Quick start

```bash
# 1. Start Postgres
docker compose up postgres -d

# 2. Configure (justfile auto-loads .env via `set dotenv-load`)
cp .env.example .env

# 3. Generate everything (TypeSpec → OpenAPI → Go server, SQL → Go db)
just generate

# 4. Apply migrations (runs the migrate/migrate Docker image; same image you'd
#    schedule as a K8s Job — the API binary does not run migrations itself)
just migrate-up

# 5. Build and run
just run
```

Or run the whole stack in containers:

```bash
docker compose up --build
```

The server listens on `http://localhost:8080` by default.

## Recipes

Task runner is [just](https://github.com/casey/just). `.env` is auto-loaded.

| Recipe | Description |
|---|---|
| `just` | List all recipes |
| `just generate` | Full pipeline: TypeSpec → OpenAPI → Go server, SQL → Go db |
| `just generate-typespec` | Compile TypeSpec; emits one OpenAPI doc per declared version |
| `just generate-api [version]` | oapi-codegen against `api/openapi-spec/<version>/openapi.yaml` (default `1.0`) |
| `just generate-db` | sqlc generate |
| `just verify-generate` | Regenerate and fail if any committed artifact under `api/openapi-spec/`, `internal/api/`, or `internal/db/` would change — wire into CI to catch unstaged spec drift |
| `just build` | Build `bin/api` |
| `just run` | Build and run locally (needs `DATABASE_URL`) |
| `just test` | Unit + integration tests |
| `just test-unit` | Unit tests only (`go test -short ./...`) |
| `just test-integration` | Integration tests (dockertest spins up Postgres) |
| `just lint` | golangci-lint in Docker |
| `just fuzz [version]` | Run schemathesis against a running server (default `1.0`) |
| `just seed` | Apply `db/seeds/pets.sql` to the running Compose Postgres |
| `just migrate-up` / `migrate-down` | Apply / roll back migrations via the `migrate/migrate` CLI image |
| `just docker-up` / `docker-down` | Start/stop the full Compose stack |
| `just clean` | Remove `bin/`, `node_modules/`, and the generated `api/openapi-spec/` |

All recipes run their external tools in Docker — no host toolchain beyond `docker`, `go`, and `just` is required for the dev loop.

The version argument on `generate-api` and `fuzz` selects which generated OpenAPI document is consumed.

## Configuration

| Env var | Default | Description |
|---|---|---|
| `DATABASE_URL` | *(required)* | Postgres connection string (`postgres://…`) |
| `PORT` | `8080` | HTTP listen port |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

## Workflow for adding an endpoint or field

1. **Contract.** Edit `api/typespec/{routes,models}.tsp`. Annotate with `@added(Versions.vX)` if the change should only apply to a new version. For request-side validation, attach an `@extension("x-oapi-codegen-extra-tags", #{ validate: "..." })` alongside the TypeSpec constraint so the rule fires at runtime as well as in the spec.
2. **Schema.** If new state needs to be persisted, add a migration in `db/migrations/` and a query in `db/queries/`.
3. **Generate.** `just generate` regenerates OpenAPI, Go server types, and Go db code.
4. **Implement.** Add the handler method to `internal/server/server.go` — the project will not compile until you do (the `StrictServerInterface` enforces it).
5. **Verify.** `just test` for behavior, `just fuzz` for contract conformance, `just lint` for style.

## API endpoints

Endpoints available in **v1.0** and **v2.0**:

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check (pings the DB pool) |
| `GET` | `/pets?limit=&cursor=` | List pets, cursor-paginated |
| `POST` | `/pets` | Create a pet |
| `GET` | `/pets/{petId}` | Get a pet by id |
| `PUT` | `/pets/{petId}` | Patch-style partial update |
| `DELETE` | `/pets/{petId}` | Delete a pet |

Added in **v2.0**:

| Method | Path | Description |
|---|---|---|
| `GET` | `/pets/{petId}/tags` | List tags for a pet |

The `Pet`, `NewPet`, and `UpdatePet` models also gain a `tags: string[]` field in v2.0.

## Testing

- **Unit tests** live alongside the code (`*_test.go`).
- **Integration tests** spin up an ephemeral Postgres via [dockertest](https://github.com/ory/dockertest), apply migrations, and run against a real database. See `internal/testutil/testdb.go`.
- **Contract fuzzing** uses schemathesis. It reads the OpenAPI document and generates randomised requests, asserting that responses conform to the declared schema and that no 500s are returned. The recipe takes a positional version argument that defaults to `1.0`:

  ```bash
  just fuzz          # fuzz v1.0 (default)
  just fuzz 2.0      # fuzz v2.0
  ```

  The recipe depends on `generate-typespec`, so the spec is always regenerated before the run.

## Prerequisites

- Docker (everything else runs in containers)
- [just](https://github.com/casey/just) (`brew install just`)
- Go 1.26+ (only needed for `just build` / `just run` outside Docker)

## License

MIT
