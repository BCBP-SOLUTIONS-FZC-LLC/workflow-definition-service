package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestModuleService_Usages_NoActiveVersion_NothingIsBehind(t *testing.T) {
	cases := []struct {
		name   string
		usages []*domain.ModuleUsage
	}{
		{
			name: "usages of several module versions",
			usages: []*domain.ModuleUsage{
				{WorkflowKey: "first", ModuleVersionNumber: 1},
				{WorkflowKey: "second", ModuleVersionNumber: 4},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			svc, m := newLibraryModuleService(ctrl)
			tenantID, moduleID := uuid.New(), uuid.New()
			m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
				Return(&domain.Module{ID: moduleID, ActiveVersionID: nil}, nil)
			m.workflowVersions.EXPECT().ListModuleUsages(gomock.Any(), tenantID, moduleID).Return(tc.usages, nil)

			got, err := svc.Usages(context.Background(), tenantID, moduleID)

			if err != nil {
				t.Fatalf("usages: %v", err)
			}
			if len(got) != len(tc.usages) {
				t.Fatalf("usages = %+v, want %d entries", got, len(tc.usages))
			}
			for _, u := range got {
				if u.Behind {
					t.Errorf("usage %q behind = true, want false: the module has no active version", u.WorkflowKey)
				}
			}
		})
	}
}

func TestModuleService_Usages_LookupFailure_IsReturnedWithoutQueryingUsages(t *testing.T) {
	unavailable := fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable)
	cases := []struct {
		name         string
		moduleErr    error
		hasActive    bool
		versionErr   error
		wantSentinel error
	}{
		{name: "module lookup unavailable", moduleErr: unavailable, wantSentinel: domain.ErrUpstreamUnavailable},
		{name: "active version lookup unavailable", hasActive: true, versionErr: unavailable, wantSentinel: domain.ErrUpstreamUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			svc, m := newLibraryModuleService(ctrl)
			tenantID, moduleID, activeID := uuid.New(), uuid.New(), uuid.New()
			if tc.moduleErr != nil {
				m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).Return(nil, tc.moduleErr)
			} else {
				mod := &domain.Module{ID: moduleID}
				if tc.hasActive {
					mod.ActiveVersionID = &activeID
				}
				m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).Return(mod, nil)
			}
			if tc.versionErr != nil {
				m.versions.EXPECT().GetByID(gomock.Any(), tenantID, activeID).Return(nil, tc.versionErr)
			}
			m.workflowVersions.EXPECT().ListModuleUsages(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

			got, err := svc.Usages(context.Background(), tenantID, moduleID)

			if !errors.Is(err, tc.wantSentinel) {
				t.Errorf("err = %v, want it to wrap %v so the API answers 503", err, tc.wantSentinel)
			}
			if got != nil {
				t.Errorf("usages = %+v, want none on a failed lookup", got)
			}
		})
	}
}
