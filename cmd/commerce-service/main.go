package main

import (
	"context"
	"errors"
	"github.com/hhoshii/marketplace-architecture/internal/commerce"
	"github.com/hhoshii/marketplace-architecture/internal/runtime"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	runtime.Main("commerce-service", "products", func(ctx context.Context, db *pgxpool.Pool, log *slog.Logger) (http.Handler, error) {
		interval := time.Minute
		if value := os.Getenv("ORDER_INTERVAL"); value != "" {
			var err error
			interval, err = time.ParseDuration(value)
			if err != nil {
				return nil, errors.New("invalid ORDER_INTERVAL")
			}
		}
		service, err := commerce.New(db, commerce.Config{PublicKey: os.Getenv("JWT_PUBLIC_KEY"), OrderInterval: interval}, log)
		if err != nil {
			return nil, err
		}
		return service.Handler()
	})
}
