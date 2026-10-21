package commerce

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/hhoshii/marketplace-architecture/internal/platform"
	"github.com/hhoshii/marketplace-architecture/internal/security"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	PublicKey     string
	OrderInterval time.Duration
}
type Service struct {
	*platform.Base
	Config Config
}

func New(db *pgxpool.Pool, c Config, log *slog.Logger) (*Service, error) {
	verifier, err := security.NewVerifier(c.PublicKey)
	if err != nil {
		return nil, err
	}
	if c.OrderInterval < 0 {
		return nil, errors.New("order interval must not be negative")
	}
	return &Service{Base: &platform.Base{DB: db, Log: log, Verifier: verifier}, Config: c}, nil
}
func (s *Service) Handler() (http.Handler, error) { return s.Base.Handler(s, "commerce") }
