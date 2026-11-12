package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/valkey"
	bpmncompiler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

type w11dWarning struct {
	msg    string
	fields map[string]any
}

type w11dWarnLog struct {
	testkit.FakeLogger
	mu    sync.Mutex
	warns []w11dWarning
}

func (l *w11dWarnLog) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, w11dWarning{msg, fields})
}

type w11dSetCall struct {
	key   string
	value interface{}
	ttl   time.Duration
}

type w11dClient struct {
	redis.Cmdable
	getErr  error
	setErr  error
	delErr  error
	evalErr error

	mu       sync.Mutex
	setCalls []w11dSetCall
	delCalls [][]string
	setNXArg []string
	evalKeys [][]string
	evalArgs [][]interface{}
}

func (c *w11dClient) Get(context.Context, string) *redis.StringCmd {
	if c.getErr != nil {
		return redis.NewStringResult("", c.getErr)
	}
	return redis.NewStringResult("", redis.Nil)
}

func (c *w11dClient) Set(_ context.Context, key string, value interface{}, ttl time.Duration) *redis.StatusCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setCalls = append(c.setCalls, w11dSetCall{key, value, ttl})
	if c.setErr != nil {
		return redis.NewStatusResult("", c.setErr)
	}
	return redis.NewStatusResult("OK", nil)
}

func (c *w11dClient) Del(_ context.Context, keys ...string) *redis.IntCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delCalls = append(c.delCalls, keys)
	if c.delErr != nil {
		return redis.NewIntResult(0, c.delErr)
	}
	return redis.NewIntResult(int64(len(keys)), nil)
}

func (c *w11dClient) SetNX(_ context.Context, key string, value interface{}, _ time.Duration) *redis.BoolCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setNXArg = []string{key, value.(string)}
	return redis.NewBoolResult(true, nil)
}

func (c *w11dClient) Eval(_ context.Context, _ string, keys []string, args ...interface{}) *redis.Cmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evalKeys = append(c.evalKeys, keys)
	c.evalArgs = append(c.evalArgs, args)
	if c.evalErr != nil {
		return redis.NewCmdResult(nil, c.evalErr)
	}
	return redis.NewCmdResult(int64(1), nil)
}

func w11dPlanService(client *w11dClient, versions *planVersions, log *w11dWarnLog) *service.CompiledPlanService {
	return service.NewCompiledPlanService(service.CompiledPlanDeps{
		Versions: versions, Cache: valkey.NewCache(client), CacheTTL: time.Hour, Log: log,
	})
}

func TestCompiledPlan_ValkeyAdapterErrors_AreLoggedWithTheAdapterPrefixAndThePlanComesFromTheDatabase(t *testing.T) {
	cause := errors.New("connection reset by peer")
	tenantID, versionID, workflowID := uuid.New(), uuid.New(), uuid.New()
	key := domain.CompiledPlanCacheKey(tenantID, versionID)

	cases := []struct {
		name       string
		client     *w11dClient
		revoked    bool
		wantMsg    string
		wantError  string
		wantReads  int
		wantSets   int
		wantDelKey []string
	}{
		{
			name:      "get fails",
			client:    &w11dClient{getErr: cause},
			wantMsg:   "compiled-plan cache read failed",
			wantError: "valkey get: " + cause.Error(),
			wantReads: 2,
			wantSets:  1,
		},
		{
			name:      "set fails",
			client:    &w11dClient{setErr: cause},
			wantMsg:   "compiled-plan cache write failed",
			wantError: "valkey set: " + cause.Error(),
			wantReads: 1,
			wantSets:  1,
		},
		{
			name:       "del fails while evicting a revoked version",
			client:     &w11dClient{delErr: cause},
			revoked:    true,
			wantMsg:    "compiled-plan cache eviction failed",
			wantError:  "valkey del: " + cause.Error(),
			wantReads:  2,
			wantSets:   1,
			wantDelKey: []string{key},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			log := &w11dWarnLog{}
			versions := &planVersions{}
			versions.get = func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
				v := publishedPlanVersion(versionID, workflowID, `{"schema_version":1}`)
				if tc.revoked && versions.reads > 1 {
					v.IsValid = false
				}
				return v, nil
			}

			got, err := w11dPlanService(tc.client, versions, log).Get(context.Background(), tenantID, versionID)

			require.NoError(t, err)
			require.NotNil(t, got.PlanJSON)
			assert.Equal(t, `{"schema_version":1}`, *got.PlanJSON)
			assert.Equal(t, tc.wantReads, versions.reads)
			require.Len(t, log.warns, 1)
			assert.Equal(t, tc.wantMsg, log.warns[0].msg)
			assert.Equal(t, map[string]any{"key": key, "error": tc.wantError}, log.warns[0].fields)
			require.Len(t, tc.client.setCalls, tc.wantSets)
			assert.Equal(t, key, tc.client.setCalls[0].key)
			assert.Equal(t, time.Hour, tc.client.setCalls[0].ttl)
			assert.Equal(t, tc.wantDelKey, w11dFirstCall(tc.client.delCalls))
		})
	}
}

func w11dFirstCall(calls [][]string) []string {
	if len(calls) == 0 {
		return nil
	}
	return calls[0]
}

func TestDraftService_Update_WhenReleasingTheLockFails_StillSavesAndReturnsTheDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()
	client := &w11dClient{evalErr: errors.New("connection reset by peer")}
	vers := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewDraftService(service.DraftDeps{
		Cache:     valkey.NewCache(client),
		Workflows: mocks.NewMockWorkflowRepository(ctrl),
		Versions:  vers,
		Assignees: mocks.NewMockAssigneeRepository(ctrl),
		Compiler:  bpmncompiler.New(),
	})
	draftID := uuid.New()
	vers.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(&domain.WorkflowVersion{ID: draftID, RecordVersion: 4}, nil)
	var saved *domain.WorkflowVersion
	vers.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, v *domain.WorkflowVersion) error {
			saved = v
			return nil
		})

	got, err := svc.Update(context.Background(), tenantID, userID, wfID, service.UpdateDraftReq{RecordVersion: 4})

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, draftID, got.ID)
	assert.Same(t, saved, got)
	wantKey := "draft-lock:" + tenantID.String() + ":" + wfID.String()
	require.Len(t, client.evalKeys, 1, "the release was attempted once and its failure did not stop the request")
	assert.Equal(t, []string{wantKey}, client.evalKeys[0])
	assert.Equal(t, []string{wantKey, client.setNXArg[1]}, []string{client.setNXArg[0], client.evalArgs[0][0].(string)})
}
