package server

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/patrice/contractkit/internal/api"
	"github.com/patrice/contractkit/internal/db"
	"github.com/patrice/contractkit/internal/problem"
)

type Server struct {
	repo   Repository
	pool   *pgxpool.Pool
	logger *slog.Logger
}

var _ api.StrictServerInterface = (*Server)(nil)

func New(repo Repository, pool *pgxpool.Pool, logger *slog.Logger) *Server {
	return &Server{
		repo,
		pool,
		logger,
	}
}

func (s *Server) HealthHealthCheck(ctx context.Context, _ api.HealthHealthCheckRequestObject) (api.HealthHealthCheckResponseObject, error) {
	status := "ok"
	if err := s.pool.Ping(ctx); err != nil {
		status = "degraded"
	}
	return api.HealthHealthCheck200JSONResponse{Status: status}, nil
}

func (s *Server) PetsListPets(ctx context.Context, request api.PetsListPetsRequestObject) (api.PetsListPetsResponseObject, error) {
	limit := int32(20)
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}

	params := db.ListPetsParams{LimitVal: limit + 1}
	if request.Params.Cursor != nil {
		id, err := strconv.ParseInt(*request.Params.Cursor, 10, 64)
		if err != nil {
			return api.PetsListPets200JSONResponse(api.PetPage{Items: []api.Pet{}}), nil
		}
		params.Cursor = pgtype.Int8{Int64: id, Valid: true}
	}

	pets, err := s.repo.ListPets(ctx, params)
	if err != nil {
		return nil, err
	}

	var nextCursor *string
	if int32(len(pets)) > limit {
		pets = pets[:limit]
		c := strconv.FormatInt(pets[limit-1].ID, 10)
		nextCursor = &c
	}

	items := make([]api.Pet, len(pets))
	for i, p := range pets {
		items[i] = toAPIPet(p)
	}
	return api.PetsListPets200JSONResponse(api.PetPage{
		Items:      items,
		NextCursor: nextCursor,
	}), nil
}

func (s *Server) PetsGetPet(ctx context.Context, request api.PetsGetPetRequestObject) (api.PetsGetPetResponseObject, error) {
	pet, err := s.repo.GetPet(ctx, request.PetID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.PetsGetPet404ApplicationProblemPlusJSONResponse(problem.NotFound("pet not found")), nil
		}
		return nil, err
	}
	return api.PetsGetPet200JSONResponse(toAPIPet(pet)), nil
}

func (s *Server) PetsCreatePet(ctx context.Context, request api.PetsCreatePetRequestObject) (api.PetsCreatePetResponseObject, error) {
	pet, err := s.repo.CreatePet(ctx, db.CreatePetParams{
		Name: request.Body.Name,
		Kind: string(request.Body.Kind),
		Age:  request.Body.Age,
	})
	if err != nil {
		return nil, err
	}
	return api.PetsCreatePet201JSONResponse(toAPIPet(pet)), nil
}

func (s *Server) PetsUpdatePet(ctx context.Context, request api.PetsUpdatePetRequestObject) (api.PetsUpdatePetResponseObject, error) {
	params := db.UpdatePetParams{ID: request.PetID}

	if request.Body.Name != nil {
		params.Name = pgtype.Text{String: *request.Body.Name, Valid: true}
	}
	if request.Body.Kind != nil {
		params.Kind = pgtype.Text{String: string(*request.Body.Kind), Valid: true}
	}
	if request.Body.Age != nil {
		params.Age = pgtype.Int4{Int32: *request.Body.Age, Valid: true}
	}

	pet, err := s.repo.UpdatePet(ctx, params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.PetsUpdatePet404ApplicationProblemPlusJSONResponse(problem.NotFound("pet not found")), nil
		}
		return nil, err
	}
	return api.PetsUpdatePet200JSONResponse(toAPIPet(pet)), nil
}

func (s *Server) PetsDeletePet(ctx context.Context, request api.PetsDeletePetRequestObject) (api.PetsDeletePetResponseObject, error) {
	affected, err := s.repo.DeletePet(ctx, request.PetID)
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return api.PetsDeletePet404ApplicationProblemPlusJSONResponse(problem.NotFound("pet not found")), nil
	}
	return api.PetsDeletePet204Response{}, nil
}

func toAPIPet(p db.Pet) api.Pet {
	return api.Pet{
		ID:        p.ID,
		Name:      p.Name,
		Kind:      api.PetKind(p.Kind),
		Age:       p.Age,
		CreatedAt: p.CreatedAt.Time,
		UpdatedAt: p.UpdatedAt.Time,
	}
}
