package server

import (
	"context"

	"github.com/patrice/contractkit/internal/db"
)

type Repository interface {
	ListPets(ctx context.Context, arg db.ListPetsParams) ([]db.Pet, error)
	GetPet(ctx context.Context, id int64) (db.Pet, error)
	CreatePet(ctx context.Context, arg db.CreatePetParams) (db.Pet, error)
	UpdatePet(ctx context.Context, arg db.UpdatePetParams) (db.Pet, error)
	DeletePet(ctx context.Context, id int64) (int64, error)
}
