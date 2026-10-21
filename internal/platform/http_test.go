package platform

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hhoshii/marketplace-architecture/internal/api"
	"github.com/hhoshii/marketplace-architecture/internal/security"
)

func testApp(t *testing.T) *Base {
	t.Helper()
	signer, err := security.NewSigner(strings.Repeat("00", 32))
	if err != nil {
		t.Fatal(err)
	}
	return &Base{Verifier: signer, Log: slog.New(slog.NewJSONHandler(io.Discard, nil))}
}
func TestContractRejectsBeforeDatabase(t *testing.T) {
	a := testApp(t)
	h, err := a.Handler(a, "all")
	if err != nil {
		t.Fatal(err)
	}
	// A valid access token is sufficient: invalid requests must never touch the nil DB.
	token, err := a.Verifier.Sign(uuid.New(), api.RoleSELLER, "access", time.Hour, uuid.NewString(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/products", `{"name":"","price":0,"stock":-1,"category":"","status":"BAD"}`},
		{"POST", "/products", `{"name":"ok","price":1.001,"stock":1,"category":"a","status":"ACTIVE"}`},
		{"POST", "/products", `{"name":"ok","price":"1.00","stock":1,"category":"a","status":"ACTIVE"}`},
		{"POST", "/products", `{"name":"ok","price":1,"stock":1,"category":"a","status":"ACTIVE","extra":true}`},
		{"POST", "/products", `{"name":"ok","price":1,"stock":1,"category":"a","status":"ACTIVE","seller_id":"00000000-0000-0000-0000-000000000001"}`},
		{"GET", "/products?page=-1", ""},
		{"GET", "/products?size=101", ""},
		{"GET", "/products/not-a-uuid", ""},
		{"POST", "/orders", `{"items":[]}`},
		{"POST", "/orders", `{"items":[{"product_id":"bad","quantity":0}]}`},
		{"POST", "/orders", `{"items":[{"product_id":"00000000-0000-0000-0000-000000000001","quantity":1}],"promo_code":"bad"}`},
		{"POST", "/products", "{"},
		{"POST", "/products", `{"name":"ok","price":1,"stock":1,"category":"a","status":"ACTIVE"}{}`},
		{"PUT", "/products/00000000-0000-0000-0000-000000000001", `{"name":"partial"}`},
	} {
		t.Run(c.path+c.body, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 400 {
				t.Fatalf("want 400 got %d %s", w.Code, w.Body.String())
			}
			var e api.Error
			if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			if e.ErrorCode != api.VALIDATIONERROR || e.Details == nil {
				t.Fatalf("invalid contract error: %+v", e)
			}
		})
	}
}
func TestTokenTypesExpiryAndSignature(t *testing.T) {
	a := testApp(t)
	for _, c := range []struct {
		kind, role   string
		expired, bad bool
		code         api.ErrorErrorCode
	}{
		{"refresh", "USER", false, false, api.TOKENINVALID}, {"access", "ADMIN", true, false, api.TOKENEXPIRED}, {"access", "USER", false, true, api.TOKENINVALID}, {"access", "ROOT", false, false, api.TOKENINVALID},
	} {
		expiry := time.Now().Add(time.Hour)
		if c.expired {
			expiry = time.Now().Add(-time.Hour)
		}
		signer := a.Verifier
		if c.bad {
			signer, _ = security.NewSigner(strings.Repeat("01", 32))
		}
		raw, _ := signer.Sign(uuid.New(), api.Role(c.role), c.kind, time.Until(expiry), uuid.NewString(), time.Now())
		r := httptest.NewRequest("GET", "/products", nil)
		r.Header.Set("Authorization", "Bearer "+raw)
		_, err := a.authenticate(r)
		f, ok := err.(*Fault)
		if !ok || f.Code != c.code {
			t.Fatalf("%+v: %v", c, err)
		}
	}
}
func TestJSONLogging(t *testing.T) {
	var log bytes.Buffer
	a := testApp(t)
	a.Log = slog.New(slog.NewJSONHandler(&log, nil))
	h, err := a.Handler(a, "all")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/auth/register", strings.NewReader(`{"password":"never-log-me","refresh_token":"hidden","nested":{"access_token":"also-hidden"}}`))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	var line map[string]any
	if err = json.Unmarshal(log.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"request_id", "method", "endpoint", "status_code", "duration_ms", "user_id", "timestamp", "request_body"} {
		if _, ok := line[field]; !ok {
			t.Errorf("missing %s", field)
		}
	}
	if strings.Contains(log.String(), "never-log-me") || strings.Contains(log.String(), "also-hidden") {
		t.Fatal("secret leaked")
	}
	if line["request_id"] != w.Header().Get("X-Request-Id") {
		t.Fatal("request id mismatch")
	}
	if _, err = uuid.Parse(w.Header().Get("X-Request-Id")); err != nil {
		t.Fatal(err)
	}
	if line["user_id"] != nil {
		t.Fatal("anonymous user should be null")
	}
}
func TestBodyLimit(t *testing.T) {
	a := testApp(t)
	h, _ := a.Handler(a, "all")
	r := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(strings.Repeat("x", 65537)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
