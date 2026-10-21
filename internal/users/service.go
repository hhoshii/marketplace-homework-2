package users

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
	PrivateKey            string
	AccessTTL, RefreshTTL time.Duration
}
type Service struct {
	*platform.Base
	Config Config
	Tokens *security.Tokens
}

func New(db *pgxpool.Pool, c Config, log *slog.Logger) (*Service, error) {
	tokens, err := security.NewSigner(c.PrivateKey)
	if err != nil {
		return nil, err
	}
	if c.AccessTTL == 0 {
		c.AccessTTL = 15 * time.Minute
	}
	if c.RefreshTTL == 0 {
		c.RefreshTTL = 14 * 24 * time.Hour
	}
	if c.AccessTTL < 15*time.Minute || c.AccessTTL > 30*time.Minute {
		return nil, errors.New("access TTL must be 15–30 minutes")
	}
	if c.RefreshTTL < 7*24*time.Hour || c.RefreshTTL > 30*24*time.Hour {
		return nil, errors.New("refresh TTL must be 7–30 days")
	}
	return &Service{Base: &platform.Base{DB: db, Log: log, Verifier: tokens}, Config: c, Tokens: tokens}, nil
}
func (s *Service) Handler() (http.Handler, error) { return s.Base.Handler(s, "users") }
