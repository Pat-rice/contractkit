package server

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/patrice/contractkit/internal/api"
	"github.com/patrice/contractkit/internal/db"
)

type fakeRepo struct {
	pets   map[int64]db.Pet
	nextID int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{pets: make(map[int64]db.Pet), nextID: 1}
}

func (f *fakeRepo) ListPets(_ context.Context, arg db.ListPetsParams) ([]db.Pet, error) {
	result := make([]db.Pet, 0, len(f.pets))
	for _, p := range f.pets {
		result = append(result, p)
	}
	// Sort by ID descending
	sort.Slice(result, func(i, j int) bool { return result[i].ID > result[j].ID })
	// Apply cursor filter
	if arg.Cursor.Valid {
		filtered := result[:0:0]
		for _, p := range result {
			if p.ID < arg.Cursor.Int64 {
				filtered = append(filtered, p)
			}
		}
		result = filtered
	}
	if int(arg.LimitVal) < len(result) {
		result = result[:arg.LimitVal]
	}
	return result, nil
}

func (f *fakeRepo) GetPet(_ context.Context, id int64) (db.Pet, error) {
	p, ok := f.pets[id]
	if !ok {
		return db.Pet{}, pgx.ErrNoRows
	}
	return p, nil
}

func (f *fakeRepo) CreatePet(_ context.Context, arg db.CreatePetParams) (db.Pet, error) {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	p := db.Pet{
		ID:        f.nextID,
		Name:      arg.Name,
		Kind:      arg.Kind,
		Age:       arg.Age,
		CreatedAt: now,
		UpdatedAt: now,
	}
	f.pets[f.nextID] = p
	f.nextID++
	return p, nil
}

func (f *fakeRepo) UpdatePet(_ context.Context, arg db.UpdatePetParams) (db.Pet, error) {
	p, ok := f.pets[arg.ID]
	if !ok {
		return db.Pet{}, pgx.ErrNoRows
	}
	if arg.Name.Valid {
		p.Name = arg.Name.String
	}
	if arg.Kind.Valid {
		p.Kind = arg.Kind.String
	}
	if arg.Age.Valid {
		p.Age = arg.Age.Int32
	}
	p.UpdatedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.pets[arg.ID] = p
	return p, nil
}

func (f *fakeRepo) DeletePet(_ context.Context, id int64) (int64, error) {
	if _, ok := f.pets[id]; !ok {
		return 0, nil
	}
	delete(f.pets, id)
	return 1, nil
}

func TestCreateAndGetPet(t *testing.T) {
	repo := newFakeRepo()
	srv := New(repo, nil, nil)
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

	getResp, err := srv.PetsGetPet(ctx, api.PetsGetPetRequestObject{PetID: created.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := getResp.(api.PetsGetPet200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", getResp)
	}
	if got.Name != "Rex" {
		t.Fatalf("expected name Rex, got %s", got.Name)
	}
}

func TestGetPetNotFound(t *testing.T) {
	repo := newFakeRepo()
	srv := New(repo, nil, nil)

	resp, err := srv.PetsGetPet(context.Background(), api.PetsGetPetRequestObject{PetID: 999})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(api.PetsGetPet404ApplicationProblemPlusJSONResponse); !ok {
		t.Fatalf("expected 404 response, got %T", resp)
	}
}

func TestListPets(t *testing.T) {
	repo := newFakeRepo()
	srv := New(repo, nil, nil)
	ctx := context.Background()

	for _, name := range []string{"Rex", "Milo", "Luna"} {
		_, _ = srv.PetsCreatePet(ctx, api.PetsCreatePetRequestObject{
			Body: &api.NewPet{Name: name, Kind: api.Dog, Age: 2},
		})
	}

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
}

func TestListPetsCursorPagination(t *testing.T) {
	repo := newFakeRepo()
	srv := New(repo, nil, nil)
	ctx := context.Background()

	for _, name := range []string{"A", "B", "C", "D", "E"} {
		_, _ = srv.PetsCreatePet(ctx, api.PetsCreatePetRequestObject{
			Body: &api.NewPet{Name: name, Kind: api.Cat, Age: 1},
		})
	}

	// First page: limit=2
	limit := int32(2)
	resp, err := srv.PetsListPets(ctx, api.PetsListPetsRequestObject{
		Params: api.PetsListPetsParams{Limit: &limit},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	page := resp.(api.PetsListPets200JSONResponse)
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 pets, got %d", len(page.Items))
	}
	if page.NextCursor == nil {
		t.Fatal("expected next cursor for first page")
	}

	// Second page using cursor
	cursor := page.NextCursor
	resp, err = srv.PetsListPets(ctx, api.PetsListPetsRequestObject{
		Params: api.PetsListPetsParams{Limit: &limit, Cursor: cursor},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	page2 := resp.(api.PetsListPets200JSONResponse)
	if len(page2.Items) != 2 {
		t.Fatalf("expected 2 pets on second page, got %d", len(page2.Items))
	}
	if page2.NextCursor == nil {
		t.Fatal("expected next cursor for second page")
	}

	// Third page: should have 1 item, no next cursor
	resp, err = srv.PetsListPets(ctx, api.PetsListPetsRequestObject{
		Params: api.PetsListPetsParams{Limit: &limit, Cursor: page2.NextCursor},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	page3 := resp.(api.PetsListPets200JSONResponse)
	if len(page3.Items) != 1 {
		t.Fatalf("expected 1 pet on last page, got %d", len(page3.Items))
	}
	if page3.NextCursor != nil {
		t.Fatalf("expected no next cursor on last page, got %s", *page3.NextCursor)
	}
}

func TestUpdatePet(t *testing.T) {
	repo := newFakeRepo()
	srv := New(repo, nil, nil)
	ctx := context.Background()

	createResp, _ := srv.PetsCreatePet(ctx, api.PetsCreatePetRequestObject{
		Body: &api.NewPet{Name: "Rex", Kind: api.Dog, Age: 3},
	})
	created := createResp.(api.PetsCreatePet201JSONResponse)

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
}

func TestDeletePet(t *testing.T) {
	repo := newFakeRepo()
	srv := New(repo, nil, nil)
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
