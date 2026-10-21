package main

import (
	"context"
	"github.com/hhoshii/marketplace-architecture/internal/runtime"
	"github.com/hhoshii/marketplace-architecture/internal/users"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
	"os"
)

func main() {
	runtime.Main("user-service", "users", func(ctx context.Context, db *pgxpool.Pool, log *slog.Logger) (http.Handler, error) {
		service, err := users.New(db, users.Config{PrivateKey: os.Getenv("JWT_PRIVATE_KEY")}, log)
		if err != nil {
			return nil, err
		}
		if err = service.BootstrapAdmin(ctx, os.Getenv("BOOTSTRAP_ADMIN_EMAIL"), os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")); err != nil {
			return nil, err
		}
		return service.Handler()
	})
}
