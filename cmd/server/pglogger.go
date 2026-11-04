package main

import (
	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

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
