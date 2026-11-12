package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
)

type countingLog struct {
	spyLogger
	lastFields map[string]any
}

func (l *countingLog) Error(msg string, fields map[string]any) {
	l.spyLogger.Error(msg, fields)
	l.lastFields = fields
}

func TestDispatchEvent_ATypeWithNoCase_IsAnErrorAndNotASilent200(t *testing.T) {
	log := &countingLog{}
	h := &Handler{log: log}
	c, w := newTestCtx("/api/v1/internal/events")

	h.dispatchEvent(c, events.Envelope[json.RawMessage]{Type: "ListedButNotWired"})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INTERNAL_ERROR"`)
	require.Equal(t, 1, log.errors)
	assert.Equal(t, "ListedButNotWired", log.lastFields["event_type"])
}
