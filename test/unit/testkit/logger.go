// Package testkit provides shared test helpers for unit test packages.
package testkit

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

// FakeLogger is a no-op port.Logger for use in unit tests.
type FakeLogger struct{}

func (FakeLogger) Info(msg string, fields map[string]any)  {}
func (FakeLogger) Error(msg string, fields map[string]any) {}
func (FakeLogger) Fatal(msg string, fields map[string]any) {}
func (FakeLogger) Warn(msg string, fields map[string]any)  {}
func (FakeLogger) Debug(msg string, fields map[string]any) {}

var _ port.Logger = FakeLogger{}
