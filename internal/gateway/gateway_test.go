package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hhoshii/marketplace-architecture/internal/api"
)

func TestRoutingWithoutDuplicateValidation(t *testing.T) {
	upstream := func(name string) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := uuid.Parse(r.Header.Get("X-Request-Id")); err != nil {
				t.Error("gateway must generate a request UUID")
			}
			if r.Header.Get("Authorization") != "Bearer forwarded-as-is" {
				t.Error("authorization was not forwarded")
			}
			w.Header().Set("X-Request-Id", r.Header.Get("X-Request-Id"))
			w.Header().Set("X-Upstream", name)
			w.WriteHeader(202)
			io.Copy(w, r.Body)
		}))
		t.Cleanup(server.Close)
		return server
	}
	users, commerce := upstream("users"), upstream("commerce")
	h, err := Handler(users.URL, commerce.URL, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ path, target string }{{"/auth/login", "users"}, {"/products", "commerce"}, {"/orders?test=1", "commerce"}, {"/health", "commerce"}} {
		r := httptest.NewRequest("POST", c.path, strings.NewReader("not JSON"))
		r.Header.Set("Authorization", "Bearer forwarded-as-is")
		r.Header.Set("X-Request-Id", "client-controlled")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 202 || w.Header().Get("X-Upstream") != c.target || w.Body.String() != "not JSON" {
			t.Fatalf("gateway changed request instead of proxying: %s %d %s", c.path, w.Code, w.Body.String())
		}
		if _, err := uuid.Parse(w.Header().Get("X-Request-Id")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnavailableUpstream(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	h, err := Handler(server.URL, server.URL, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/login", nil))
	var failure api.Error
	if err = json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if w.Code != 503 || failure.ErrorCode != api.DEPENDENCYUNAVAILABLE {
		t.Fatalf("expected contract error: %d %s", w.Code, w.Body.String())
	}
	if _, err := uuid.Parse(w.Header().Get("X-Request-Id")); err != nil {
		t.Fatal(err)
	}
}
