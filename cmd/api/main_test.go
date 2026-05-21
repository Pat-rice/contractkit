package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/patrice/contractkit/internal/api"
	"github.com/patrice/contractkit/internal/db"
	"github.com/patrice/contractkit/internal/problem"
	"github.com/patrice/contractkit/internal/server"
)

// Minimal in-memory fake repository for wiring tests. Mirrors the one in
// internal/server, kept here to keep this test package free of cross-package
// test-helper visibility constraints.
type fakeRepo struct {
	pets     map[int64]db.Pet
	nextID   int64
	getPanic bool
}

func newFakeRepo() *fakeRepo { return &fakeRepo{pets: map[int64]db.Pet{}, nextID: 1} }

func (f *fakeRepo) ListPets(_ context.Context, _ db.ListPetsParams) ([]db.Pet, error) {
	return nil, nil
}

func (f *fakeRepo) GetPet(_ context.Context, id int64) (db.Pet, error) {
	if f.getPanic {
		panic("boom")
	}
	p, ok := f.pets[id]
	if !ok {
		return db.Pet{}, pgx.ErrNoRows
	}
	return p, nil
}

func (f *fakeRepo) CreatePet(_ context.Context, arg db.CreatePetParams) (db.Pet, error) {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	p := db.Pet{ID: f.nextID, Name: arg.Name, Kind: arg.Kind, Age: arg.Age, CreatedAt: now, UpdatedAt: now}
	f.pets[f.nextID] = p
	f.nextID++
	return p, nil
}

func (f *fakeRepo) UpdatePet(_ context.Context, _ db.UpdatePetParams) (db.Pet, error) {
	return db.Pet{}, pgx.ErrNoRows
}
func (f *fakeRepo) DeletePet(_ context.Context, _ int64) (int64, error) { return 0, nil }

func newTestServer(t *testing.T) (*httptest.Server, *fakeRepo) {
	t.Helper()
	repo := newFakeRepo()
	srv := server.New(repo, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := buildHandler(srv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, repo
}

func decodeProblem(t *testing.T, body io.Reader) api.Problem {
	t.Helper()
	var p api.Problem
	if err := json.NewDecoder(body).Decode(&p); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	return p
}

func TestHTTP_NotFound_Pet(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/pets/99999")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("content-type: %q", ct)
	}
	p := decodeProblem(t, resp.Body)
	if p.Type != "urn:problem-type:contractkit:resourceNotFound" {
		t.Errorf("type: %q", p.Type)
	}
	if p.Status != 404 {
		t.Errorf("status: %d", p.Status)
	}
	if p.Instance == nil || !strings.HasPrefix(*p.Instance, "urn:uuid:") {
		t.Errorf("instance: %v", p.Instance)
	}
}

func TestHTTP_UnknownRoute(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("content-type: %q", ct)
	}
	p := decodeProblem(t, resp.Body)
	if p.Type != "urn:problem-type:contractkit:resourceNotFound" {
		t.Errorf("type: %q", p.Type)
	}
}

func TestHTTP_BadRequest_Decode(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Post(ts.URL+"/pets", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("content-type: %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if bytes.Contains(body, []byte("json:")) || bytes.Contains(body, []byte("oapi")) || bytes.Contains(body, []byte("Go struct")) {
		t.Errorf("leaks internals: %s", body)
	}
	var p api.Problem
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Type != "urn:problem-type:contractkit:badRequest" {
		t.Errorf("type: %q", p.Type)
	}
}

func TestHTTP_BadRequest_WrongType(t *testing.T) {
	ts, _ := newTestServer(t)
	// "age" should be int; send a string instead.
	resp, err := http.Post(ts.URL+"/pets", "application/json",
		strings.NewReader(`{"name":"x","kind":"dog","age":"oops"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if bytes.Contains(body, []byte("NewPet")) || bytes.Contains(body, []byte("int32")) {
		t.Errorf("leaks Go internals: %s", body)
	}
	var p api.Problem
	_ = json.Unmarshal(body, &p)
	if p.Detail == nil || !strings.Contains(*p.Detail, "age") {
		t.Errorf("detail should mention the json field 'age', got %v", p.Detail)
	}
}

func TestHTTP_Validation_Body(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Post(ts.URL+"/pets", "application/json",
		strings.NewReader(`{"name":"","kind":"unicorn","age":-1,"tags":["","ok"]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("content-type: %q", ct)
	}
	p := decodeProblem(t, resp.Body)
	if p.Type != "urn:problem-type:contractkit:validationFailed" {
		t.Fatalf("type: %q", p.Type)
	}
	if p.Errors == nil {
		t.Fatal("errors[] is nil")
	}
	seen := map[string]api.ProblemErrorIn{}
	for _, e := range *p.Errors {
		seen[e.Name] = e.In
	}
	for _, want := range []string{"name", "kind", "age"} {
		if got, ok := seen[want]; !ok || got != api.Body {
			t.Errorf("missing/wrong body error for %q: got %s (present=%v)", want, got, ok)
		}
	}
}

func TestHTTP_Validation_Query(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/pets?limit=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("content-type: %q", ct)
	}
	p := decodeProblem(t, resp.Body)
	if p.Type != "urn:problem-type:contractkit:validationFailed" {
		t.Fatalf("type: %q", p.Type)
	}
	if p.Errors == nil || len(*p.Errors) == 0 {
		t.Fatal("errors[] is nil/empty")
	}
	got := (*p.Errors)[0]
	if got.Name != "limit" || got.In != api.Query {
		t.Errorf("first error: %+v (want name=limit, in=query)", got)
	}
}

func TestHTTP_PathBindError(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/pets/abc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("content-type: %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, bad := range []string{"strconv", "ParseInt", "Invalid format for parameter"} {
		if bytes.Contains(body, []byte(bad)) {
			t.Errorf("leaks %q: %s", bad, body)
		}
	}
	var p api.Problem
	_ = json.Unmarshal(body, &p)
	if p.Type != "urn:problem-type:contractkit:badRequest" {
		t.Errorf("type: %q", p.Type)
	}
	if p.Detail == nil || !strings.Contains(*p.Detail, "petId") {
		t.Errorf("detail should mention petId, got %v", p.Detail)
	}
}

func TestHTTP_Panic(t *testing.T) {
	ts, repo := newTestServer(t)
	repo.getPanic = true
	resp, err := http.Get(ts.URL + "/pets/1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("content-type: %q", ct)
	}
	p := decodeProblem(t, resp.Body)
	if p.Type != "urn:problem-type:contractkit:internal" {
		t.Errorf("type: %q", p.Type)
	}
}
