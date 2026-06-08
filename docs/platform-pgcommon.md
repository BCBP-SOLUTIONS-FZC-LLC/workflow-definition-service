# platform-pgcommon

Module: `github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon`

Provides the PostgreSQL connection pool with RLS GUC injection, transaction helpers, slow-query tracing, and a migration runner. All repository adapters in `internal/adapter/outbound/postgres/` depend on this library.

---

## Pool setup

```go
import "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

pool, err := pgcommon.NewPool(ctx, pgcommon.Config{
    DSN:         cfg.DatabaseURL,
    MaxConns:    cfg.PGMaxConns,
    MinConns:    cfg.PGMinConns,
    Logger:      log,              // accepts port.Logger — pass gincommon ZapLogger directly
    GUCProvider: pgcommon.GUCSetFromContext,  // auto-injects tenant_id, user_id, roles on every connection
})
```

`GUCSetFromContext` reads the `GUCSet` stored by `WithGUCSet(ctx, gs)` — the `platform-gincommon` middleware calls this for every HTTP request.

---

## Connection lifecycle

### Single query (`WithConn`)

```mermaid
sequenceDiagram
    participant Caller
    participant Pool
    participant pgxpool
    participant Hook as PrepareConn hook
    participant DB as PostgreSQL

    Caller ->>+ Pool: WithConn(ctx, fn)
    Pool   ->>+ pgxpool: Acquire(ctx)
    pgxpool ->>+ Hook: PrepareConn(ctx, conn)
    alt GUCProvider set and GUCSet non-zero
        Hook ->> Hook: GUCProvider(ctx) → GUCSet
        Hook ->> Hook: g.Validate()
        Hook ->>  DB: batch set_config(user_id, tenant_id, tenant_roles, is_local=false)
        Hook -->> pgxpool: true, nil
    else no GUCProvider or zero GUCSet
        Hook -->> pgxpool: true, nil
    end
    pgxpool -->>- Pool: *pgxpool.Conn
    Pool   ->>+ Caller: fn(ctx, conn)
    Caller -->>- Pool: error or nil
    Pool   ->> pgxpool: conn.Release()
    Pool -->>- Caller: return error
```

### Transaction (`RunInTx`)

```mermaid
sequenceDiagram
    participant Caller
    participant RunInTx
    participant DB as PostgreSQL

    Caller ->>+ RunInTx: RunInTx(ctx, pool, opts, fn)
    RunInTx ->>+ DB: pool.BeginTx(ctx, opts)
    DB -->>- RunInTx: tx

    RunInTx ->>+ Caller: fn(ctx, tx)

    alt fn returns nil
        Caller -->>- RunInTx: nil
        RunInTx ->> DB: tx.Commit(ctx)
        RunInTx -->> Caller: nil
    else fn returns error
        Caller -->> RunInTx: error
        RunInTx ->> DB: tx.Rollback(context.Background())
        RunInTx -->>- Caller: fn error
    else fn panics
        RunInTx ->> DB: tx.Rollback(context.Background())
        RunInTx ->>- Caller: re-panic
    end
```

---

## Transactor pattern (definition service)

Repos participate in multi-step transactions without leaking pgx types into the port layer. The `Transactor` adapter stores the `pgx.Tx` in context:

```go
// Service layer — no pgx imports needed
err = s.transactor.RunInTx(ctx, func(ctx context.Context) error {
    if err := s.versionRepo.Publish(ctx, ...); err != nil { return err }
    return s.outboxRepo.Enqueue(ctx, env)
})
```

Repos call `exec(ctx, pool, fn)` which checks context for a tx first:

```go
// In base.go — adapter layer only
func exec(ctx context.Context, pool *pgcommon.Pool, fn func(db.DBTX) error) error {
    if tx, ok := txFromContext(ctx); ok {
        return fn(tx)
    }
    return pool.WithConn(ctx, func(_ context.Context, conn *pgxpool.Conn) error {
        return fn(conn)
    })
}
```

---

## Retry on deadlock

```go
err = pgcommon.RunInTxWithRetryOpts(ctx, pool, pgx.TxOptions{
    IsoLevel:   pgx.Serializable,
    AccessMode: pgx.ReadWrite,
}, pgcommon.RetryOptions{
    MaxAttempts:    5,
    InitialWait:    10 * time.Millisecond,
    MaxWait:        500 * time.Millisecond,
    Multiplier:     2.0,
    JitterFraction: 0.25,
}, func(ctx context.Context, tx pgx.Tx) error {
    return doWork(ctx, tx)
})
```

Retries automatically on SQLSTATE `40P01` (deadlock) and `40001` (serialization failure).

---

## Error helpers

```go
if pgcommon.IsUniqueViolation(err) {
    switch pgcommon.ConstraintName(err) {
    case "workflow_tenant_id_business_key_key":
        return domain.ErrDuplicateBusinessKey
    case "idx_wv_single_draft":
        return domain.ErrDraftAlreadyExists
    }
}
if errors.Is(err, pgx.ErrNoRows) {
    return domain.ErrNotFound
}
```

All helpers work with wrapped errors (`errors.As` internally).

---

## Key configuration

| Env var | Default | Notes |
|---------|---------|-------|
| `DATABASE_URL` | — | Required |
| `PG_MAX_CONNS` | `10` | Pool max |
| `PG_MIN_CONNS` | `2` | Pool min idle |
| `PG_SLOW_QUERY_THRESHOLD_MS` | `200` | Log slow queries at WARN |
