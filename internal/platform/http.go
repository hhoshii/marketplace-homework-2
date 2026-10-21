package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hhoshii/marketplace-architecture/internal/api"
)

type requestMeta struct{ UserID any }
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}

func (s *Base) Handler(server api.ServerInterface, group string) (http.Handler, error) {
	mux := chi.NewRouter()
	mux.NotFound(func(w http.ResponseWriter, r *http.Request) {
		s.Fail(w, r, Problem(404, api.NOTFOUND, "Путь не найден"))
	})
	mux.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		s.Fail(w, r, Problem(405, api.METHODNOTALLOWED, "Метод не поддерживается"))
	})
	handler := api.HandlerWithOptions(server, api.ChiServerOptions{BaseRouter: mux, ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
		s.Fail(w, r, Validation("parameters", "Некорректные параметры"))
	}})
	return s.Wrap(handler, group)
}

func (s *Base) Wrap(next http.Handler, group string) (http.Handler, error) {
	spec, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	if err = spec.Validate(context.Background()); err != nil {
		return nil, err
	}
	for path := range spec.Paths.Map() {
		userPath := strings.HasPrefix(path, "/auth/")
		if path != "/health" && ((group == "users" && !userPath) || (group == "commerce" && userPath)) {
			spec.Paths.Delete(path)
		}
	}
	validator, err := legacy.NewRouter(spec)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now().UTC()
		requestID := uuid.NewString()
		sw := &statusWriter{ResponseWriter: w}
		sw.Header().Set("X-Request-Id", requestID)
		sw.Header().Set("X-Content-Type-Options", "nosniff")
		sw.Header().Set("Cache-Control", "no-store")
		meta := &requestMeta{}
		if incoming, err := uuid.Parse(r.Header.Get("X-Request-Id")); err == nil {
			requestID = incoming.String()
			sw.Header().Set("X-Request-Id", requestID)
		}
		var loggedBody any
		defer func() {
			if recover() != nil {
				s.Fail(sw, r, Problem(500, api.INTERNALERROR, "Внутренняя ошибка"))
			}
			if sw.status == 0 {
				sw.status = 200
			}
			attrs := []any{"request_id", requestID, "method", r.Method, "endpoint", r.URL.Path, "status_code", sw.status, "duration_ms", float64(time.Since(start).Microseconds()) / 1000, "user_id", meta.UserID, "timestamp", start.Format(time.RFC3339Nano)}
			if r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" {
				attrs = append(attrs, "request_body", loggedBody)
			}
			s.Log.InfoContext(r.Context(), "api_request", attrs...)
		}()
		r.Body = http.MaxBytesReader(sw, r.Body, 65536)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			s.Fail(sw, r, Validation("body", "Максимальный размер 65536 байт"))
			return
		}
		if len(body) > 0 {
			if json.Unmarshal(body, &loggedBody) != nil {
				loggedBody = "[invalid JSON]"
			} else {
				loggedBody = redact(loggedBody)
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if _, _, err := validator.FindRoute(r); err != nil {
			status, code := 404, api.NOTFOUND
			if errors.Is(err, routers.ErrMethodNotAllowed) {
				status, code = 405, api.METHODNOTALLOWED
			}
			s.Fail(sw, r, Problem(status, code, "Маршрут не найден"))
			return
		}
		public := (r.Method == "POST" && (r.URL.Path == "/auth/login" || r.URL.Path == "/auth/register" || r.URL.Path == "/auth/refresh")) || (r.Method == "GET" && r.URL.Path == "/health")
		if !public {
			p, err := s.authenticate(r)
			if err != nil {
				s.Fail(sw, r, err)
				return
			}
			meta.UserID = p.ID.String()
			r = r.WithContext(context.WithValue(r.Context(), authKey{}, p))
		}
		if len(body) > 0 && !json.Valid(body) {
			s.Fail(sw, r, Validation("body", "Требуется один корректный JSON-документ"))
			return
		}
		route, pathParams, err := validator.FindRoute(r)
		if err != nil {
			status, code := 404, api.NOTFOUND
			if errors.Is(err, routers.ErrMethodNotAllowed) {
				status, code = 405, api.METHODNOTALLOWED
			}
			s.Fail(sw, r, Problem(status, code, "Маршрут не найден"))
			return
		}
		input := &openapi3filter.RequestValidationInput{Request: r, PathParams: pathParams, Route: route, Options: &openapi3filter.Options{MultiError: true, AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if err = openapi3filter.ValidateRequest(r.Context(), input); err != nil {
			f := Problem(400, api.VALIDATIONERROR, "Некорректные входные данные")
			violations := []map[string]string{}
			collectViolations(err, &violations)
			if len(violations) == 0 {
				violations = append(violations, map[string]string{"field": "body", "message": "Нарушен контракт запроса"})
			}
			f.Details = map[string]interface{}{"violations": violations}
			s.Fail(sw, r, f)
			return
		}
		next.ServeHTTP(sw, r)
	}), nil
}
func collectViolations(err error, out *[]map[string]string) {
	switch e := err.(type) {
	case openapi3.MultiError:
		for _, sub := range e {
			collectViolations(sub, out)
		}
	case *openapi3filter.RequestError:
		if e.Parameter != nil {
			*out = append(*out, map[string]string{"field": e.Parameter.In + "." + e.Parameter.Name, "message": "Нарушен формат или ограничение параметра"})
		} else {
			collectViolations(e.Err, out)
		}
	case *openapi3.SchemaError:
		field := "/" + strings.Join(e.JSONPointer(), "/")
		*out = append(*out, map[string]string{"field": field, "message": e.Reason})
	default:
		if e := errors.Unwrap(err); e != nil {
			collectViolations(e, out)
		}
	}
}
func redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, value := range x {
			key := strings.ToLower(k)
			if strings.Contains(key, "password") || strings.Contains(key, "token") || strings.Contains(key, "secret") || key == "authorization" {
				x[k] = "[REDACTED]"
			} else {
				x[k] = redact(value)
			}
		}
	case []any:
		for i := range x {
			x[i] = redact(x[i])
		}
	}
	return v
}
