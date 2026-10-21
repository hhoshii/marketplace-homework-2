package security

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/hhoshii/marketplace-architecture/internal/api"
)

type Claims struct {
	Role api.Role `json:"role"`
	Type string   `json:"type"`
	jwt.RegisteredClaims
}
type Tokens struct {
	public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func NewVerifier(publicHex string) (*Tokens, error) {
	public, err := hex.DecodeString(publicHex)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return nil, errors.New("JWT_PUBLIC_KEY must encode 32 Ed25519 public-key bytes")
	}
	return &Tokens{public: ed25519.PublicKey(public)}, nil
}
func NewSigner(seedHex string) (*Tokens, error) {
	seed, err := hex.DecodeString(seedHex)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("JWT_PRIVATE_KEY must encode 32 Ed25519 seed bytes")
	}
	key := ed25519.NewKeyFromSeed(seed)
	return &Tokens{private: key, public: key.Public().(ed25519.PublicKey)}, nil
}
func (t *Tokens) PublicHex() string { return hex.EncodeToString(t.public) }
func (t *Tokens) Sign(id uuid.UUID, role api.Role, kind string, ttl time.Duration, jti string, now time.Time) (string, error) {
	if len(t.private) == 0 {
		return "", errors.New("token signing is unavailable in verifier")
	}
	return jwt.NewWithClaims(jwt.SigningMethodEdDSA, Claims{Role: role, Type: kind, RegisteredClaims: jwt.RegisteredClaims{
		Subject: id.String(), ID: jti, Issuer: "user-service", Audience: jwt.ClaimStrings{"marketplace-" + kind},
		IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}}).SignedString(t.private)
}
func (t *Tokens) Parse(raw, kind string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) { return t.public, nil },
		jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer("user-service"), jwt.WithAudience("marketplace-"+kind), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil {
		return nil, err
	}
	if claims.Type != kind {
		return nil, errors.New("wrong token type")
	}
	if claims.Role != api.RoleUSER && claims.Role != api.RoleSELLER && claims.Role != api.RoleADMIN {
		return nil, errors.New("invalid role")
	}
	for _, v := range []string{claims.Subject, claims.ID} {
		if _, err = uuid.Parse(v); err != nil {
			return nil, fmt.Errorf("invalid token UUID: %w", err)
		}
	}
	return claims, nil
}
