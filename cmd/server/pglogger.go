package main

import (
	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/domain"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

// pgCommonLogger adapts this service's port.Logger (map[string]any fields)
// to platform-pgcommon's domain.Logger (variadic domain.Field) — port.Logger
// is defined against an unexported internal type in platform-pgcommon
// versions before v1.2.0, so no external module could implement it directly;
// domain.Logger is the now-public, externally-implementable replacement.
// Composition-root-only: internal/core/service can't depend on either
// platform-pgcommon or a platform-gincommon-shaped logger without breaking
// arch-lint's domain/port/service/adapter direction.
type pgCommonLogger struct{ log port.Logger }

func newPGCommonLogger(log port.Logger) pgdomain.Logger {
	return pgCommonLogger{log: log}
}

func (l pgCommonLogger) Debug(msg string, fields ...pgdomain.Field) {
	l.log.Debug(msg, pgFieldsToMap(fields))
}

func (l pgCommonLogger) Info(msg string, fields ...pgdomain.Field) {
	l.log.Info(msg, pgFieldsToMap(fields))
}

func (l pgCommonLogger) Warn(msg string, fields ...pgdomain.Field) {
	l.log.Warn(msg, pgFieldsToMap(fields))
}

func (l pgCommonLogger) Error(msg string, fields ...pgdomain.Field) {
	l.log.Error(msg, pgFieldsToMap(fields))
}

func pgFieldsToMap(fields []pgdomain.Field) map[string]any {
	m := make(map[string]any, len(fields))
	for _, f := range fields {
		m[f.Key] = f.Value
	}
	return m
}
