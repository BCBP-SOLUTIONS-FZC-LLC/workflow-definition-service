package handler_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type planTableOf map[string]*int64

func (p planTableOf) ReadPlanTable(context.Context) (map[string]*int64, error) { return p, nil }

func TestPromoteVersionHTTP_FirstActivationOnAPlanMissingFromAReadableTable_AnswersUnavailableNeverADefaultPlan(t *testing.T) {
	t.Parallel()
	five := int64(5)
	f := w7bNewFixture(t)
	w7bExpectFirstActivationReads(f, 1)
	limits := service.NewPlanLimitService(service.PlanLimitDeps{
		Reader: planTableOf{"starter": &five}, TTL: time.Minute,
	})

	w := do(f.router(limits), w7bPromoteRequest("zzq_unknown"))

	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	p := w7bDecodeProblem(t, w)
	assert.Equal(t, w7bTypeUnavailable, p.Type)
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", p.Code)
	assert.Equal(t, w7bDetailUnavailable, p.Detail)
	assert.NotContains(t, w.Body.String(), "zzq_unknown", "the plan code stays out of the response")
	assert.Empty(t, f.outcomes(), "no transaction is opened, so no active version is written")
	w7bRequireOneErrorLog(t, f.log, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", w7bVersionPath("promote"), `plan "zzq_unknown" is not in the plan table`)
}
