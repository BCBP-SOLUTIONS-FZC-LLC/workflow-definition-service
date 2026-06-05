package postgres

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// stubTx satisfies the pgx.Tx interface without a real DB connection.
type stubTx struct{ pgx.Tx }

func TestTxContext_NotFound(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		wantOk bool
	}{
		{
			name:   "plain context returns not-ok",
			ctx:    context.Background(),
			wantOk: false,
		},
		{
			name:   "wrong value type returns not-ok",
			ctx:    context.WithValue(context.Background(), txContextKey{}, "not-a-tx"),
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := txFromContext(tt.ctx)
			if ok != tt.wantOk {
				t.Errorf("txFromContext ok = %v, want %v", ok, tt.wantOk)
			}
		})
	}
}

func TestTxContext_RoundTrip(t *testing.T) {
	var tx pgx.Tx = &stubTx{}
	ctx := withTx(context.Background(), tx)
	got, ok := txFromContext(ctx)
	if !ok {
		t.Fatal("txFromContext: ok = false, want true")
	}
	if got != tx {
		t.Errorf("txFromContext returned a different value than stored by withTx")
	}
}
