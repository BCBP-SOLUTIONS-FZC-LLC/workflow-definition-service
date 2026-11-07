package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouter_HealthzRequestID_IsEchoedOrGenerated(t *testing.T) {
	cases := []struct {
		name      string
		sent      string
		wantEcho  bool
		wantValue string
	}{
		{name: "caller supplied id is echoed", sent: "abc-123", wantEcho: true, wantValue: "abc-123"},
		{name: "missing id is generated as a uuid", sent: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := newUnroutedEngine("test")
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			if tc.sent != "" {
				req.Header.Set("X-Request-ID", tc.sent)
			}
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			got := w.Header().Get("X-Request-ID")
			if tc.wantEcho {
				assert.Equal(t, tc.wantValue, got)
				return
			}
			_, err := uuid.Parse(got)
			assert.NoError(t, err, "generated request id %q must be a UUID", got)
		})
	}
}
