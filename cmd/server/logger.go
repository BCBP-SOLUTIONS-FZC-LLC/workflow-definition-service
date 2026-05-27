package main

import "go.uber.org/zap"

// portLogger bridges gincommon's port.Logger interface (map-based fields)
// to *zap.Logger typed field calls.
type portLogger struct {
	z *zap.Logger
}

func (p *portLogger) Debug(msg string, fields map[string]interface{}) {
	p.z.Debug(msg, toZapFields(fields)...)
}

func (p *portLogger) Info(msg string, fields map[string]interface{}) {
	p.z.Info(msg, toZapFields(fields)...)
}

func (p *portLogger) Warn(msg string, fields map[string]interface{}) {
	p.z.Warn(msg, toZapFields(fields)...)
}

func (p *portLogger) Error(msg string, fields map[string]interface{}) {
	p.z.Error(msg, toZapFields(fields)...)
}

func (p *portLogger) Fatal(msg string, fields map[string]interface{}) {
	p.z.Fatal(msg, toZapFields(fields)...)
}

func toZapFields(fields map[string]interface{}) []zap.Field {
	zf := make([]zap.Field, 0, len(fields))
	for k, v := range fields {
		zf = append(zf, zap.Any(k, v))
	}
	return zf
}
