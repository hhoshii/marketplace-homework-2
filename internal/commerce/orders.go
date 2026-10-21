package commerce

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/hhoshii/marketplace-architecture/internal/api"
	"github.com/hhoshii/marketplace-architecture/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

const orderColumns = "id,user_id,status,promo_code_id,total_amount,discount_amount,created_at,updated_at"

func scanOrder(row pgx.Row) (api.OrderResponse, error) {
	var o api.OrderResponse
	err := row.Scan(&o.Id, &o.UserId, &o.Status, &o.PromoCodeId, &o.TotalAmount, &o.DiscountAmount, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = platform.Problem(404, api.ORDERNOTFOUND, "Заказ не найден")
	}
	return o, err
}
func loadItems(ctx context.Context, tx pgx.Tx, id uuid.UUID) ([]api.OrderItemResponse, error) {
	rows, err := tx.Query(ctx, "SELECT product_id,quantity,price_at_order FROM order_items WHERE order_id=$1 ORDER BY product_id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []api.OrderItemResponse{}
	for rows.Next() {
		var i api.OrderItemResponse
		if err = rows.Scan(&i.ProductId, &i.Quantity, &i.PriceAtOrder); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
func ownership(p platform.Principal, o api.OrderResponse) error {
	if p.Role != api.RoleADMIN && p.ID != o.UserId {
		return platform.Problem(403, api.ORDEROWNERSHIPVIOLATION, "Заказ принадлежит другому пользователю")
	}
	return nil
}
func (s *Service) orderRole(r *http.Request) error {
	if !platform.Permitted(platform.Who(r), api.RoleUSER, api.RoleADMIN) {
		return platform.Deny()
	}
	return nil
}
func lockUser(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var v uuid.UUID
	if _, err := tx.Exec(ctx, "INSERT INTO checkout_locks(user_id) VALUES($1) ON CONFLICT DO NOTHING", id); err != nil {
		return err
	}
	return tx.QueryRow(ctx, "SELECT user_id FROM checkout_locks WHERE user_id=$1 FOR UPDATE", id).Scan(&v)
}
func (s *Service) rate(ctx context.Context, tx pgx.Tx, user uuid.UUID, operation string) error {
	var limited bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_operations WHERE user_id=$1 AND operation_type=$2
 AND created_at > clock_timestamp()-($3::bigint * interval '1 microsecond'))`, user, operation, s.Config.OrderInterval.Microseconds()).Scan(&limited)
	if err != nil {
		return err
	}
	if limited {
		return platform.Problem(429, api.ORDERLIMITEXCEEDED, "Слишком частые операции с заказом")
	}
	return nil
}
func distinctItems(items []api.OrderItemInput) error {
	seen := map[uuid.UUID]bool{}
	for _, i := range items {
		if seen[i.ProductId] {
			return platform.Validation("items", "Повторный product_id запрещён")
		}
		seen[i.ProductId] = true
	}
	return nil
}

// All order mutations lock user -> order -> sorted union of products -> promo.
// Sorting prevents deadlocks for orders with overlapping product sets.
func lockProducts(ctx context.Context, tx pgx.Tx, newItems []api.OrderItemInput, oldItems []api.OrderItemResponse) (map[uuid.UUID]api.ProductResponse, error) {
	ids := map[uuid.UUID]bool{}
	for _, i := range newItems {
		ids[i.ProductId] = true
	}
	for _, i := range oldItems {
		ids[i.ProductId] = true
	}
	sorted := make([]uuid.UUID, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].String() < sorted[j].String() })
	products := map[uuid.UUID]api.ProductResponse{}
	for _, id := range sorted {
		p, err := scanProduct(tx.QueryRow(ctx, "SELECT "+productColumns+" FROM products WHERE id=$1 FOR UPDATE", id))
		if err != nil {
			return nil, err
		}
		products[id] = p
	}
	return products, nil
}
func reserve(ctx context.Context, tx pgx.Tx, products map[uuid.UUID]api.ProductResponse, items []api.OrderItemInput) ([]api.OrderItemResponse, decimal.Decimal, error) {
	for _, i := range items {
		if products[i.ProductId].Status != api.ACTIVE {
			return nil, decimal.Zero, platform.Problem(409, api.PRODUCTINACTIVE, "Товар неактивен")
		}
	}
	missing := []map[string]any{}
	for _, i := range items {
		p := products[i.ProductId]
		if p.Stock < i.Quantity {
			missing = append(missing, map[string]any{"product_id": i.ProductId, "requested_quantity": i.Quantity, "available_stock": p.Stock})
		}
	}
	if len(missing) > 0 {
		e := platform.Problem(409, api.INSUFFICIENTSTOCK, "Недостаточно остатков")
		e.Details = map[string]any{"products": missing}
		return nil, decimal.Zero, e
	}
	snapshot := make([]api.OrderItemResponse, 0, len(items))
	total := decimal.Zero
	for _, i := range items {
		p := products[i.ProductId]
		if _, err := tx.Exec(ctx, "UPDATE products SET stock=stock-$2 WHERE id=$1", p.Id, i.Quantity); err != nil {
			return nil, decimal.Zero, err
		}
		snapshot = append(snapshot, api.OrderItemResponse{ProductId: p.Id, Quantity: i.Quantity, PriceAtOrder: p.Price})
		total = total.Add(p.Price.Mul(decimal.NewFromInt(int64(i.Quantity))))
	}
	if total.GreaterThan(decimal.RequireFromString("9999999999.99")) {
		return nil, decimal.Zero, platform.Validation("items", "Сумма превышает NUMERIC(12,2)")
	}
	return snapshot, total, nil
}
func restore(ctx context.Context, tx pgx.Tx, products map[uuid.UUID]api.ProductResponse, items []api.OrderItemResponse) error {
	for _, i := range items {
		p := products[i.ProductId]
		if int64(p.Stock)+int64(i.Quantity) > 2147483647 {
			return platform.Validation("stock", "Возврат превышает максимальный остаток")
		}
		if _, err := tx.Exec(ctx, "UPDATE products SET stock=stock+$2 WHERE id=$1", i.ProductId, i.Quantity); err != nil {
			return err
		}
		p.Stock += i.Quantity
		products[i.ProductId] = p
	}
	return nil
}
func saveItems(ctx context.Context, tx pgx.Tx, id uuid.UUID, items []api.OrderItemResponse) error {
	for _, i := range items {
		if _, err := tx.Exec(ctx, "INSERT INTO order_items(order_id,product_id,quantity,price_at_order) VALUES($1,$2,$3,$4)", id, i.ProductId, i.Quantity, i.PriceAtOrder); err != nil {
			return err
		}
	}
	return nil
}
func discount(total, value decimal.Decimal, kind string) decimal.Decimal {
	if kind == "PERCENTAGE" {
		if value.GreaterThan(decimal.NewFromInt(70)) {
			return decimal.Zero
		}
		return total.Mul(value).Div(decimal.NewFromInt(100)).Round(2)
	}
	return decimal.Min(value, total)
}
func promo(ctx context.Context, tx pgx.Tx, total decimal.Decimal, code *string, existing *uuid.UUID) (*uuid.UUID, decimal.Decimal, error) {
	if code == nil && existing == nil {
		return nil, decimal.Zero, nil
	}
	var id uuid.UUID
	var kind string
	var value, min decimal.Decimal
	var uses, max int
	var active bool
	var from, until time.Time
	query := "SELECT id,discount_type,discount_value,min_order_amount,current_uses,max_uses,active,valid_from,valid_until FROM promo_codes WHERE "
	var arg any
	if existing != nil {
		query += "id=$1"
		arg = *existing
	} else {
		query += "code=$1"
		arg = *code
	}
	err := tx.QueryRow(ctx, query+" FOR UPDATE", arg).Scan(&id, &kind, &value, &min, &uses, &max, &active, &from, &until)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, decimal.Zero, platform.Problem(422, api.PROMOCODEINVALID, "Промокод не найден")
	}
	if err != nil {
		return nil, decimal.Zero, err
	}
	now := time.Now()
	// An existing reservation already owns one use, even when uses == max.
	valid := active && !now.Before(from) && !now.After(until) && (existing != nil || uses < max)
	if !valid || total.LessThan(min) {
		if existing != nil {
			_, err = tx.Exec(ctx, "UPDATE promo_codes SET current_uses=current_uses-1 WHERE id=$1", id)
			return nil, decimal.Zero, err
		}
		if !valid {
			return nil, decimal.Zero, platform.Problem(422, api.PROMOCODEINVALID, "Промокод истёк, неактивен или исчерпан")
		}
		return nil, decimal.Zero, platform.Problem(422, api.PROMOCODEMINAMOUNT, "Не достигнута минимальная сумма")
	}
	if existing == nil {
		if _, err = tx.Exec(ctx, "UPDATE promo_codes SET current_uses=current_uses+1 WHERE id=$1", id); err != nil {
			return nil, decimal.Zero, err
		}
	}
	return &id, discount(total, value, kind), nil
}
func (s *Service) CreateOrder(w http.ResponseWriter, r *http.Request) {
	if err := s.orderRole(r); err != nil {
		s.Fail(w, r, err)
		return
	}
	in, err := platform.Decode[api.OrderCreate](r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	if err = distinctItems(in.Items); err != nil {
		s.Fail(w, r, err)
		return
	}
	p := platform.Who(r)
	v, err := s.Transaction(r.Context(), func(tx pgx.Tx) (any, error) {
		ctx := r.Context()
		if err := lockUser(ctx, tx, p.ID); err != nil {
			return nil, err
		}
		if err := s.rate(ctx, tx, p.ID, "CREATE_ORDER"); err != nil {
			return nil, err
		}
		var active bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM orders WHERE user_id=$1 AND status IN ('CREATED','PAYMENT_PENDING'))", p.ID).Scan(&active); err != nil {
			return nil, err
		}
		if active {
			return nil, platform.Problem(409, api.ORDERHASACTIVE, "Уже есть активный заказ")
		}
		products, err := lockProducts(ctx, tx, in.Items, nil)
		if err != nil {
			return nil, err
		}
		items, total, err := reserve(ctx, tx, products, in.Items)
		if err != nil {
			return nil, err
		}
		promoID, off, err := promo(ctx, tx, total, in.PromoCode, nil)
		if err != nil {
			return nil, err
		}
		o, err := scanOrder(tx.QueryRow(ctx, "INSERT INTO orders(user_id,promo_code_id,total_amount,discount_amount) VALUES($1,$2,$3,$4) RETURNING "+orderColumns, p.ID, promoID, total.Sub(off), off))
		if err != nil {
			return nil, err
		}
		if err = saveItems(ctx, tx, o.Id, items); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO user_operations(user_id,operation_type) VALUES($1,'CREATE_ORDER')", p.ID); err != nil {
			return nil, err
		}
		o.Items = items
		return o, nil
	})
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	platform.Write(w, 201, v)
}
func (s *Service) GetOrder(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if err := s.orderRole(r); err != nil {
		s.Fail(w, r, err)
		return
	}
	v, err := s.Transaction(r.Context(), func(tx pgx.Tx) (any, error) {
		if _, err := tx.Exec(r.Context(), "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
			return nil, err
		}
		o, err := scanOrder(tx.QueryRow(r.Context(), "SELECT "+orderColumns+" FROM orders WHERE id=$1", id))
		if err != nil {
			return nil, err
		}
		if err = ownership(platform.Who(r), o); err != nil {
			return nil, err
		}
		o.Items, err = loadItems(r.Context(), tx, id)
		return o, err
	})
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	platform.Write(w, 200, v)
}
func (s *Service) lockedOrder(r *http.Request, tx pgx.Tx, id uuid.UUID) (api.OrderResponse, error) {
	ctx := r.Context()
	// Discover the immutable owner before taking locks.
	var owner uuid.UUID
	err := tx.QueryRow(ctx, "SELECT user_id FROM orders WHERE id=$1", id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.OrderResponse{}, platform.Problem(404, api.ORDERNOTFOUND, "Заказ не найден")
	}
	if err != nil {
		return api.OrderResponse{}, err
	}
	if err = lockUser(ctx, tx, owner); err != nil {
		return api.OrderResponse{}, err
	}
	o, err := scanOrder(tx.QueryRow(ctx, "SELECT "+orderColumns+" FROM orders WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return o, err
	}
	if err = ownership(platform.Who(r), o); err != nil {
		return o, err
	}
	o.Items, err = loadItems(ctx, tx, id)
	return o, err
}
func (s *Service) UpdateOrder(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if err := s.orderRole(r); err != nil {
		s.Fail(w, r, err)
		return
	}
	in, err := platform.Decode[api.OrderUpdate](r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	if err = distinctItems(in.Items); err != nil {
		s.Fail(w, r, err)
		return
	}
	v, err := s.Transaction(r.Context(), func(tx pgx.Tx) (any, error) {
		ctx := r.Context()
		o, err := s.lockedOrder(r, tx, id)
		if err != nil {
			return nil, err
		}
		if o.Status != api.CREATED {
			return nil, platform.Problem(409, api.INVALIDSTATETRANSITION, "Обновление разрешено только для CREATED")
		}
		if err = s.rate(ctx, tx, o.UserId, "UPDATE_ORDER"); err != nil {
			return nil, err
		}
		products, err := lockProducts(ctx, tx, in.Items, o.Items)
		if err != nil {
			return nil, err
		}
		if err = restore(ctx, tx, products, o.Items); err != nil {
			return nil, err
		}
		items, total, err := reserve(ctx, tx, products, in.Items)
		if err != nil {
			return nil, err
		}
		promoID, off, err := promo(ctx, tx, total, nil, o.PromoCodeId)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM order_items WHERE order_id=$1", id); err != nil {
			return nil, err
		}
		if err = saveItems(ctx, tx, id, items); err != nil {
			return nil, err
		}
		o, err = scanOrder(tx.QueryRow(ctx, "UPDATE orders SET total_amount=$2,discount_amount=$3,promo_code_id=$4 WHERE id=$1 RETURNING "+orderColumns, id, total.Sub(off), off, promoID))
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO user_operations(user_id,operation_type) VALUES($1,'UPDATE_ORDER')", o.UserId); err != nil {
			return nil, err
		}
		o.Items = items
		return o, nil
	})
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	platform.Write(w, 200, v)
}
func (s *Service) CancelOrder(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if err := s.orderRole(r); err != nil {
		s.Fail(w, r, err)
		return
	}
	v, err := s.Transaction(r.Context(), func(tx pgx.Tx) (any, error) {
		ctx := r.Context()
		o, err := s.lockedOrder(r, tx, id)
		if err != nil {
			return nil, err
		}
		if o.Status != api.CREATED && o.Status != api.PAYMENTPENDING {
			return nil, platform.Problem(409, api.INVALIDSTATETRANSITION, "Отмена разрешена только для CREATED или PAYMENT_PENDING")
		}
		products, err := lockProducts(ctx, tx, nil, o.Items)
		if err != nil {
			return nil, err
		}
		if err = restore(ctx, tx, products, o.Items); err != nil {
			return nil, err
		}
		if o.PromoCodeId != nil {
			if _, err = tx.Exec(ctx, "UPDATE promo_codes SET current_uses=current_uses-1 WHERE id=$1", *o.PromoCodeId); err != nil {
				return nil, err
			}
		}
		items := o.Items
		o, err = scanOrder(tx.QueryRow(ctx, "UPDATE orders SET status='CANCELED' WHERE id=$1 RETURNING "+orderColumns, id))
		o.Items = items
		return o, err
	})
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	platform.Write(w, 200, v)
}
func (s *Service) TransitionOrder(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if platform.Who(r).Role != api.RoleADMIN {
		s.Fail(w, r, platform.Deny())
		return
	}
	in, err := platform.Decode[api.OrderTransition](r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	v, err := s.Transaction(r.Context(), func(tx pgx.Tx) (any, error) {
		o, err := s.lockedOrder(r, tx, id)
		if err != nil {
			return nil, err
		}
		next := map[api.OrderStatus]api.OrderStatus{api.CREATED: api.PAYMENTPENDING, api.PAYMENTPENDING: api.PAID, api.PAID: api.SHIPPED, api.SHIPPED: api.COMPLETED}
		if next[o.Status] != in.Status {
			return nil, platform.Problem(409, api.INVALIDSTATETRANSITION, "Недопустимый переход состояния")
		}
		items := o.Items
		o, err = scanOrder(tx.QueryRow(r.Context(), "UPDATE orders SET status=$2 WHERE id=$1 RETURNING "+orderColumns, id, in.Status))
		o.Items = items
		return o, err
	})
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	platform.Write(w, 200, v)
}
