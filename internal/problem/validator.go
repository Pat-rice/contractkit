package problem

import (
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"

	"github.com/patrice/contractkit/internal/api"
)

// FromValidatorErrors converts go-playground/validator errors into ProblemError
// entries. The list is capped at MaxErrors; if more errors exist, a final
// truncation-marker entry is appended.
func FromValidatorErrors(ve validator.ValidationErrors) []api.ProblemError {
	keep := MaxErrors
	if len(ve) < keep {
		keep = len(ve)
	}
	out := make([]api.ProblemError, 0, keep+1)
	for i, fe := range ve {
		if i >= MaxErrors {
			break
		}
		name, in := locate(fe.Namespace())
		if name == "" {
			name = fe.Field()
		}
		out = append(out, api.ProblemError{
			Name:   name,
			In:     in,
			Rule:   fe.Tag(),
			Detail: describeFieldError(fe),
		})
	}
	if len(ve) > MaxErrors {
		out = append(out, api.ProblemError{
			Name:   "_truncated",
			In:     api.Body,
			Rule:   "truncated",
			Detail: fmt.Sprintf("%d additional validation errors omitted", len(ve)-MaxErrors),
		})
	}
	return out
}

// locate splits a validator namespace into (name, location). The validator
// namespace looks like "PetsCreatePetRequestObject.Body.tags[0]" or
// "PetsGetPetRequestObject.petId"; we strip the leading request-object segment
// and pick the location from the next segment.
func locate(namespace string) (name string, in api.ProblemErrorIn) {
	ns := namespace
	if i := strings.Index(ns, "."); i >= 0 {
		ns = ns[i+1:]
	} else {
		// Single segment — path param (e.g. petId on PetsGetPetRequestObject).
		return ns, api.Path
	}
	switch {
	case strings.HasPrefix(ns, "Body."):
		return ns[len("Body."):], api.Body
	case ns == "Body":
		return "", api.Body
	case strings.HasPrefix(ns, "Params."):
		return ns[len("Params."):], api.Query
	case ns == "Params":
		return "", api.Query
	case strings.HasPrefix(ns, "Headers."):
		return ns[len("Headers."):], api.Header
	case ns == "Headers":
		return "", api.Header
	default:
		// Fallback: assume a path param when no Body/Params/Headers segment is present.
		return ns, api.Path
	}
}

func describeFieldError(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "min":
		return fmt.Sprintf("must be at least %s", fe.Param())
	case "max":
		return fmt.Sprintf("must be at most %s", fe.Param())
	case "gte":
		return fmt.Sprintf("must be >= %s", fe.Param())
	case "lte":
		return fmt.Sprintf("must be <= %s", fe.Param())
	case "gt":
		return fmt.Sprintf("must be > %s", fe.Param())
	case "lt":
		return fmt.Sprintf("must be < %s", fe.Param())
	case "oneof":
		return fmt.Sprintf("must be one of [%s]", fe.Param())
	case "email":
		return "must be a valid email"
	case "url":
		return "must be a valid URL"
	case "uuid":
		return "must be a valid UUID"
	default:
		return fmt.Sprintf("failed %q validation", fe.Tag())
	}
}
