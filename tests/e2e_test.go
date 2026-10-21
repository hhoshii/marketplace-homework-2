//go:build integration

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/hhoshii/marketplace-architecture/internal/api"
	"github.com/hhoshii/marketplace-architecture/internal/users"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type suite struct {
	t                    *testing.T
	db                   *pgxpool.Pool
	usersDB              *pgxpool.Pool
	baseURL              string
	client               *http.Client
	usersURL             string
	commerceURL          string
	router               routers.Router
	seller, buyer, admin api.TokenPair
}

func setup(t *testing.T) *suite {
	t.Helper()
	for _, key := range []string{"TEST_BASE_URL", "TEST_USERS_SERVICE_URL", "TEST_COMMERCE_SERVICE_URL", "TEST_USERS_DATABASE_URL", "TEST_COMMERCE_DATABASE_URL"} {
		if os.Getenv(key) == "" {
			t.Fatalf("%s is required; run make e2e against Docker Compose", key)
		}
	}
	dsn := os.Getenv("TEST_COMMERCE_DATABASE_URL")
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	udsn := os.Getenv("TEST_USERS_DATABASE_URL")
	usersDB, err := pgxpool.New(context.Background(), udsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(usersDB.Close)
	a, err := users.New(usersDB, users.Config{PrivateKey: strings.Repeat("00", 32)}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := api.GetSwagger()
	router, err := legacy.NewRouter(spec)
	if err != nil {
		t.Fatal(err)
	}
	s := &suite{t: t, db: db, usersDB: usersDB, baseURL: os.Getenv("TEST_BASE_URL"), client: &http.Client{Timeout: 15 * time.Second}, usersURL: os.Getenv("TEST_USERS_SERVICE_URL"), commerceURL: os.Getenv("TEST_COMMERCE_SERVICE_URL"), router: router}
	s.initialize(a)
	return s
}
func (s *suite) initialize(a *users.Service) {
	t := s.t
	s.seller = s.register("SELLER")
	s.buyer = s.register("USER")
	email := uuid.NewString() + "@example.test"
	if err := a.BootstrapAdmin(context.Background(), email, "integration-admin-password"); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(s.call("POST", "/auth/login", "", map[string]any{"email": email, "password": "integration-admin-password"}, 200, ""), &s.admin)
}
func (s *suite) call(method, path, token string, data any, status int, code string) []byte {
	s.t.Helper()
	var body io.Reader
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			s.t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, s.baseURL+path, body)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	if resp.StatusCode != status {
		s.t.Fatalf("%s %s: want %d got %d: %s", method, path, status, resp.StatusCode, raw)
	}
	if _, err = uuid.Parse(resp.Header.Get("X-Request-Id")); err != nil {
		s.t.Fatal("missing request id")
	}
	if code != "" {
		var e api.Error
		json.Unmarshal(raw, &e)
		if string(e.ErrorCode) != code {
			s.t.Fatalf("want %s got %s", code, raw)
		}
	}
	route, params, err := s.router.FindRoute(req)
	if err == nil {
		input := &openapi3filter.ResponseValidationInput{RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, Route: route, PathParams: params}, Status: status, Header: resp.Header, Body: io.NopCloser(bytes.NewReader(raw))}
		if err = openapi3filter.ValidateResponse(context.Background(), input); err != nil {
			s.t.Fatalf("response violates OpenAPI: %v", err)
		}
	}
	return raw
}
func (s *suite) register(role string) api.TokenPair {
	var pair api.TokenPair
	json.Unmarshal(s.call("POST", "/auth/register", "", map[string]any{"email": uuid.NewString() + "@example.test", "password": "integration-password", "role": role}, 201, ""), &pair)
	return pair
}
func (s *suite) product(price float64, stock int, status string) api.ProductResponse {
	var p api.ProductResponse
	json.Unmarshal(s.call("POST", "/products", s.seller.AccessToken, map[string]any{"name": "Тест", "price": price, "stock": stock, "category": "exact", "status": status}, 201, ""), &p)
	return p
}
func items(p api.ProductResponse, n int) map[string]any {
	return map[string]any{"items": []map[string]any{{"product_id": p.Id, "quantity": n}}}
}
func (s *suite) order(p api.ProductResponse, n int, promo string) api.OrderResponse {
	data := items(p, n)
	if promo != "" {
		data["promo_code"] = promo
	}
	var o api.OrderResponse
	json.Unmarshal(s.call("POST", "/orders", s.buyer.AccessToken, data, 201, ""), &o)
	return o
}
func (s *suite) stock(p api.ProductResponse, want int) {
	s.t.Helper()
	var got int
	if err := s.db.QueryRow(context.Background(), "SELECT stock FROM products WHERE id=$1", p.Id).Scan(&got); err != nil {
		s.t.Fatal(err)
	}
	if got != want {
		s.t.Fatalf("stock: want %d got %d", want, got)
	}
}
func (s *suite) count(query string, args ...any) int {
	s.t.Helper()
	var n int
	if err := s.db.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}
