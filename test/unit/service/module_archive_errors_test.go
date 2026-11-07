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

func TestModuleService_Archive_FailurePropagatesSentinel(t *testing.T) {
	outage := fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable)
	cases := []struct {
		name    string
		ownTx   bool
		setup   func(m moduleMocks, tenantID, moduleID, activeID uuid.UUID)
		wantErr error
	}{
		{
			name: "module vanishes before the transaction re-read",
			setup: func(m moduleMocks, tenantID, moduleID, activeID uuid.UUID) {
				gomock.InOrder(
					m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
						Return(&domain.Module{ID: moduleID, Scope: domain.ScopeTenant, ActiveVersionID: &activeID}, nil),
					m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
						Return(nil, domain.ErrNotFound),
				)
			},
			wantErr: domain.ErrNotFound,
		},
		{
			name: "active version row is gone when it is archived",
			setup: func(m moduleMocks, tenantID, moduleID, activeID uuid.UUID) {
				m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
					Return(&domain.Module{ID: moduleID, Scope: domain.ScopeTenant, ActiveVersionID: &activeID}, nil).Times(2)
				m.versions.EXPECT().Archive(gomock.Any(), tenantID, activeID).Return(domain.ErrNotFound)
			},
			wantErr: domain.ErrNotFound,
		},
		{
			name: "database unavailable on the first lookup",
			setup: func(m moduleMocks, tenantID, moduleID, _ uuid.UUID) {
				m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).Return(nil, outage)
			},
			wantErr: domain.ErrUpstreamUnavailable,
		},
		{
			name: "database unavailable inside the transaction",
			setup: func(m moduleMocks, tenantID, moduleID, activeID uuid.UUID) {
				m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
					Return(&domain.Module{ID: moduleID, Scope: domain.ScopeTenant, ActiveVersionID: &activeID}, nil).Times(2)
				m.versions.EXPECT().Archive(gomock.Any(), tenantID, activeID).Return(outage)
			},
			wantErr: domain.ErrUpstreamUnavailable,
		},
		{
			name:  "transaction cannot start",
			ownTx: true,
			setup: func(m moduleMocks, tenantID, moduleID, activeID uuid.UUID) {
				m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
					Return(&domain.Module{ID: moduleID, Scope: domain.ScopeTenant, ActiveVersionID: &activeID}, nil)
				m.tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).Return(outage)
			},
			wantErr: domain.ErrUpstreamUnavailable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			svc, m := newModuleService(ctrl)
			if !tc.ownTx {
				passthrough(m.tx)
			}
			tenantID, moduleID, activeID := uuid.New(), uuid.New(), uuid.New()
			tc.setup(m, tenantID, moduleID, activeID)

			err := svc.Archive(context.Background(), tenantID, uuid.New(), moduleID)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Archive() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestModuleService_Get_FailurePropagatesSentinel(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(m moduleMocks, tenantID, moduleID, activeID uuid.UUID)
		wantErr error
	}{
		{
			name: "module lookup fails with a non-sentinel error",
			setup: func(m moduleMocks, tenantID, moduleID, _ uuid.UUID) {
				m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).Return(nil, errArchiveScanFailed)
			},
			wantErr: errArchiveScanFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			svc, m := newModuleService(ctrl)
			tenantID, moduleID, activeID := uuid.New(), uuid.New(), uuid.New()
			tc.setup(m, tenantID, moduleID, activeID)

			_, _, err := svc.Get(context.Background(), tenantID, moduleID)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Get() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

var errArchiveScanFailed = errors.New("scan failed")

func TestModuleService_Archive_ModuleStillUsedByWorkflows_Archives(t *testing.T) {
	cases := []struct {
		name string
	}{
		{name: "no usage lookup stands in the way"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			svc, m := newLibraryModuleService(ctrl)
			passthrough(m.tx)
			tenantID, moduleID, activeID := uuid.New(), uuid.New(), uuid.New()
			m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
				Return(&domain.Module{ID: moduleID, Scope: domain.ScopeTenant, ActiveVersionID: &activeID}, nil).Times(2)
			m.versions.EXPECT().Archive(gomock.Any(), tenantID, activeID).Return(nil)
			m.modules.EXPECT().UpdateActiveVersion(gomock.Any(), moduleID, (*uuid.UUID)(nil)).Return(nil)

			if err := svc.Archive(context.Background(), tenantID, uuid.New(), moduleID); err != nil {
				t.Fatalf("Archive() = %v, want nil: a module in use by workflows may still be archived", err)
			}
		})
	}
}
