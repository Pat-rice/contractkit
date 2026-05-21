package problem

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"

	"github.com/patrice/contractkit/internal/api"
)

func TestNew(t *testing.T) {
	p := New(http.StatusNotFound, TypeResourceNotFound, "pet not found")
	if p.Type != "urn:problem-type:contractkit:resourceNotFound" {
		t.Fatalf("type: %q", p.Type)
	}
	if p.Title != http.StatusText(http.StatusNotFound) {
		t.Fatalf("title: %q", p.Title)
	}
	if p.Status != 404 {
		t.Fatalf("status: %d", p.Status)
	}
	if p.Detail == nil || *p.Detail != "pet not found" {
		t.Fatalf("detail: %v", p.Detail)
	}
	if p.Instance == nil || !strings.HasPrefix(*p.Instance, "urn:uuid:") {
		t.Fatalf("instance: %v", p.Instance)
	}
	if p.Errors != nil {
		t.Fatalf("errors should be nil: %v", p.Errors)
	}
}

func TestNew_EmptyDetailIsOmitted(t *testing.T) {
	p := New(http.StatusInternalServerError, TypeInternal, "")
	if p.Detail != nil {
		t.Fatalf("detail should be nil for empty input, got %v", *p.Detail)
	}
}

func TestShortcuts(t *testing.T) {
	cases := []struct {
		name      string
		got       api.Problem
		wantSlug  string
		wantCode  int32
	}{
		{"NotFound", NotFound("x"), TypeResourceNotFound, 404},
		{"BadRequest", BadRequest("x"), TypeBadRequest, 400},
		{"Internal", Internal("x"), TypeInternal, 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got.Status != c.wantCode {
				t.Errorf("status %d, want %d", c.got.Status, c.wantCode)
			}
			if c.got.Type != typeURIPrefix+c.wantSlug {
				t.Errorf("type %q, want suffix %q", c.got.Type, c.wantSlug)
			}
		})
	}
}

func TestValidationFailed(t *testing.T) {
	p := ValidationFailed([]api.ProblemError{{Name: "x", In: api.Body, Rule: "required", Detail: "is required"}})
	if p.Status != 400 || p.Type != typeURIPrefix+TypeValidationFailed {
		t.Fatalf("unexpected type/status: %s %d", p.Type, p.Status)
	}
	if p.Errors == nil || len(*p.Errors) != 1 {
		t.Fatalf("errors: %v", p.Errors)
	}
}

func TestValidationFailed_EmptyErrorsIsNil(t *testing.T) {
	p := ValidationFailed(nil)
	if p.Errors != nil {
		t.Fatalf("Errors should be nil for empty input")
	}
}

func TestLocate(t *testing.T) {
	cases := []struct {
		namespace string
		wantName  string
		wantIn    api.ProblemErrorIn
	}{
		{"PetsCreatePetRequestObject.Body.name", "name", api.Body},
		{"PetsCreatePetRequestObject.Body.tags[0]", "tags[0]", api.Body},
		{"PetsCreatePetRequestObject.Body.tags[0].sub", "tags[0].sub", api.Body},
		{"PetsCreatePetRequestObject.Body", "", api.Body},
		{"PetsListPetsRequestObject.Params.limit", "limit", api.Query},
		{"PetsListPetsRequestObject.Params", "", api.Query},
		{"WhateverRequestObject.Headers.X-Foo", "X-Foo", api.Header},
		{"PetsGetPetRequestObject.petId", "petId", api.Path},
		{"OnlyOneSegment", "OnlyOneSegment", api.Path},
	}
	for _, c := range cases {
		t.Run(c.namespace, func(t *testing.T) {
			gotName, gotIn := locate(c.namespace)
			if gotName != c.wantName || gotIn != c.wantIn {
				t.Errorf("locate(%q) = (%q, %s), want (%q, %s)", c.namespace, gotName, gotIn, c.wantName, c.wantIn)
			}
		})
	}
}