func (s *suite) exec(query string, args ...any) {
	s.t.Helper()
	if _, err := s.db.Exec(context.Background(), query, args...); err != nil {
		s.t.Fatal(err)
	}
}
func (s *suite) age(user uuid.UUID) {
	s.exec("UPDATE user_operations SET created_at=now()-interval '2 minutes' WHERE user_id=$1", user)
}
func (s *suite) createPromo(value, min float64, max int, kind string) api.PromoResponse {
	var promo api.PromoResponse
	code := "P_" + strings.ToUpper(uuid.NewString()[:8])
	json.Unmarshal(s.call("POST", "/promo-codes", s.seller.AccessToken, map[string]any{"code": code, "discount_type": kind, "discount_value": value, "min_order_amount": min, "max_uses": max, "valid_from": time.Now().Add(-time.Hour).UTC(), "valid_until": time.Now().Add(time.Hour).UTC()}, 201, ""), &promo)
	return promo
}
func TestProductCRUDAndRBAC(t *testing.T) {
	s := setup(t)
	p := s.product(12.34, 8, "ACTIVE")
	s.call("GET", "/products/"+p.Id.String(), "", nil, 401, "TOKEN_INVALID")
	for _, pair := range []api.TokenPair{s.buyer, s.seller, s.admin} {
		s.call("GET", "/products/"+p.Id.String(), pair.AccessToken, nil, 200, "")
	}
	s.call("POST", "/products", s.buyer.AccessToken, map[string]any{"name": "x", "price": 1, "stock": 1, "category": "x", "status": "ACTIVE"}, 403, "ACCESS_DENIED")
	in := map[string]any{"name": "Обновлён", "description": nil, "price": 15.25, "stock": 9, "category": "exact", "status": "INACTIVE"}
	other := s.register("SELLER")
	s.call("PUT", "/products/"+p.Id.String(), other.AccessToken, in, 403, "ACCESS_DENIED")
	s.call("DELETE", "/products/"+p.Id.String(), other.AccessToken, nil, 403, "ACCESS_DENIED")
	var updated api.ProductResponse
	json.Unmarshal(s.call("PUT", "/products/"+p.Id.String(), s.admin.AccessToken, in, 200, ""), &updated)
	if !updated.CreatedAt.Equal(p.CreatedAt) || !updated.UpdatedAt.After(p.UpdatedAt) {
		t.Fatal("automatic timestamps failed")
	}
	var page api.ProductPage
	json.Unmarshal(s.call("GET", "/products?status=INACTIVE&category=exact&page=0&size=1", s.buyer.AccessToken, nil, 200, ""), &page)
	if len(page.Content) != 1 || page.TotalElements < 1 || page.Size != 1 {
		t.Fatalf("bad pagination %+v", page)
	}
	s.call("GET", "/products?category=EXACT", s.buyer.AccessToken, nil, 200, "")
	s.call("DELETE", "/products/"+p.Id.String(), s.seller.AccessToken, nil, 204, "")
	s.call("DELETE", "/products/"+p.Id.String(), s.seller.AccessToken, nil, 204, "")
	if s.count("SELECT count(*) FROM products WHERE id=$1 AND status='ARCHIVED'", p.Id) != 1 {
		t.Fatal("not soft deleted")
	}
	s.call("GET", "/products/"+uuid.NewString(), s.buyer.AccessToken, nil, 404, "PRODUCT_NOT_FOUND")
}
func TestOrderTransactionsSnapshotsAndStates(t *testing.T) {
	s := setup(t)
	p := s.product(10.01, 10, "ACTIVE")
	promo := s.createPromo(10, 20, 1, "PERCENTAGE")
	o := s.order(p, 3, promo.Code)
	s.stock(p, 7)
	if o.TotalAmount.String() != "27.03" || o.DiscountAmount.String() != "3" {
		t.Fatalf("bad discount %+v", o)
	}
	if s.count("SELECT current_uses FROM promo_codes WHERE id=$1", promo.Id) != 1 {
		t.Fatal("promo not consumed")
	}
	// Product price changes must not change the existing snapshot.
	s.exec("UPDATE products SET price=20 WHERE id=$1", p.Id)
	var stored api.OrderResponse
	json.Unmarshal(s.call("GET", "/orders/"+o.Id.String(), s.buyer.AccessToken, nil, 200, ""), &stored)
	if stored.Items[0].PriceAtOrder.String() != "10.01" {
		t.Fatal("snapshot changed")
	}
	s.call("POST", "/orders", s.buyer.AccessToken, items(p, 1), 429, "ORDER_LIMIT_EXCEEDED")
	s.age(s.buyer.UserId)
	s.call("POST", "/orders", s.buyer.AccessToken, items(p, 1), 409, "ORDER_HAS_ACTIVE")
	stranger := s.register("USER")
	for _, operation := range []struct {
		method, path string
		data         any
	}{{"GET", "/orders/" + o.Id.String(), nil}, {"PUT", "/orders/" + o.Id.String(), items(p, 1)}, {"POST", "/orders/" + o.Id.String() + "/cancel", nil}} {
		s.call(operation.method, operation.path, stranger.AccessToken, operation.data, 403, "ORDER_OWNERSHIP_VIOLATION")
	}
	s.call("GET", "/orders/"+o.Id.String(), s.seller.AccessToken, nil, 403, "ACCESS_DENIED")
	s.call("PUT", "/orders/"+o.Id.String(), s.buyer.AccessToken, items(p, 999), 409, "INSUFFICIENT_STOCK")
	s.stock(p, 7)
	if s.count("SELECT quantity FROM order_items WHERE order_id=$1", o.Id) != 3 {
		t.Fatal("failed update changed items")
	}
	// Existing promo use remains valid even at its maximum.
	json.Unmarshal(s.call("PUT", "/orders/"+o.Id.String(), s.buyer.AccessToken, items(p, 2), 200, ""), &stored)
	if stored.TotalAmount.String() != "36" || stored.DiscountAmount.String() != "4" {
		t.Fatal("promo recalc failed")
	}
	s.call("PUT", "/orders/"+o.Id.String(), s.buyer.AccessToken, items(p, 1), 429, "ORDER_LIMIT_EXCEEDED")
	s.age(s.buyer.UserId)
	json.Unmarshal(s.call("PUT", "/orders/"+o.Id.String(), s.admin.AccessToken, items(p, 1), 200, ""), &stored)
	s.stock(p, 9) // total 20 still meets minimum
	s.exec("UPDATE promo_codes SET min_order_amount=25 WHERE id=$1", promo.Id)
	s.age(s.buyer.UserId)
	json.Unmarshal(s.call("PUT", "/orders/"+o.Id.String(), s.buyer.AccessToken, items(p, 1), 200, ""), &stored)
	if stored.PromoCodeId != nil || !stored.DiscountAmount.IsZero() {
		t.Fatal("inapplicable promo retained")
	}
	if s.count("SELECT current_uses FROM promo_codes WHERE id=$1", promo.Id) != 0 {
		t.Fatal("promo use not returned")
	}
	s.call("POST", "/orders/"+o.Id.String()+"/status", s.buyer.AccessToken, map[string]any{"status": "PAID"}, 403, "ACCESS_DENIED")
	s.call("POST", "/orders/"+o.Id.String()+"/status", s.admin.AccessToken, map[string]any{"status": "PAID"}, 409, "INVALID_STATE_TRANSITION")
	s.call("POST", "/orders/"+o.Id.String()+"/status", s.admin.AccessToken, map[string]any{"status": "PAYMENT_PENDING"}, 200, "")
	s.call("PUT", "/orders/"+o.Id.String(), s.buyer.AccessToken, items(p, 2), 409, "INVALID_STATE_TRANSITION")
	s.call("POST", "/orders/"+o.Id.String()+"/cancel", s.buyer.AccessToken, nil, 200, "")
	s.stock(p, 10)
	s.call("POST", "/orders/"+o.Id.String()+"/cancel", s.buyer.AccessToken, nil, 409, "INVALID_STATE_TRANSITION")
	s.stock(p, 10)
	s.call("GET", "/orders/"+uuid.NewString(), s.buyer.AccessToken, nil, 404, "ORDER_NOT_FOUND")
}
func TestRollbackAndPromoBoundaries(t *testing.T) {
	s := setup(t)
	p := s.product(10, 10, "ACTIVE")
	inactive := s.product(10, 5, "INACTIVE")
	s.call("POST", "/orders", s.buyer.AccessToken, items(inactive, 1), 409, "PRODUCT_INACTIVE")
	missing := items(p, 1)
	missing["items"] = []map[string]any{{"product_id": uuid.NewString(), "quantity": 1}}
	s.call("POST", "/orders", s.buyer.AccessToken, missing, 404, "PRODUCT_NOT_FOUND")
	data := items(p, 2)
	data["promo_code"] = "MISSING"
	s.call("POST", "/orders", s.buyer.AccessToken, data, 422, "PROMO_CODE_INVALID")
	s.stock(p, 10)
	promo := s.createPromo(100, 30, 1, "FIXED_AMOUNT")
	data["promo_code"] = promo.Code
	s.call("POST", "/orders", s.buyer.AccessToken, data, 422, "PROMO_CODE_MIN_AMOUNT")
	s.stock(p, 10)
	for _, query := range []string{"UPDATE promo_codes SET active=false WHERE id=$1", "UPDATE promo_codes SET active=true,valid_until=now()-interval '1 minute',valid_from=now()-interval '1 hour' WHERE id=$1", "UPDATE promo_codes SET valid_until=now()+interval '1 hour',current_uses=max_uses WHERE id=$1"} {
		s.exec(query, promo.Id)
		s.call("POST", "/orders", s.buyer.AccessToken, data, 422, "PROMO_CODE_INVALID")
		s.stock(p, 10)
	}
	if s.count("SELECT count(*) FROM orders WHERE user_id=$1", s.buyer.UserId) != 0 || s.count("SELECT count(*) FROM user_operations WHERE user_id=$1", s.buyer.UserId) != 0 {
		t.Fatal("failed create committed")
	}
	s.exec("UPDATE promo_codes SET current_uses=0,min_order_amount=0 WHERE id=$1", promo.Id)
	o := s.order(p, 2, promo.Code)
	if !o.TotalAmount.IsZero() {
		t.Fatal("fixed discount should be capped")
	}
	s.call("POST", "/orders/"+o.Id.String()+"/cancel", s.buyer.AccessToken, nil, 200, "")
	if s.count("SELECT current_uses FROM promo_codes WHERE id=$1", promo.Id) != 0 {
		t.Fatal("cancel did not release promo")
	}
	s.age(s.buyer.UserId)
	high := s.createPromo(70.01, 0, 1, "PERCENTAGE")
	o = s.order(p, 2, high.Code)
	if !o.DiscountAmount.IsZero() || o.TotalAmount.String() != "20" {
		t.Fatal("70% rule failed")
	}
}
func TestPaidLifecycle(t *testing.T) {
	s := setup(t)
	p := s.product(1, 2, "ACTIVE")
	o := s.order(p, 1, "")
	for _, status := range []string{"PAYMENT_PENDING", "PAID", "SHIPPED", "COMPLETED"} {
		s.call("POST", "/orders/"+o.Id.String()+"/status", s.admin.AccessToken, map[string]any{"status": status}, 200, "")
		if status == "PAID" {
			s.call("POST", "/orders/"+o.Id.String()+"/cancel", s.buyer.AccessToken, nil, 409, "INVALID_STATE_TRANSITION")
		}
	}
	s.call("POST", "/orders/"+o.Id.String()+"/status", s.admin.AccessToken, map[string]any{"status": "CREATED"}, 409, "INVALID_STATE_TRANSITION")
	s.stock(p, 1)
}
func TestAuthAndRefresh(t *testing.T) {
	s := setup(t)
	s.call("POST", "/auth/register", "", map[string]any{"email": "root@example.test", "password": "integration-password", "role": "ADMIN"}, 400, "VALIDATION_ERROR")
	s.call("POST", "/auth/login", "", map[string]any{"email": "missing@example.test", "password": "wrong"}, 401, "AUTH_INVALID_CREDENTIALS")
	s.call("GET", "/products", s.buyer.RefreshToken, nil, 401, "TOKEN_INVALID")
	s.call("POST", "/auth/refresh", "", map[string]any{"refresh_token": s.buyer.AccessToken}, 401, "REFRESH_TOKEN_INVALID")
	var pair api.TokenPair
	json.Unmarshal(s.call("POST", "/auth/refresh", "", map[string]any{"refresh_token": s.buyer.RefreshToken}, 200, ""), &pair)
	s.call("POST", "/auth/refresh", "", map[string]any{"refresh_token": s.buyer.RefreshToken}, 401, "REFRESH_TOKEN_INVALID")
	s.call("GET", "/products", pair.AccessToken, nil, 200, "")
	if _, err := s.usersDB.Exec(context.Background(), "UPDATE refresh_tokens SET expires_at=now()-interval '1 minute' WHERE user_id=$1", pair.UserId); err != nil {
		t.Fatal(err)
	}
	s.call("POST", "/auth/refresh", "", map[string]any{"refresh_token": pair.RefreshToken}, 401, "REFRESH_TOKEN_INVALID")
	s.call("POST", "/auth/refresh", "", map[string]any{"refresh_token": "bad"}, 401, "REFRESH_TOKEN_INVALID")
}
func TestValidationBounds(t *testing.T) {
	s := setup(t)
	p := s.product(1, 10, "ACTIVE")
	for _, n := range []int{0, 1000} {
		s.call("POST", "/orders", s.buyer.AccessToken, items(p, n), 400, "VALIDATION_ERROR")
	}
	duplicate := items(p, 1)
	duplicate["items"] = []map[string]any{{"product_id": p.Id, "quantity": 1}, {"product_id": p.Id, "quantity": 1}}
	s.call("POST", "/orders", s.buyer.AccessToken, duplicate, 400, "VALIDATION_ERROR")
	many := make([]map[string]any, 51)
	for i := range many {
		many[i] = map[string]any{"product_id": p.Id, "quantity": 1}
	}
	s.call("POST", "/orders", s.buyer.AccessToken, map[string]any{"items": many}, 400, "VALIDATION_ERROR")
	for _, change := range []struct {
		field string
		v     any
	}{{"name", ""}, {"name", strings.Repeat("я", 256)}, {"description", strings.Repeat("x", 4001)}, {"price", 0}, {"price", 0.001}, {"stock", -1}, {"category", ""}, {"category", strings.Repeat("x", 101)}, {"status", "BAD"}} {
		data := map[string]any{"name": "ok", "price": 1, "stock": 1, "category": "a", "status": "ACTIVE"}
		data[change.field] = change.v
		s.call("POST", "/products", s.seller.AccessToken, data, 400, "VALIDATION_ERROR")
	}
	s.stock(p, 10)
}

