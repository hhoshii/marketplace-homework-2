package runtime

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Factory func(context.Context, *pgxpool.Pool, *slog.Logger) (http.Handler, error)

func Main(service, table string, factory Factory) {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", service)
	if err := run(log, table, factory); err != nil {
		log.Error("startup_failed", "error", err.Error())
		os.Exit(1)
	}
}
func run(log *slog.Logger, table string, factory Factory) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid DATABASE_URL")
	}
	config.MaxConns = 20
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	config.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "5000"
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return errors.New("cannot create database pool")
	}
	defer db.Close()
	if err = db.Ping(ctx); err != nil {
		return errors.New("database unavailable")
	}
	var migrated bool
	if err = db.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&migrated); err != nil || !migrated {
		return errors.New("database schema missing; run service Flyway migrate")
	}
	handler, err := factory(ctx, db, log)
	if err != nil {
		return err
	}
	address := os.Getenv("HTTP_ADDR")
	if address == "" {
		address = ":8080"
	}
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Info("server_started", "address", address)
	select {
	case err = <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
