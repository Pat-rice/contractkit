// Package problem builds RFC 9457 Problem Details responses for the API.
package problem

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/patrice/contractkit/internal/api"
)

// Project-owned slugs for the urn:problem-type:contractkit: namespace.
// Using typed constants prevents typo'd slugs from shipping silently to clients.
const (
	TypeBadRequest       = "badRequest"
	TypeValidationFailed = "validationFailed"
	TypeResourceNotFound = "resourceNotFound"
	TypeInternal         = "internal"
)

const typeURIPrefix = "urn:problem-type:contractkit:"

// MaxErrors caps the size of Problem.Errors to prevent unbounded amplification
// from large invalid payloads (e.g. a body with thousands of failing items).
const MaxErrors = 20

// New returns an api.Problem with type, title, status, detail and a freshly
// minted urn:uuid instance. Callers fill Errors when reporting validation.
func New(status int, slug, detail string) api.Problem {
	instance := "urn:uuid:" + uuid.NewString()
	p := api.Problem{
		Type:     typeURIPrefix + slug,
		Title:    http.StatusText(status),
		Status:   int32(status),
		Instance: &instance,
	}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

// NotFound returns a 404 resourceNotFound problem.
func NotFound(detail string) api.Problem {
	return New(http.StatusNotFound, TypeResourceNotFound, detail)
}

// BadRequest returns a 400 badRequest problem.
func BadRequest(detail string) api.Problem {
	return New(http.StatusBadRequest, TypeBadRequest, detail)
}

// Internal returns a 500 internal problem.
func Internal(detail string) api.Problem {
	return New(http.StatusInternalServerError, TypeInternal, detail)
}

// ValidationFailed returns a 400 validationFailed problem with the given errors[].
func ValidationFailed(errs []api.ProblemError) api.Problem {
	p := New(http.StatusBadRequest, TypeValidationFailed, "request validation failed")
	if len(errs) > 0 {
		p.Errors = &errs
	}
	return p
}
