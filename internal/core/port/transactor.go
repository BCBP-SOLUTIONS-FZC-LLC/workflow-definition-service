package port

import "context"

// Transactor runs a function inside a database transaction.
// The ctx passed to fn carries the active transaction so that
// repository calls made inside fn automatically join it.
type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}
