package security

import (
	"github.com/google/uuid"
	"github.com/hhoshii/marketplace-architecture/internal/api"
	"strings"
	"testing"
	"time"
)

func TestPublicVerifierCannotSign(t *testing.T) {
	signer, err := NewSigner(strings.Repeat("00", 32))
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(signer.PublicHex())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = verifier.Sign(uuid.New(), api.RoleADMIN, "access", time.Minute, uuid.NewString(), time.Now()); err == nil {
		t.Fatal("public verifier minted token")
	}
	token, err := signer.Sign(uuid.New(), api.RoleUSER, "access", time.Minute, uuid.NewString(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = verifier.Parse(token, "access"); err != nil {
		t.Fatal(err)
	}
}
