package postgres

import (
	"context"
	"testing"
)

func TestTxContext(t *testing.T) {
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
