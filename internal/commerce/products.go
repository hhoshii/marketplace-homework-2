package commerce

import (
	"errors"
	"github.com/google/uuid"
	"github.com/hhoshii/marketplace-architecture/internal/api"
	"github.com/hhoshii/marketplace-architecture/internal/platform"
	"github.com/jackc/pgx/v5"
	"net/http"
)

const productColumns = "id,name,description,price,stock,category,status,seller_id,created_at,updated_at"

func scanProduct(row pgx.Row) (api.ProductResponse, error) {
	var p api.ProductResponse
	err := row.Scan(&p.Id, &p.Name, &p.Description, &p.Price, &p.Stock, &p.Category, &p.Status, &p.SellerId, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = platform.Problem(404, api.PRODUCTNOTFOUND, "Товар не найден")
	}
	return p, err
}
func (s *Service) CreateProduct(w http.ResponseWriter, r *http.Request) {
	p := platform.Who(r)
	if !platform.Permitted(p, api.RoleSELLER, api.RoleADMIN) {
		s.Fail(w, r, platform.Deny())
		return
	}
	in, err := platform.Decode[api.ProductCreate](r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	v, err := scanProduct(s.DB.QueryRow(r.Context(), `INSERT INTO products(name,description,price,stock,category,status,seller_id)
 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+productColumns, in.Name, in.Description, in.Price, in.Stock, in.Category, in.Status, p.ID))
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	platform.Write(w, 201, v)
}
func (s *Service) GetProduct(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	v, err := scanProduct(s.DB.QueryRow(r.Context(), "SELECT "+productColumns+" FROM products WHERE id=$1", id))
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	platform.Write(w, 200, v)
}
func (s *Service) ListProducts(w http.ResponseWriter, r *http.Request, in api.ListProductsParams) {
	page, size := 0, 20
	if in.Page != nil {
		page = *in.Page
	}
	if in.Size != nil {
		size = *in.Size
	}
	v, err := s.Transaction(r.Context(), func(tx pgx.Tx) (any, error) {
		// Count and content share one MVCC snapshot.
		if _, err := tx.Exec(r.Context(), "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
			return nil, err
		}
		out := api.ProductPage{Content: []api.ProductResponse{}, Page: page, Size: size}
		filter := " FROM products WHERE ($1::text IS NULL OR status::text=$1) AND ($2::text IS NULL OR category=$2)"
		if err := tx.QueryRow(r.Context(), "SELECT count(*)"+filter, in.Status, in.Category).Scan(&out.TotalElements); err != nil {
			return nil, err
		}
		rows, err := tx.Query(r.Context(), "SELECT "+productColumns+filter+" ORDER BY created_at,id LIMIT $3 OFFSET $4", in.Status, in.Category, size, page*size)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanProduct(rows)
			if err != nil {
				return nil, err
			}
			out.Content = append(out.Content, p)
		}
		return out, rows.Err()
	})
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	platform.Write(w, 200, v)
}
func (s *Service) mutateProduct(w http.ResponseWriter, r *http.Request, id uuid.UUID, archive bool) {
	p := platform.Who(r)
	if !platform.Permitted(p, api.RoleSELLER, api.RoleADMIN) {
		s.Fail(w, r, platform.Deny())
		return
	}
	var in api.ProductUpdate
	if !archive {
		var err error
		in, err = platform.Decode[api.ProductUpdate](r)
		if err != nil {
			s.Fail(w, r, err)
			return
		}
	}
	v, err := s.Transaction(r.Context(), func(tx pgx.Tx) (any, error) {
		old, err := scanProduct(tx.QueryRow(r.Context(), "SELECT "+productColumns+" FROM products WHERE id=$1 FOR UPDATE", id))
		if err != nil {
			return nil, err
		}
		if p.Role != api.RoleADMIN && old.SellerId != p.ID {
			return nil, platform.Deny()
		}
		if archive {
			_, err = tx.Exec(r.Context(), "UPDATE products SET status='ARCHIVED' WHERE id=$1", id)
			return nil, err
		}
		return scanProduct(tx.QueryRow(r.Context(), `UPDATE products SET name=$2,description=$3,price=$4,stock=$5,category=$6,status=$7 WHERE id=$1 RETURNING `+productColumns,
			id, in.Name, in.Description, in.Price, in.Stock, in.Category, in.Status))
	})
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	if archive {
		w.WriteHeader(204)
		return
	}
	platform.Write(w, 200, v)
}
func (s *Service) UpdateProduct(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	s.mutateProduct(w, r, id, false)
}
func (s *Service) DeleteProduct(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	s.mutateProduct(w, r, id, true)
}
func (s *Service) CreatePromo(w http.ResponseWriter, r *http.Request) {
	if !platform.Permitted(platform.Who(r), api.RoleSELLER, api.RoleADMIN) {
		s.Fail(w, r, platform.Deny())
		return
	}
	in, err := platform.Decode[api.PromoCreate](r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	if !in.ValidUntil.After(in.ValidFrom) {
		s.Fail(w, r, platform.Validation("valid_until", "Должно быть позже valid_from"))
		return
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	var v api.PromoResponse
	err = s.DB.QueryRow(r.Context(), `INSERT INTO promo_codes(code,discount_type,discount_value,min_order_amount,max_uses,valid_from,valid_until,active)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id,code,current_uses`, in.Code, in.DiscountType, in.DiscountValue, in.MinOrderAmount, in.MaxUses, in.ValidFrom, in.ValidUntil, active).Scan(&v.Id, &v.Code, &v.CurrentUses)
	if platform.UniqueViolation(err) {
		err = platform.Validation("code", "Промокод уже существует")
	}
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	platform.Write(w, 201, v)
}
