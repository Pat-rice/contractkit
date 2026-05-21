-- name: ListPets :many
SELECT id, name, kind, age, created_at, updated_at
FROM pets
WHERE (sqlc.narg(cursor)::bigint IS NULL OR id < sqlc.narg(cursor))
ORDER BY id DESC
LIMIT sqlc.arg(limit_val);

-- name: GetPet :one
SELECT id, name, kind, age, created_at, updated_at
FROM pets
WHERE id = $1;

-- name: CreatePet :one
INSERT INTO pets (name, kind, age)
VALUES ($1, $2, $3)
RETURNING id, name, kind, age, created_at, updated_at;

-- name: UpdatePet :one
UPDATE pets
SET
    name = COALESCE(sqlc.narg(name), name),
    kind = COALESCE(sqlc.narg(kind), kind),
    age = COALESCE(sqlc.narg(age), age),
    updated_at = now()
WHERE id = $1
RETURNING id, name, kind, age, created_at, updated_at;

-- name: DeletePet :execrows
DELETE FROM pets WHERE id = $1;
