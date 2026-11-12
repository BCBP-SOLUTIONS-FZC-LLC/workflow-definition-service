package membershipclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestMembershipClient_CheckEligibility_A200WithAnUnreadableBodyIsUnavailable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "not JSON", body: "not-json"},
		{name: "empty body", body: ""},
		{name: "truncated JSON", body: `{"eligible": tr`},
		{name: "eligible of the wrong type", body: `{"eligible": "yes"}`},
		{name: "a JSON array", body: `[]`},
		{name: "no eligible flag", body: `{"reason": "x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			client := httpadapter.NewMembershipClient(srv.URL, 10*time.Second)

			got, err := client.CheckEligibility(context.Background(), uuid.New(), uuid.New(), "dept-1", "approver")

			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.False(t, got.Eligible, "an unreadable answer is never eligible")
			assert.EqualValues(t, 1, calls.Load(), "an unreadable body is not retried")
		})
	}
}
