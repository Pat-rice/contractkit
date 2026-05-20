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
	"strings"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/patrice/contractkit/internal/api"
	"github.com/patrice/contractkit/internal/config"
	"github.com/patrice/contractkit/internal/db"
	"github.com/patrice/contractkit/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	var logLevel slog.Level
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))

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
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			err = json.NewEncoder(w).Encode(api.Error{Code: "BAD_REQUEST", Message: err.Error()})
			if err != nil {
				return
			}
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			var ve validator.ValidationErrors
			if errors.As(err, &ve) {
				details := toValidationDetails(ve)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(api.Error{
					Code:    "VALIDATION_ERROR",
					Message: "request validation failed",
					Details: &details,
				})
				return
			}
			logger.Error("internal error", "error", err, "path", r.URL.Path, "method", r.Method)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			err = json.NewEncoder(w).Encode(api.Error{Code: "INTERNAL", Message: "internal server error"})
			if err != nil {
				return
			}
		},
	})

	mux := http.NewServeMux()
	api.HandlerFromMux(strictHandler, mux)

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
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

func toValidationDetails(ve validator.ValidationErrors) []api.ValidationDetail {
	out := make([]api.ValidationDetail, 0, len(ve))
	for _, fe := range ve {
		out = append(out, api.ValidationDetail{
			Field:   fieldPath(fe),
			Rule:    fe.Tag(),
			Message: describeFieldError(fe),
		})
	}
	return out
}

func fieldPath(fe validator.FieldError) string {
	ns := fe.Namespace()
	if i := strings.Index(ns, "."); i >= 0 {
		return ns[i+1:]
	}
	return fe.Field()
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
