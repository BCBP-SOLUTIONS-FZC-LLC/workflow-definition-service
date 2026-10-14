// Package testkit provides shared test helpers for unit test packages.
package testkit

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

// FakeLogger is a no-op port.Logger for use in unit tests.
type FakeLogger struct{}

// Info implements port.Logger.
func (FakeLogger) Info(_ string, _ map[string]any) { /* no-op */ }

// Error implements port.Logger.
func (FakeLogger) Error(_ string, _ map[string]any) { /* no-op */ }

// Fatal implements port.Logger.
func (FakeLogger) Fatal(_ string, _ map[string]any) { /* no-op */ }

// Warn implements port.Logger.
func (FakeLogger) Warn(_ string, _ map[string]any) { /* no-op */ }

// Debug implements port.Logger.
func (FakeLogger) Debug(_ string, _ map[string]any) { /* no-op */ }

var _ port.Logger = FakeLogger{}
