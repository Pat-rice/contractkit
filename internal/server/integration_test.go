package server

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"testing"

	"github.com/patrice/contractkit/internal/api"
	"github.com/patrice/contractkit/internal/db"
	"github.com/patrice/contractkit/internal/testutil"
)

var testDB *testutil.TestDB

func TestMain(m *testing.M) {
	flag.Parse()

	if !testing.Short() {
		var err error
		testDB, err = testutil.SetupTestDB()
		if err != nil {
			slog.Error("failed to setup test db, skipping integration tests", "error", err)
		}
	}

	code := m.Run()

	if testDB != nil {
		testDB.Teardown()
	}
	os.Exit(code)
}

func skipIfNoTestDB(t *testing.T) {
	t.Helper()
	if testDB == nil {
		t.Skip("no test database available (Docker not running or -short flag)")
	}
}

func newIntegrationServer(t *testing.T) *Server {
	t.Helper()
	skipIfNoTestDB(t)

	ctx := context.Background()
	if err := testDB.TruncatePets(ctx); err != nil {
		t.Fatalf("failed to truncate pets: %v", err)
	}

	queries := db.New(testDB.Pool)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return New(queries, testDB.Pool, logger)
}

func TestIntegrationCreateAndGetPet(t *testing.T) {
	srv := newIntegrationServer(t)
	ctx := context.Background()

	createResp, err := srv.PetsCreatePet(ctx, api.PetsCreatePetRequestObject{
		Body: &api.NewPet{Name: "Rex", Kind: api.Dog, Age: 3},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	created, ok := createResp.(api.PetsCreatePet201JSONResponse)
	if !ok {
		t.Fatalf("expected 201 response, got %T", createResp)
	}
	if created.Name != "Rex" || created.Kind != api.Dog || created.Age != 3 {
		t.Fatalf("unexpected pet values: %+v", created)
	}
	if created.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("expected non-zero createdAt")
	}

	getResp, err := srv.PetsGetPet(ctx, api.PetsGetPetRequestObject{PetID: created.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := getResp.(api.PetsGetPet200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", getResp)
	}
	if got.Name != "Rex" || got.ID != created.ID {
		t.Fatalf("expected Rex with ID %d, got %+v", created.ID, got)
	}
}

func TestIntegrationGetPetNotFound(t *testing.T) {
	srv := newIntegrationServer(t)

	resp, err := srv.PetsGetPet(context.Background(), api.PetsGetPetRequestObject{PetID: 99999})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	notFound, ok := resp.(api.PetsGetPet404ApplicationProblemPlusJSONResponse)
	if !ok {
		t.Fatalf("expected 404 response, got %T", resp)
	}
	if notFound.Type != "urn:problem-type:contractkit:resourceNotFound" {
		t.Fatalf("unexpected problem type: %s", notFound.Type)
	}
	if notFound.Status != 404 {
		t.Fatalf("expected status 404, got %d", notFound.Status)
	}
}

func TestIntegrationListPets(t *testing.T) {
	srv := newIntegrationServer(t)
	ctx := context.Background()

	names := []string{"Rex", "Milo", "Luna"}
	for _, name := range names {
		_, err := srv.PetsCreatePet(ctx, api.PetsCreatePetRequestObject{
			Body: &api.NewPet{Name: name, Kind: api.Cat, Age: 2},
		})
		if err != nil {
			t.Fatalf("failed to create pet %s: %v", name, err)
		}
	}

	// All pets, no cursor
	resp, err := srv.PetsListPets(ctx, api.PetsListPetsRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	page, ok := resp.(api.PetsListPets200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if len(page.Items) != 3 {
		t.Fatalf("expected 3 pets, got %d", len(page.Items))
	}
	if page.NextCursor != nil {
		t.Fatalf("expected no next cursor, got %s", *page.NextCursor)
	}

	// Page 1 with limit=2
	limit := int32(2)
	resp, err = srv.PetsListPets(ctx, api.PetsListPetsRequestObject{
		Params: api.PetsListPetsParams{Limit: &limit},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	page1 := resp.(api.PetsListPets200JSONResponse)
	if len(page1.Items) != 2 {
		t.Fatalf("expected 2 pets, got %d", len(page1.Items))
	}
	if page1.NextCursor == nil {
		t.Fatal("expected next cursor for first page")
	}

	// Page 2 using cursor
	resp, err = srv.PetsListPets(ctx, api.PetsListPetsRequestObject{
		Params: api.PetsListPetsParams{Limit: &limit, Cursor: page1.NextCursor},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	page2 := resp.(api.PetsListPets200JSONResponse)
	if len(page2.Items) != 1 {
		t.Fatalf("expected 1 pet on second page, got %d", len(page2.Items))
	}
	if page2.NextCursor != nil {
		t.Fatalf("expected no next cursor on last page, got %s", *page2.NextCursor)
	}
}

func TestIntegrationUpdatePet(t *testing.T) {
	srv := newIntegrationServer(t)
	ctx := context.Background()

	createResp, _ := srv.PetsCreatePet(ctx, api.PetsCreatePetRequestObject{
		Body: &api.NewPet{Name: "Rex", Kind: api.Dog, Age: 3},
	})
	created := createResp.(api.PetsCreatePet201JSONResponse)

	// Partial update: only name
	newName := "Max"
	updateResp, err := srv.PetsUpdatePet(ctx, api.PetsUpdatePetRequestObject{
		PetID: created.ID,
		Body:  &api.UpdatePet{Name: &newName},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated, ok := updateResp.(api.PetsUpdatePet200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", updateResp)
	}
	if updated.Name != "Max" {
		t.Fatalf("expected name Max, got %s", updated.Name)
	}
	if updated.Age != 3 {
		t.Fatalf("expected age 3 unchanged, got %d", updated.Age)
	}
	if updated.Kind != api.Dog {
		t.Fatalf("expected kind dog unchanged, got %s", updated.Kind)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatal("expected updatedAt to be after creation time")
	}
}

func TestIntegrationUpdatePetNotFound(t *testing.T) {
	srv := newIntegrationServer(t)

	newName := "Ghost"
	resp, err := srv.PetsUpdatePet(context.Background(), api.PetsUpdatePetRequestObject{
		PetID: 99999,
		Body:  &api.UpdatePet{Name: &newName},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(api.PetsUpdatePet404ApplicationProblemPlusJSONResponse); !ok {
		t.Fatalf("expected 404 response, got %T", resp)
	}
}

func TestIntegrationDeletePet(t *testing.T) {
	srv := newIntegrationServer(t)
	ctx := context.Background()

	createResp, _ := srv.PetsCreatePet(ctx, api.PetsCreatePetRequestObject{
		Body: &api.NewPet{Name: "Rex", Kind: api.Dog, Age: 3},
	})
	created := createResp.(api.PetsCreatePet201JSONResponse)

	deleteResp, err := srv.PetsDeletePet(ctx, api.PetsDeletePetRequestObject{PetID: created.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := deleteResp.(api.PetsDeletePet204Response); !ok {
		t.Fatalf("expected 204 response, got %T", deleteResp)
	}

	getResp, _ := srv.PetsGetPet(ctx, api.PetsGetPetRequestObject{PetID: created.ID})
	if _, ok := getResp.(api.PetsGetPet404ApplicationProblemPlusJSONResponse); !ok {
		t.Fatalf("expected 404 after delete, got %T", getResp)
	}
}

func TestIntegrationDeletePetNotFound(t *testing.T) {
	srv := newIntegrationServer(t)

	resp, err := srv.PetsDeletePet(context.Background(), api.PetsDeletePetRequestObject{PetID: 99999})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	notFound, ok := resp.(api.PetsDeletePet404ApplicationProblemPlusJSONResponse)
	if !ok {
		t.Fatalf("expected 404 response, got %T", resp)
	}
	if notFound.Type != "urn:problem-type:contractkit:resourceNotFound" {
		t.Fatalf("unexpected problem type: %s", notFound.Type)
	}
}

func TestIntegrationHealthCheck(t *testing.T) {
	srv := newIntegrationServer(t)

	resp, err := srv.HealthHealthCheck(context.Background(), api.HealthHealthCheckRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	health, ok := resp.(api.HealthHealthCheck200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if health.Status != "ok" {
		t.Fatalf("expected status ok, got %s", health.Status)
	}
}
