package service

import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"

type noopLogger struct{}

func (noopLogger) Info(string, map[string]any)  {}
func (noopLogger) Error(string, map[string]any) {}
func (noopLogger) Fatal(string, map[string]any) {}
func (noopLogger) Warn(string, map[string]any)  {}
func (noopLogger) Debug(string, map[string]any) {}

var _ port.Logger = noopLogger{}

func logOrNoop(l port.Logger) port.Logger {
	if l == nil {
		return noopLogger{}
	}
	return l
}
