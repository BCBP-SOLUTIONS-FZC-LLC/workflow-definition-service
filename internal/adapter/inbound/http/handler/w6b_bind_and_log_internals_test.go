package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type w6bLogRecord struct {
	msg    string
	fields map[string]any
}

type w6bRecordingLogger struct {
	mu      sync.Mutex
	errs    []w6bLogRecord
	warnLen int
}

func (l *w6bRecordingLogger) Debug(string, map[string]any) { /* not asserted */ }
func (l *w6bRecordingLogger) Info(string, map[string]any)  { /* not asserted */ }
func (l *w6bRecordingLogger) Fatal(string, map[string]any) { /* not asserted */ }

func (l *w6bRecordingLogger) Warn(string, map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnLen++
}

func (l *w6bRecordingLogger) Error(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, w6bLogRecord{msg: msg, fields: fields})
}

var _ port.Logger = (*w6bRecordingLogger)(nil)

func w6bContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, nil)
	return c, w
}

func TestErrResponse_ServerErrorWithoutRequestContext_LogsUnknownTenantAndUserWithTraceID(t *testing.T) {
	traceID := trace.TraceID{0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19}
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	})
	require.True(t, spanCtx.IsValid())

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"unexpected error", errors.New("pool exhausted"), http.StatusInternalServerError, "INTERNAL_ERROR"},
		{"upstream unavailable", fmt.Errorf("calling execution: %w", domain.ErrUpstreamUnavailable), http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := &w6bRecordingLogger{}
			c, w := w6bContext(http.MethodGet, "/w6b/some/path")
			c.Request = c.Request.WithContext(trace.ContextWithSpanContext(c.Request.Context(), spanCtx))

			errResponse(c, log, tc.err)

			require.Equal(t, tc.wantStatus, w.Code)
			require.Len(t, log.errs, 1)
			rec := log.errs[0]
			assert.Equal(t, "request failed", rec.msg)
			assert.Equal(t, map[string]any{
				"status":    tc.wantStatus,
				"code":      tc.wantCode,
				"error":     tc.err.Error(),
				"tenant_id": "unknown",
				"user_id":   "unknown",
				"path":      "/w6b/some/path",
				"trace_id":  traceID.String(),
			}, rec.fields)
		})
	}
}

func TestErrResponse_ServerErrorWithoutSpan_LogsEmptyTraceID(t *testing.T) {
	log := &w6bRecordingLogger{}
	c, _ := w6bContext(http.MethodGet, "/w6b/no-span")

	errResponse(c, log, errors.New("boom"))

	require.Len(t, log.errs, 1)
	assert.Equal(t, "", log.errs[0].fields["trace_id"])
}

func TestRejectInvalidText_CountsRunesNotBytes(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		max      int
		wantFail bool
	}{
		{"five two-byte runes at a limit of five", strings.Repeat("é", 5), 5, false},
		{"six two-byte runes at a limit of five", strings.Repeat("é", 6), 5, true},
		{"three four-byte runes at a limit of three", strings.Repeat("😀", 3), 3, false},
		{"four four-byte runes at a limit of three", strings.Repeat("😀", 4), 3, true},
		{"a combining sequence counts each rune", "éé", 3, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, w := w6bContext(http.MethodPost, "/w6b/text")

			rejected := rejectInvalidText(c, lengthLimit{field: "title", value: tc.value, max: tc.max})

			assert.Equal(t, tc.wantFail, rejected)
			if !tc.wantFail {
				assert.Equal(t, http.StatusOK, w.Code, "nothing is written when the text fits")
				assert.Empty(t, w.Body.String(), "no body is written when the text fits")
				return
			}
			require.Equal(t, http.StatusUnprocessableEntity, w.Code)
			p := decodeProblem(t, w.Body.Bytes())
			assert.Equal(t, CodeInvalidInput, p.Code)
			assert.Equal(t, "request fields exceed maximum length", p.Detail)
			assert.Equal(t, []invalidParam{{Name: "title", Reason: fmt.Sprintf("must not exceed %d characters", tc.max)}}, p.InvalidParams)
		})
	}
}

type w6bValidated struct {
	Name   string `validate:"required"`
	Kind   string `validate:"oneof=alpha beta"`
	Ceil   int    `validate:"max=3"`
	Always string `validate:"eq=fixed"`
}

func TestDescribeBindError_ValidatorFailure_RequiredTagIsRequiredAndOthersAreNotValid(t *testing.T) {
	err := validator.New().Struct(w6bValidated{Name: "", Kind: "gamma", Ceil: 9, Always: "other"})
	require.Error(t, err)

	detail, params := describeBindError(fmt.Errorf("validate request body: %w", err))

	assert.Equal(t, "request body has missing or invalid fields", detail)
	assert.Equal(t, []invalidParam{
		{Name: "Name", Reason: "is required"},
		{Name: "Kind", Reason: "is not valid"},
		{Name: "Ceil", Reason: "is not valid"},
		{Name: "Always", Reason: "is not valid"},
	}, params)
}

func TestDescribeBindError_ErrorOfNoKnownKind_GivesTheDefaultDetailAndNoParams(t *testing.T) {
	for _, err := range []error{
		errors.New("boom"),
		fmt.Errorf("decode request body: %w", errors.New("unexpected token")),
		errTrailingData,
	} {
		t.Run(err.Error(), func(t *testing.T) {
			detail, params := describeBindError(err)

			assert.Equal(t, "request body is not valid JSON", detail)
			assert.Nil(t, params)
		})
	}
}

