package testutil

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"

	dbpkg "github.com/patrice/contractkit/db"
)

type TestDB struct {
	Pool     *pgxpool.Pool
	pool     *dockertest.Pool
	resource *dockertest.Resource
}

func SetupTestDB() (*TestDB, error) {
	pool, err := dockertest.NewPool("")
	if err != nil {
		return nil, fmt.Errorf("could not construct dockertest pool: %w", err)
	}

	if err := pool.Client.Ping(); err != nil {
		return nil, fmt.Errorf("could not connect to Docker: %w", err)
	}

	pool.MaxWait = 60 * time.Second

	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "16-alpine",
		Env: []string{
			"POSTGRES_DB=petstore_test",
			"POSTGRES_USER=postgres",
			"POSTGRES_PASSWORD=postgres",
			"listen_addresses='*'",
		},
	}, func(config *docker.HostConfig) {
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		return nil, fmt.Errorf("could not start postgres container: %w", err)
	}

	if err := resource.Expire(120); err != nil {
		log.Printf("could not set resource expiry: %v", err)
	}

	databaseURL := fmt.Sprintf("postgres://postgres:postgres@localhost:%s/petstore_test?sslmode=disable",
		resource.GetPort("5432/tcp"))

	var pgPool *pgxpool.Pool
	if err := pool.Retry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var err error
		pgPool, err = pgxpool.New(ctx, databaseURL)
		if err != nil {
			return err
		}
		return pgPool.Ping(ctx)
	}); err != nil {
		if purgeErr := pool.Purge(resource); purgeErr != nil {
			log.Printf("could not purge resource: %v", purgeErr)
		}
		return nil, fmt.Errorf("could not connect to postgres: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := applyMigrations(ctx, pgPool); err != nil {
		pgPool.Close()
		if purgeErr := pool.Purge(resource); purgeErr != nil {
			log.Printf("could not purge resource: %v", purgeErr)
		}
		return nil, fmt.Errorf("could not apply migrations: %w", err)
	}

	return &TestDB{
		Pool:     pgPool,
		pool:     pool,
		resource: resource,
	}, nil
}

func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := fs.ReadDir(dbpkg.MigrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	var ups []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			ups = append(ups, e.Name())
		}
	}
	sort.Strings(ups)
	for _, name := range ups {
		data, err := fs.ReadFile(dbpkg.MigrationsFS, "migrations/"+name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

func (tdb *TestDB) Teardown() {
	if tdb.Pool != nil {
		tdb.Pool.Close()
	}
	if err := tdb.pool.Purge(tdb.resource); err != nil {
		log.Printf("could not purge dockertest resource: %v", err)
	}
}

func (tdb *TestDB) TruncatePets(ctx context.Context) error {
	_, err := tdb.Pool.Exec(ctx, "TRUNCATE TABLE pets RESTART IDENTITY")
	return err
}

func (tdb *TestDB) SeedPets(ctx context.Context) error {
	sql, err := dbpkg.SeedsFS.ReadFile("seeds/pets.sql")
	if err != nil {
		return fmt.Errorf("could not read seed file: %w", err)
	}
	_, err = tdb.Pool.Exec(ctx, string(sql))
	return err
}
