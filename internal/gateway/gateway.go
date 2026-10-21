package gateway

import (
	"errors"
	"github.com/google/uuid"
	"github.com/hhoshii/marketplace-architecture/internal/api"
	"github.com/hhoshii/marketplace-architecture/internal/platform"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// Handler only routes requests. Authentication and validation belong to the services.
func Handler(usersURL, commerceURL string, log *slog.Logger) (http.Handler, error) {
	users, err := url.Parse(usersURL)
	if err != nil || users.Host == "" || (users.Scheme != "http" && users.Scheme != "https") {
		return nil, errors.New("invalid USER_SERVICE_URL")
	}
	commerce, err := url.Parse(commerceURL)
	if err != nil || commerce.Host == "" || (commerce.Scheme != "http" && commerce.Scheme != "https") {
		return nil, errors.New("invalid COMMERCE_SERVICE_URL")
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			target := commerce
			if strings.HasPrefix(r.In.URL.Path, "/auth/") {
				target = users
			}
			r.SetURL(target)
			r.Out.Header.Set("X-Request-Id", r.In.Header.Get("X-Request-Id"))
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("X-Request-Id", r.Header.Get("X-Request-Id"))
			log.ErrorContext(r.Context(), "upstream_unavailable", "request_id", r.Header.Get("X-Request-Id"), "method", r.Method, "endpoint", r.URL.Path, "status_code", 503)
			platform.Write(w, 503, api.Error{ErrorCode: api.DEPENDENCYUNAVAILABLE, Message: "Сервис временно недоступен"})
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Request-Id", uuid.NewString())
		proxy.ServeHTTP(w, r)
	}), nil
}
