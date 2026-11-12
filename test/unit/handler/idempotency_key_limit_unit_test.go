package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithIdempotency_KeyLimitIsCountedInBytesAndTheMessageSaysBytes(t *testing.T) {
	called := 0
	r := newMemRouter(newMemCache(), func(c *gin.Context) { called++; c.Status(http.StatusOK) })

	cases := []struct {
		name string
		key  string
		want int
	}{
		{"256 ascii bytes", strings.Repeat("k", 256), http.StatusOK},
		{"257 ascii bytes", strings.Repeat("k", 257), http.StatusBadRequest},
		{"129 two-byte characters are 258 bytes", strings.Repeat("é", 129), http.StatusBadRequest},
		{"128 two-byte characters are 256 bytes", strings.Repeat("é", 128), http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(r, idemReq(tc.key, uuid.New()))

			require.Equal(t, tc.want, w.Code, w.Body.String())
			if tc.want == http.StatusBadRequest {
				prob := decodeProblem(t, w)
				assert.Equal(t, "BAD_REQUEST", prob["code"])
				assert.Equal(t, "Idempotency-Key must be at most 256 bytes", prob["detail"])
			}
		})
	}
	assert.Equal(t, 2, called, "only the two keys within the limit reach the handler")
}
