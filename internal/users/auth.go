package users

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hhoshii/marketplace-architecture/internal/api"
	"github.com/hhoshii/marketplace-architecture/internal/platform"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

func (s *Service) issue(ctx context.Context, tx pgx.Tx, p platform.Principal) (api.TokenPair, error) {
	now := time.Now().UTC()
	access, err := s.Tokens.Sign(p.ID, p.Role, "access", s.Config.AccessTTL, uuid.NewString(), now)
	if err != nil {
		return api.TokenPair{}, err
	}
	jti := uuid.New()
	refresh, err := s.Tokens.Sign(p.ID, p.Role, "refresh", s.Config.RefreshTTL, jti.String(), now)
	if err != nil {
		return api.TokenPair{}, err
	}
	sum := sha256.Sum256([]byte(refresh))
	_, err = tx.Exec(ctx, "INSERT INTO refresh_tokens(id,user_id,token_hash,expires_at) VALUES($1,$2,$3,$4)", jti, p.ID, sum[:], now.Add(s.Config.RefreshTTL))
	return api.TokenPair{AccessToken: access, RefreshToken: refresh, TokenType: api.Bearer, ExpiresIn: int(s.Config.AccessTTL.Seconds()), UserId: p.ID, Role: p.Role}, err
}
func (s *Service) Register(w http.ResponseWriter, r *http.Request) {
	in, err := platform.Decode[api.RegisterRequest](r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	// bcrypt accepts at most 72 bytes, which differs from JSON Schema character count.
	if len([]byte(in.Password)) > 72 {
		s.Fail(w, r, platform.Validation("password", "Максимум 72 байта UTF-8"))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	role := api.RoleUSER
	if in.Role != nil {
		role = api.Role(*in.Role)
	}
	v, err := s.Transaction(r.Context(), func(tx pgx.Tx) (any, error) {
		p := platform.Principal{ID: uuid.New(), Role: role}
		_, err := tx.Exec(r.Context(), "INSERT INTO users(id,email,password_hash,role) VALUES($1,$2,$3,$4)", p.ID, strings.ToLower(string(in.Email)), string(hash), role)
		if platform.UniqueViolation(err) {
			return nil, platform.Problem(409, api.USERALREADYEXISTS, "Пользователь уже существует")
		}
		if err != nil {
			return nil, err
		}
		return s.issue(r.Context(), tx, p)
	})
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	platform.Write(w, 201, v)
}
func (s *Service) Login(w http.ResponseWriter, r *http.Request) {
	in, err := platform.Decode[api.LoginRequest](r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	var p platform.Principal
	var hash string
	err = s.DB.QueryRow(r.Context(), "SELECT id,password_hash,role FROM users WHERE email=$1", strings.ToLower(string(in.Email))).Scan(&p.ID, &hash, &p.Role)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.Fail(w, r, err)
		return
	}
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		s.Fail(w, r, platform.Problem(401, api.AUTHINVALIDCREDENTIALS, "Неверный email или пароль"))
		return
	}
	v, err := s.Transaction(r.Context(), func(tx pgx.Tx) (any, error) { return s.issue(r.Context(), tx, p) })
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	platform.Write(w, 200, v)
}
func (s *Service) Refresh(w http.ResponseWriter, r *http.Request) {
	in, err := platform.Decode[api.RefreshRequest](r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	c, err := s.Tokens.Parse(in.RefreshToken, "refresh")
	if err != nil {
		s.Fail(w, r, platform.Problem(401, api.REFRESHTOKENINVALID, "Некорректный refresh token"))
		return
	}
	sum := sha256.Sum256([]byte(in.RefreshToken))
	v, err := s.Transaction(r.Context(), func(tx pgx.Tx) (any, error) {
		var p platform.Principal
		// UPDATE locks the row. Exactly one concurrent refresh can consume it.
		err := tx.QueryRow(r.Context(), `UPDATE refresh_tokens SET revoked_at=now()
   WHERE id=$1 AND user_id=$2 AND token_hash=$3 AND revoked_at IS NULL AND expires_at>now()
   RETURNING user_id`, c.ID, c.Subject, sum[:]).Scan(&p.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, platform.Problem(401, api.REFRESHTOKENINVALID, "Refresh token истёк или уже использован")
		}
		if err != nil {
			return nil, err
		}
		if err = tx.QueryRow(r.Context(), "SELECT role FROM users WHERE id=$1", p.ID).Scan(&p.Role); err != nil {
			return nil, err
		}
		return s.issue(r.Context(), tx, p)
	})
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	platform.Write(w, 200, v)
}
func (s *Service) BootstrapAdmin(ctx context.Context, email, password string) error {
	if email == "" && password == "" {
		return nil
	}
	if !strings.Contains(email, "@") || len(password) < 10 || len(password) > 72 {
		return errors.New("invalid bootstrap admin credentials")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	// An existing email is never silently promoted or overwritten.
	_, err = s.DB.Exec(ctx, "INSERT INTO users(email,password_hash,role) VALUES($1,$2,'ADMIN') ON CONFLICT(email) DO NOTHING", strings.ToLower(email), string(hash))
	return err
}