// Concurrent calls use plain HTTP and return only status codes to the test goroutine.
func (s *suite) race(path string, tokens []string, bodies []any) []int {
	out := make([]int, len(tokens))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range tokens {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			raw, _ := json.Marshal(bodies[i])
			r, _ := http.NewRequest("POST", s.baseURL+path, bytes.NewReader(raw))
			r.Header.Set("Content-Type", "application/json")
			if tokens[i] != "" {
				r.Header.Set("Authorization", "Bearer "+tokens[i])
			}
			response, err := s.client.Do(r)
			if err != nil {
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			out[i] = response.StatusCode
		}(i)
	}
	close(start)
	wg.Wait()
	return out
}
func assertStatuses(t *testing.T, got []int, want map[int]int) {
	t.Helper()
	counts := map[int]int{}
	for _, n := range got {
		counts[n]++
	}
	if fmt.Sprint(counts) != fmt.Sprint(want) {
		t.Fatalf("statuses %v, want %v", counts, want)
	}
}
func TestConcurrentStockPromoActiveAndRefresh(t *testing.T) {
	s := setup(t)
	p := s.product(10, 1, "ACTIVE")
	other := s.register("USER")
	codes := s.race("/orders", []string{s.buyer.AccessToken, other.AccessToken}, []any{items(p, 1), items(p, 1)})
	assertStatuses(t, codes, map[int]int{201: 1, 409: 1})
	s.stock(p, 0)
	p2 := s.product(10, 10, "ACTIVE")
	promo := s.createPromo(10, 0, 1, "PERCENTAGE")
	a, b := s.register("USER"), s.register("USER")
	d := items(p2, 1)
	d["promo_code"] = promo.Code
	codes = s.race("/orders", []string{a.AccessToken, b.AccessToken}, []any{d, d})
	assertStatuses(t, codes, map[int]int{201: 1, 422: 1})
	s.stock(p2, 9)
	if s.count("SELECT current_uses FROM promo_codes WHERE id=$1", promo.Id) != 1 {
		t.Fatal("oversubscribed promo")
	}
	single := s.register("USER")
	codes = s.race("/orders", []string{single.AccessToken, single.AccessToken}, []any{items(p2, 1), items(p2, 1)})
	assertStatuses(t, codes, map[int]int{201: 1, 429: 1})
	if s.count("SELECT count(*) FROM orders WHERE user_id=$1", single.UserId) != 1 {
		t.Fatal("duplicate active orders")
	}
	body := map[string]any{"refresh_token": single.RefreshToken}
	codes = s.race("/auth/refresh", []string{"", ""}, []any{body, body})
	assertStatuses(t, codes, map[int]int{200: 1, 401: 1})
}

