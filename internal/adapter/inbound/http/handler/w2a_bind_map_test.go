package handler

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type w2aBindTarget struct {
	Name string `json:"name" binding:"required"`
}

func w2aBindContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/w2a-bind", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

func TestBindStrictJSON_EmptyOrWhitespaceBody_AnswersBadRequestNotValidJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "empty body", body: ""},
		{name: "spaces only", body: "    "},
		{name: "newline and tab only", body: "\n\t\r\n"},
		{name: "mixed whitespace", body: " \t\n \r "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, w := w2aBindContext(tc.body)
			var target w2aBindTarget

			err := bindStrictJSON(c, &target)

			require.Error(t, err)
			assert.ErrorIs(t, err, io.EOF, "an empty body is reported as end of input, not as a field error")
			assert.Empty(t, target.Name)

			(&Handler{}).bindErrResponse(c, err)

			require.Equal(t, http.StatusBadRequest, w.Code)
			p := decodeProblem(t, w.Body.Bytes())
			assert.Equal(t, CodeBadRequest, p.Code)
			assert.Equal(t, "request body is not valid JSON", p.Detail)
			assert.Equal(t, http.StatusBadRequest, p.Status)
			assert.Equal(t, "/w2a-bind", p.Instance)
			assert.Empty(t, p.InvalidParams)
		})
	}
}

func TestBindStrictJSON_JSONWrappedInWhitespace_Binds(t *testing.T) {
	t.Parallel()
	c, _ := w2aBindContext("  \n{\"name\":\"x\"}\t\n")
	var target w2aBindTarget

	require.NoError(t, bindStrictJSON(c, &target))

	assert.Equal(t, "x", target.Name)
}

func TestMapErr_ErrorWrappingTwoSentinels_TakesTheEarlierCase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   ErrCode
		wantDetail string
	}{
		{
			name:       "not found before forbidden, joined with errors.Join",
			err:        errors.Join(domain.ErrForbidden, domain.ErrNotFound),
			wantStatus: http.StatusNotFound,
			wantCode:   CodeNotFound,
		},
		{
			name:       "forbidden before duplicate key",
			err:        errors.Join(domain.ErrDuplicateTemplateKey, domain.ErrForbidden),
			wantStatus: http.StatusForbidden,
			wantCode:   CodeForbidden,
		},
		{
			name:       "draft concurrency before invalid version status",
			err:        errors.Join(domain.ErrInvalidVersionStatus, domain.ErrDraftConcurrency),
			wantStatus: http.StatusConflict,
			wantCode:   CodeDraftConcurrency,
		},
		{
			name:       "key replay before request in progress",
			err:        errors.Join(domain.ErrIdempotencyInProgress, domain.ErrIdempotencyKeyReplay),
			wantStatus: http.StatusConflict,
			wantCode:   CodeIdempotencyReplay,
		},
		{
			name:       "upstream unavailable before malformed BPMN",
			err:        errors.Join(domain.ErrMalformedBPMN, domain.ErrUpstreamUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   CodeUpstream,
			wantDetail: "an upstream dependency is temporarily unavailable",
		},
		{
			name:       "malformed BPMN before an unknown error",
			err:        errors.Join(errors.New("w2a unknown failure"), domain.ErrMalformedBPMN),
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeInvalidBPMNXML,
			wantDetail: "the BPMN document could not be parsed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, code, detail, _ := mapErr(tc.err)

			assert.Equal(t, tc.wantStatus, status)
			assert.Equal(t, tc.wantCode, code)
			if tc.wantDetail != "" {
				assert.Equal(t, tc.wantDetail, detail)
			}
		})
	}
}
