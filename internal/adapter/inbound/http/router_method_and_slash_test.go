package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type methodProblem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
	Code     string `json:"code"`
}

func TestRouter_AWrongMethodOnARegisteredPath_Answers405WithAnAllowHeader(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		target    string
		wantAllow []string
	}{
		{"probe path", http.MethodPost, "/healthz", []string{"GET"}},
		{"head on the probe path", http.MethodHead, "/healthz", []string{"GET"}},
		{"readiness path", http.MethodDelete, "/readyz", []string{"GET"}},
		{"collection with two methods", http.MethodPut, "/api/v1/workflows", []string{"GET", "POST"}},
		{"member path", http.MethodPost, "/api/v1/workflows/11111111-1111-1111-1111-111111111111", []string{"GET"}},
		{"internal events path", http.MethodGet, "/api/v1/internal/events", []string{"POST"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := newUnroutedEngine("test")
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, httptest.NewRequest(tc.method, tc.target, strings.NewReader("{}")))

			require.Equal(t, http.StatusMethodNotAllowed, w.Code, w.Body.String())
			assert.ElementsMatch(t, tc.wantAllow, strings.Split(w.Header().Get("Allow"), ", "))
			assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
			var p methodProblem
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
			assert.Equal(t, "https://api.workflow.platform/errors/method-not-allowed", p.Type)
			assert.Equal(t, "Method Not Allowed", p.Title)
			assert.Equal(t, http.StatusMethodNotAllowed, p.Status)
			assert.Equal(t, "METHOD_NOT_ALLOWED", p.Code)
			assert.Equal(t, "the method is not allowed for this path", p.Detail)
			assert.Equal(t, tc.target, p.Instance)
		})
	}
}

func TestRouter_AWrongMethodAnswers405BeforeAuthentication(t *testing.T) {
	engine := newUnroutedEngine("test")
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/workflows", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "no identity headers and no system caller identity, yet not a 401")
}

func TestRouter_ATrailingSlashIsNotRedirected_ItAnswers404(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
	}{
		{"probe path", http.MethodGet, "/healthz/"},
		{"readiness path", http.MethodGet, "/readyz/"},
		{"collection read", http.MethodGet, "/api/v1/workflows/"},
		{"collection write", http.MethodPost, "/api/v1/workflows/"},
		{"internal events", http.MethodPost, "/api/v1/internal/events/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := newUnroutedEngine("test")
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, httptest.NewRequest(tc.method, tc.target, strings.NewReader("{}")))

			require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
			assert.Empty(t, w.Header().Get("Location"))
			body := decodeUnroutedProblem(t, w)
			assert.Equal(t, "NOT_FOUND", body.Code)
			assert.Equal(t, tc.target, body.Instance)
		})
	}
}

func TestRouter_TheProbesStillAnswerOnTheirExactPaths(t *testing.T) {
	for _, target := range []string{"/healthz", "/readyz"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			engine := newUnroutedEngine("test")
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))

			assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		})
	}
}

func TestRouter_AWrongMethodOnTheDevOnlyPages_Answers405InDev(t *testing.T) {
	for _, target := range []string{"/asyncapi", "/swagger/index.html"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			engine := newUnroutedEngine("dev")
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, target, nil))

			require.Equal(t, http.StatusMethodNotAllowed, w.Code, w.Body.String())
			assert.Equal(t, "GET", w.Header().Get("Allow"))
			assert.Equal(t, "METHOD_NOT_ALLOWED", decodeUnroutedProblem(t, w).Code)
		})
	}
}
