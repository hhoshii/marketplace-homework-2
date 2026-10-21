package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hhoshii/marketplace-architecture/internal/api"
	"github.com/hhoshii/marketplace-architecture/internal/security"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

func init() { decimal.MarshalJSONWithoutQuotes = true }

type Base struct {
	api.Unimplemented
	DB       *pgxpool.Pool
	Verifier *security.Tokens
	Log      *slog.Logger
}
type Fault struct {
	Status  int
	Code    api.ErrorErrorCode
	Message string
	Details map[string]interface{}
}

func (e *Fault) Error() string { return string(e.Code) }
func Problem(status int, code api.ErrorErrorCode, message string) *Fault {
	return &Fault{Status: status, Code: code, Message: message}
}
func Validation(field, message string) *Fault {
	e := Problem(400, api.VALIDATIONERROR, "Некорректные входные данные")
	e.Details = map[string]interface{}{"violations": []map[string]string{{"field": field, "message": message}}}
	return e
}
func Write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func (s *Base) Fail(w http.ResponseWriter, r *http.Request, err error) {
	var f *Fault
	if !errors.As(err, &f) {
		s.Log.ErrorContext(r.Context(), "request_failed", "request_id", w.Header().Get("X-Request-Id"), "error_type", fmt.Sprintf("%T", err))
		f = Problem(500, api.INTERNALERROR, "Внутренняя ошибка")
	}
	response := api.Error{ErrorCode: f.Code, Message: f.Message}
	if f.Details != nil {
		response.Details = &f.Details
	}
	Write(w, f.Status, response)
}
func Decode[T any](r *http.Request) (T, error) {
	var v T
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return v, Validation("body", "Некорректный JSON")
	}
	return v, nil
}
func (s *Base) Transaction(ctx context.Context, fn func(pgx.Tx) (any, error)) (any, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	v, err := fn(tx)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return v, nil
}
func (s *Base) Health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.DB.Ping(ctx); err != nil {
		s.Fail(w, r, err)
		return
	}
	Write(w, 200, api.Health{Status: "ok"})
}

type Principal struct {
	ID   uuid.UUID
	Role api.Role
}
type authKey struct{}

func Who(r *http.Request) Principal { return r.Context().Value(authKey{}).(Principal) }
func Permitted(p Principal, roles ...api.Role) bool {
	for _, role := range roles {
		if p.Role == role {
			return true
		}
	}
	return false
}
func Deny() error { return Problem(403, api.ACCESSDENIED, "Недостаточно прав") }

func UniqueViolation(err error) bool {
	var e *pgconn.PgError
	return errors.As(err, &e) && e.Code == "23505"
}
func WithPrincipal(r *http.Request, p Principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), authKey{}, p))
}
