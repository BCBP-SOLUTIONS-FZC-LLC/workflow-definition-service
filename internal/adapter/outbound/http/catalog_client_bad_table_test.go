package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestCatalogClient_APlanTableThatCouldWidenOrBlockCreationFailsTheWholeRead(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"a negative limit": {
			body: `{"plans":[{"code":"starter","workflow_template_limit":5},{"code":"pro","workflow_template_limit":-1}]}`,
			want: "negative",
		},
		"a limit given as text": {
			body: `{"plans":[{"code":"pro","workflow_template_limit":"ten"}]}`,
			want: "unreadable workflow_template_limit",
		},
		"a limit given as an object": {
			body: `{"plans":[{"code":"pro","workflow_template_limit":{"max":5}}]}`,
			want: "unreadable workflow_template_limit",
		},
		"a fractional limit": {
			body: `{"plans":[{"code":"pro","workflow_template_limit":1.5}]}`,
			want: "unreadable workflow_template_limit",
		},
		"a limit beyond the 64-bit range": {
			body: `{"plans":[{"code":"pro","workflow_template_limit":9223372036854775808}]}`,
			want: "unreadable workflow_template_limit",
		},
		"two codes that are the same after trimming and lower-casing": {
			body: `{"plans":[{"code":"Pro","workflow_template_limit":5},{"code":" pro ","workflow_template_limit":null}]}`,
			want: "more than once",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			table, err := NewCatalogClient(srv.URL, time.Second).ReadPlanTable(context.Background())

			assert.Nil(t, table, "no table is returned from a read that failed")
			assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			if err != nil {
				assert.Contains(t, err.Error(), tc.want)
			}
		})
	}
}
