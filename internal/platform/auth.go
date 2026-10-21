package platform

import (
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/hhoshii/marketplace-architecture/internal/api"
)

func (s *Base) authenticate(r *http.Request) (Principal, error) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return Principal{}, Problem(401, api.TOKENINVALID, "Требуется access token")
	}
	claims, err := s.Verifier.Parse(parts[1], "access")
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return Principal{}, Problem(401, api.TOKENEXPIRED, "Access token истёк")
		}
		return Principal{}, Problem(401, api.TOKENINVALID, "Некорректный access token")
	}
	return Principal{ID: uuid.MustParse(claims.Subject), Role: claims.Role}, nil
}