func TestConcurrentUpdateAndCancel(t *testing.T) {
	s := setup(t)
	p := s.product(2.50, 10, "ACTIVE")
	o := s.order(p, 2, "")
	path := "/orders/" + o.Id.String()
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			raw, _ := json.Marshal(items(p, 3))
			r, _ := http.NewRequest("PUT", s.baseURL+path, bytes.NewReader(raw))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+s.buyer.AccessToken)
			response, err := s.client.Do(r)
			if err != nil {
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			codes[i] = response.StatusCode
		}(i)
	}
	close(start)
	wg.Wait()
	assertStatuses(t, codes, map[int]int{200: 1, 429: 1})
	s.stock(p, 7)
	codes = s.race(path+"/cancel", []string{s.buyer.AccessToken, s.buyer.AccessToken}, []any{nil, nil})
	assertStatuses(t, codes, map[int]int{200: 1, 409: 1})
	s.stock(p, 10)
}

func TestServiceBoundaries(t *testing.T) {
	s := setup(t)
	// Shared PostgreSQL instance must not grant access to the other service's DB.
	for _, check := range []struct{ key, otherDB string }{{"TEST_USERS_DATABASE_URL", "commerce"}, {"TEST_COMMERCE_DATABASE_URL", "users"}} {
		config, err := pgxpool.ParseConfig(os.Getenv(check.key))
		if err != nil {
			t.Fatal(err)
		}
		config.ConnConfig.Database = check.otherDB
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		pool, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		err = pool.Ping(ctx)
		pool.Close()
		cancel()
		var denied *pgconn.PgError
		if !errors.As(err, &denied) || denied.Code != "42501" {
			t.Fatalf("expected database permission denial for %s -> %s, got %v", check.key, check.otherDB, err)
		}
	}
	for _, check := range []struct {
		db    *pgxpool.Pool
		table string
	}{{s.db, "users"}, {s.db, "refresh_tokens"}, {s.usersDB, "products"}, {s.usersDB, "orders"}} {
		var absent bool
		if err := check.db.QueryRow(context.Background(), "SELECT to_regclass($1) IS NULL", "public."+check.table).Scan(&absent); err != nil {
			t.Fatal(err)
		}
		if !absent {
			t.Fatalf("service owns unexpected table %s", check.table)
		}
	}
	for _, check := range []struct {
		url, method, path string
		body              any
	}{{s.commerceURL, "POST", "/auth/register", map[string]any{"email": "boundary@example.test", "password": "valid-password"}}, {s.usersURL, "GET", "/products", nil}} {
		raw, _ := json.Marshal(check.body)
		r, _ := http.NewRequest(check.method, check.url+check.path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		response, err := s.client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatalf("foreign service route exposed: %s %d", check.path, response.StatusCode)
		}
	}
	data := map[string]any{"name": "owned", "price": 1, "stock": 2, "category": "a", "status": "ACTIVE"}
	var owned api.ProductResponse
	json.Unmarshal(s.call("POST", "/products", s.admin.AccessToken, data, 201, ""), &owned)
	if owned.SellerId != s.admin.UserId {
		t.Fatal("product owner must come from JWT")
	}
	data["seller_id"] = s.seller.UserId
	s.call("POST", "/products", s.admin.AccessToken, data, 400, "VALIDATION_ERROR")
	s.call("POST", "/products", s.seller.AccessToken, data, 400, "VALIDATION_ERROR")
	s.call("GET", "/users/"+s.seller.UserId.String(), s.admin.AccessToken, nil, 404, "NOT_FOUND")
	// Commerce can handle a signed user's order without any local user row.
	p := s.product(1, 2, "ACTIVE")
	o := s.order(p, 1, "")
	s.call("POST", "/orders/"+o.Id.String()+"/cancel", s.buyer.AccessToken, nil, 200, "")
}