type fakeBody struct {
	Name string `validate:"required" json:"name"`
}
type fakeParams struct {
	Limit int `validate:"gte=1,lte=200" json:"limit"`
}
type fakeRequest struct {
	Body   *fakeBody
	Params fakeParams
}

func TestFromValidatorErrors_In(t *testing.T) {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" || name == "" {
			return fld.Name
		}
		return name
	})

	err := v.Struct(fakeRequest{Body: &fakeBody{Name: ""}, Params: fakeParams{Limit: 0}})
	ve, ok := err.(validator.ValidationErrors)
	if !ok {
		t.Fatalf("expected validation errors, got %v", err)
	}
	errs := FromValidatorErrors(ve)
	if len(errs) < 2 {
		t.Fatalf("expected ≥2 errors, got %d", len(errs))
	}
	byName := map[string]api.ProblemError{}
	for _, e := range errs {
		byName[e.Name] = e
	}
	if e, ok := byName["name"]; !ok || e.In != api.Body || e.Rule != "required" {
		t.Errorf("body name entry: %+v", e)
	}
	if e, ok := byName["limit"]; !ok || e.In != api.Query || e.Rule != "gte" {
		t.Errorf("query limit entry: %+v", e)
	}
}

func TestFromValidatorErrors_Truncation(t *testing.T) {
	// Synthesize MaxErrors+5 errors by running validator on a slice of failing structs.
	v := validator.New(validator.WithRequiredStructEnabled())
	type item struct {
		Name string `validate:"required"`
	}
	type parent struct {
		Items []item `validate:"dive"`
	}
	items := make([]item, MaxErrors+5)
	err := v.Struct(parent{Items: items})
	ve := err.(validator.ValidationErrors)
	if len(ve) != MaxErrors+5 {
		t.Fatalf("setup: expected %d ve, got %d", MaxErrors+5, len(ve))
	}
	out := FromValidatorErrors(ve)
	if len(out) != MaxErrors+1 {
		t.Fatalf("expected %d entries (cap + marker), got %d", MaxErrors+1, len(out))
	}
	last := out[len(out)-1]
	if last.Name != "_truncated" || last.Rule != "truncated" {
		t.Errorf("last entry is not the truncation marker: %+v", last)
	}
}

func TestContentTypeMiddleware_RewritesJSONOn4xx(t *testing.T) {
	cases := []struct {
		name       string
		preset     string
		status     int
		wantHeader string
	}{
		{"json 4xx", "application/json", 404, ContentType},
		{"json charset 4xx", "application/json; charset=utf-8", 400, ContentType},
		{"json 2xx untouched", "application/json", 200, "application/json"},
		{"text 4xx untouched", "text/plain; charset=utf-8", 404, "text/plain; charset=utf-8"},
		{"no header 4xx untouched", "", 404, ""},
		{"json 5xx", "application/json", 500, ContentType},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := ContentTypeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.preset != "" {
					w.Header().Set("Content-Type", c.preset)
				}
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte("body"))
			}))
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/x", nil)
			h.ServeHTTP(rec, req)
			if got := rec.Header().Get("Content-Type"); got != c.wantHeader {
				t.Errorf("content-type = %q, want %q", got, c.wantHeader)
			}
		})
	}
}

func TestContentTypeMiddleware_SuppressesDoubleWriteHeader(t *testing.T) {
	h := ContentTypeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.WriteHeader(500) // second call must be a no-op via our wrapper
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 400 {
		t.Fatalf("status code: got %d, want 400 (second WriteHeader must be suppressed)", rec.Code)
	}
}

func TestWrite_SetsContentTypeAndLogs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	p := NotFound("missing")
	Write(rec, req, logger, p)

	if got := rec.Header().Get("Content-Type"); got != ContentType {
		t.Fatalf("content-type: %q", got)
	}
	if rec.Code != 404 {
		t.Fatalf("status: %d", rec.Code)
	}
	var decoded api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Type != p.Type || decoded.Status != p.Status {
		t.Errorf("body mismatch: %+v", decoded)
	}
}
