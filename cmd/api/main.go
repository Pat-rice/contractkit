package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/patrice/contractkit/internal/api"
	"github.com/patrice/contractkit/internal/config"
	"github.com/patrice/contractkit/internal/db"
	"github.com/patrice/contractkit/internal/problem"
	"github.com/patrice/contractkit/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLogLevel(cfg.LogLevel)}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to create connection pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logger.Error("failed to ping database", "error", err)
		os.Exit(1)
	}

	queries := db.New(pool)
	srv := server.New(queries, pool, logger)

	handler := buildHandler(srv, logger)

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("starting server", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shutdown server", "error", err)
	}

	logger.Info("server stopped")
}

// buildHandler wires the strict handler, validator middleware, and the outer
// chain (mux + content-type rewrite + panic recovery). Extracted so tests can
// stand up the same wiring against httptest.
func buildHandler(srv api.StrictServerInterface, logger *slog.Logger) http.Handler {
	validate := validator.New(validator.WithRequiredStructEnabled())
	validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" || name == "" {
			return fld.Name
		}
		return name
	})

	strictHandler := api.NewStrictHandlerWithOptions(srv, []api.StrictMiddlewareFunc{validationMiddleware(validate)}, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			problem.Write(w, r, logger, problem.BadRequest(sanitizeRequestErr(err)))
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			var ve validator.ValidationErrors
			if errors.As(err, &ve) {
				problem.Write(w, r, logger, problem.ValidationFailed(problem.FromValidatorErrors(ve)))
				return
			}
			logger.Error("handler error", "error", err, "path", r.URL.Path, "method", r.Method)
			problem.Write(w, r, logger, problem.Internal("internal server error"))
		},
	})

	mux := http.NewServeMux()
	api.HandlerWithOptions(strictHandler, api.StdHTTPServerOptions{
		BaseRouter: mux,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			problem.Write(w, r, logger, problem.BadRequest(sanitizeBindErr(err)))
		},
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		problem.Write(w, r, logger, problem.NotFound("route not found"))
	})

	return recoverMiddleware(logger)(problem.ContentTypeMiddleware(mux))
}

func validationMiddleware(v *validator.Validate) api.StrictMiddlewareFunc {
	return func(f api.StrictHandlerFunc, _ string) api.StrictHandlerFunc {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request interface{}) (interface{}, error) {
			if err := v.StructCtx(ctx, request); err != nil {
				var ive *validator.InvalidValidationError
				if errors.As(err, &ive) {
					return f(ctx, w, r, request)
				}
				return nil, err
			}
			return f(ctx, w, r, request)
		}
	}
}

// recoverMiddleware turns a panic in any downstream handler into a 500
// application/problem+json response and a structured log line with the stack.
func recoverMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						"panic", fmt.Sprint(rec),
						"path", r.URL.Path,
						"method", r.Method,
						"stack", string(debug.Stack()),
					)
					problem.Write(w, r, logger, problem.Internal("internal server error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// sanitizeRequestErr returns a client-safe detail for body-decode failures from
// the strict handler. Raw stdlib json errors leak Go struct names and types
// (e.g. "cannot unmarshal string into Go struct field NewPet.age of type int32")
// so we collapse them to a small fixed vocabulary keyed on the JSON tag.
func sanitizeRequestErr(err error) string {
	var ute *json.UnmarshalTypeError
	if errors.As(err, &ute) {
		if ute.Field != "" {
			return fmt.Sprintf("field %q has the wrong type", lastJSONField(ute.Field))
		}
		return "request body has a field with the wrong type"
	}
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return "request body is not valid JSON"
	}
	return "request body could not be parsed"
}

// sanitizeBindErr returns a client-safe detail for path/query/header binding
// failures from oapi-codegen's outer router. The error types carry a
// spec-defined ParamName which is safe to surface; the wrapped error is not.
func sanitizeBindErr(err error) string {
	var ipfe *api.InvalidParamFormatError
	if errors.As(err, &ipfe) {
		return fmt.Sprintf("parameter %q has an invalid format", ipfe.ParamName)
	}
	var rpe *api.RequiredParamError
	if errors.As(err, &rpe) {
		return fmt.Sprintf("required query parameter %q is missing", rpe.ParamName)
	}
	var rhe *api.RequiredHeaderError
	if errors.As(err, &rhe) {
		return fmt.Sprintf("required header %q is missing", rhe.ParamName)
	}
	var tmve *api.TooManyValuesForParamError
	if errors.As(err, &tmve) {
		return fmt.Sprintf("parameter %q has too many values", tmve.ParamName)
	}
	var upe *api.UnmarshalingParamError
	if errors.As(err, &upe) {
		return fmt.Sprintf("parameter %q is malformed", upe.ParamName)
	}
	var ucpe *api.UnescapedCookieParamError
	if errors.As(err, &ucpe) {
		return fmt.Sprintf("cookie parameter %q is malformed", ucpe.ParamName)
	}
	return "request parameters could not be parsed"
}

func lastJSONField(field string) string {
	if i := strings.LastIndex(field, "."); i >= 0 {
		return field[i+1:]
	}
	return field
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