func TestBindErrResponse_NilLogger_WritesProblemAndSkipsLogging(t *testing.T) {
	c, w := w6bContext(http.MethodPost, "/w6b/bind")

	(&Handler{}).bindErrResponse(c, errors.New("anything"))

	require.Equal(t, http.StatusBadRequest, w.Code)
	p := decodeProblem(t, w.Body.Bytes())
	assert.Equal(t, CodeBadRequest, p.Code)
	assert.Equal(t, "request body is not valid JSON", p.Detail)
}

type w6bLeaf struct {
	Name string `json:"name"`
}

type w6bTarget struct {
	ID       string                          `json:"id"`
	Leaf     *w6bLeaf                        `json:"leaf"`
	Leaves   []w6bLeaf                       `json:"leaves"`
	Matrix   [][]w6bLeaf                     `json:"matrix"`
	Fixed    [2]w6bLeaf                      `json:"fixed"`
	Tags     []string                        `json:"tags"`
	Raw      []byte                          `json:"raw_bytes"`
	RawPtr   *json.RawMessage                `json:"raw_ptr"`
	Strs     map[string]string               `json:"strs"`
	Nested   map[string]map[string]w6bLeaf   `json:"nested"`
	Lists    map[string][]w6bLeaf            `json:"lists"`
	Free     any                             `json:"free"`
	FreeList []any                           `json:"free_list"`
	Ptrs     map[string]*w6bLeaf             `json:"ptrs"`
	Deep     map[string]map[string][]w6bLeaf `json:"deep"`
}

func TestCollectUnknown_RawMessageMapKeysAndMismatchedShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"a raw message field may hold any object", `{"raw_ptr":{"anything":{"at":["all"]}}}`, nil},
		{"a raw message field may hold any list", `{"raw_ptr":[{"x":1},{"y":[{"z":2}]}]}`, nil},
		{"a byte slice field given an object is left to the decoder", `{"raw_bytes":{"x":1}}`, nil},
		{"map keys are data, never reported", `{"strs":{"anything":"a","else":"b"},"nested":{"k1":{"k2":{"name":"n"}}}}`, nil},
		{"map values of non-struct type hold any shape", `{"strs":{"a":{"x":1}}}`, nil},
		{"unknown key under a nested map value", `{"nested":{"k1":{"k2":{"zzq_unknown":"n"}}}}`, []string{"nested.k1.k2.zzq_unknown"}},
		{"unknown key in a list held by a map", `{"lists":{"k":[{"name":"a"},{"other":"b"}]}}`, []string{"lists.k[1].other"}},
		{"unknown key three maps deep", `{"deep":{"a":{"b":[{"name":"n","bad":1}]}}}`, []string{"deep.a.b[0].bad"}},
		{"unknown key behind a pointer value in a map", `{"ptrs":{"k":{"extra":1}}}`, []string{"ptrs.k.extra"}},
		{"list items of a non-struct type are ignored", `{"tags":[{"x":1},["y"],"z"]}`, nil},
		{"an object given where a list is declared is ignored", `{"leaves":{"x":1}}`, nil},
		{"a list given where an object is declared is ignored", `{"leaf":[{"x":1}]}`, nil},
		{"an object given where a string is declared is ignored", `{"id":{"x":1}}`, nil},
		{"a list given where a string is declared is ignored", `{"id":[{"x":1}]}`, nil},
		{"any-typed fields hold any shape", `{"free":{"a":{"b":1}},"free_list":[{"a":1},[{"b":2}]]}`, nil},
		{"nested lists report the index of each level", `{"matrix":[[{"name":"a"}],[{"name":"b"},{"bad":1}]]}`, []string{"matrix[1][1].bad"}},
		{"a fixed-size array is walked like a list", `{"fixed":[{"name":"a"},{"bad":1}]}`, []string{"fixed[1].bad"}},
		{"a null value is ignored", `{"leaf":null,"leaves":null,"strs":null}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkKnownFields([]byte(tc.body), &w6bTarget{})

			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			var unknown *unknownFieldsError
			require.True(t, errors.As(err, &unknown), "error %v", err)
			assert.Equal(t, tc.want, unknown.names)
		})
	}
}

func TestCheckKnownFields_UnknownNameOverSixtyFourRunes_IsCutToSixtyFour(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"exactly 64 ascii characters stay whole", fmt.Sprintf(`{%q:1}`, strings.Repeat("a", 64)), strings.Repeat("a", 64)},
		{"65 ascii characters are cut to 64", fmt.Sprintf(`{%q:1}`, strings.Repeat("a", 65)), strings.Repeat("a", 64)},
		{"a hundred characters are cut to 64", fmt.Sprintf(`{%q:1}`, strings.Repeat("b", 100)), strings.Repeat("b", 64)},
		{"64 two-byte runes stay whole", fmt.Sprintf(`{%q:1}`, strings.Repeat("é", 64)), strings.Repeat("é", 64)},
		{"65 two-byte runes are cut at a rune boundary", fmt.Sprintf(`{%q:1}`, strings.Repeat("é", 65)), strings.Repeat("é", 64)},
		{"the whole path counts, not only the last key", fmt.Sprintf(`{"leaf":{%q:1}}`, strings.Repeat("c", 70)), "leaf." + strings.Repeat("c", 59)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkKnownFields([]byte(tc.body), &w6bTarget{})

			var unknown *unknownFieldsError
			require.True(t, errors.As(err, &unknown), "error %v", err)
			assert.Equal(t, []string{tc.want}, unknown.names)
			assert.LessOrEqual(t, len([]rune(unknown.names[0])), 64)
		})
	}
}
