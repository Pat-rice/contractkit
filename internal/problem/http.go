package problem

import (
	"encoding/json"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/patrice/contractkit/internal/api"
)

// ContentType is the IANA media type for RFC 9457 problem documents.
const ContentType = "application/problem+json"

// Write serialises a problem with status and application/problem+json. Also
// emits a structured log line carrying the instance URN so operators can
// correlate a client-reported instance back to a server log entry.
func Write(w http.ResponseWriter, r *http.Request, logger *slog.Logger, p api.Problem) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(int(p.Status))
	_ = json.NewEncoder(w).Encode(p)

	if logger == nil {
		return
	}
	level := slog.LevelInfo
	if p.Status >= 500 {
		level = slog.LevelError
	}
	var instance string
	if p.Instance != nil {
		instance = *p.Instance
	}
	logger.Log(r.Context(), level, "problem response",
		"instance", instance,
		"type", p.Type,
		"status", p.Status,
		"path", r.URL.Path,
		"method", r.Method,
	)
}

// ContentTypeMiddleware rewrites application/json to application/problem+json
// on any 4xx/5xx response. Required because oapi-codegen's generated
// *JSONResponse writers hardcode application/json for every status code; the
// rewrite catches handler-returned errors that don't go through [Write].
//
// TODO(oapi-codegen): remove once the generator emits per-status content types
// (issue: https://github.com/oapi-codegen/oapi-codegen/issues/333 and related).
func ContentTypeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&contentTypeWriter{ResponseWriter: w}, r)
	})
}

type contentTypeWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

// Unwrap exposes the underlying writer to http.ResponseController so callers
// can still reach Hijack/Flush/SetWriteDeadline if needed.
func (w *contentTypeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *contentTypeWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	if status >= 400 {
		if ct := w.Header().Get("Content-Type"); ct != "" {
			if mt, _, err := mime.ParseMediaType(ct); err == nil && strings.HasPrefix(mt, "application/json") {
				w.Header().Set("Content-Type", ContentType)
			}
		}
	}
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *contentTypeWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

var _ http.ResponseWriter = (*contentTypeWriter)(nil)
