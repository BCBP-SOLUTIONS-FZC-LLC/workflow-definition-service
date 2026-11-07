package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSwaggerRoute_DevMode_IndexPageIsServedByTheStockHandlerWithoutIdentity(t *testing.T) {
	engine := newUnroutedEngine("dev")
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	body := w.Body.String()
	assert.Contains(t, body, "swagger-ui", "the stock Swagger UI page must be returned")
	assert.Contains(t, body, "swagger-initializer.js", "the page must load the initializer the router overrides")
	assert.NotContains(t, body, "NOT_FOUND")
}

func TestSwaggerRoute_NonDevMode_IndexPageIsNotRegistered(t *testing.T) {
	t.Parallel()
	for _, env := range []string{"production", "staging", ""} {
		t.Run("env="+env, func(t *testing.T) {
			t.Parallel()
			engine := newUnroutedEngine(env)
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil))

			require.Equal(t, http.StatusNotFound, w.Code)
			body := decodeUnroutedProblem(t, w)
			assert.Equal(t, "NOT_FOUND", body.Code)
			assert.Equal(t, "/swagger/index.html", body.Instance)
			assert.NotContains(t, w.Body.String(), "swagger-ui")
		})
	}
}
