package users

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestConfig(t *testing.T) {
	for _, c := range []Config{{PrivateKey: "short"}, {PrivateKey: strings.Repeat("00", 32), AccessTTL: time.Hour}, {PrivateKey: strings.Repeat("00", 32), RefreshTTL: time.Hour}} {
		if _, err := New(nil, c, slog.New(slog.NewJSONHandler(io.Discard, nil))); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}
