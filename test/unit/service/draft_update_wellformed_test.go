package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	bpmncompiler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestDraftService_Update_RefusesTextThatIsNotWellFormedXML(t *testing.T) {
	tests := []struct {
		name string
		xml  string
	}{
		{"empty string", ""},
		{"only spaces", "   \n\t"},
		{"plain text", "this is not xml"},
		{"unclosed element", "<definitions><process>"},
		{"mismatched end tag", "<a><b></a></b>"},
		{"two root elements", "<a/><b/>"},
		{"unquoted attribute", "<a id=1/>"},
		{"document type declaration", "<!DOCTYPE a [<!ENTITY x \"y\">]><a/>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			svc := service.NewDraftService(service.DraftDeps{
				Cache:     mocks.NewMockCacheStore(ctrl),
				Workflows: mocks.NewMockWorkflowRepository(ctrl),
				Versions:  mocks.NewMockWorkflowVersionRepository(ctrl),
				Compiler:  bpmncompiler.New(),
			})

			xml := tc.xml
			_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), uuid.New(),
				service.UpdateDraftReq{BPMNXML: &xml, RecordVersion: 3})

			if !errors.Is(err, domain.ErrMalformedBPMN) {
				t.Fatalf("Update err = %v, want ErrMalformedBPMN", err)
			}
		})
	}
}

func TestDraftService_Update_RefusesOversizedTokenCount(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc := service.NewDraftService(service.DraftDeps{
		Cache:     mocks.NewMockCacheStore(ctrl),
		Workflows: mocks.NewMockWorkflowRepository(ctrl),
		Versions:  mocks.NewMockWorkflowVersionRepository(ctrl),
		Compiler:  bpmncompiler.New(),
	})

	xml := "<a>" + strings.Repeat("<b/>", 600_000) + "</a>"
	_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), uuid.New(),
		service.UpdateDraftReq{BPMNXML: &xml, RecordVersion: 3})

	if !errors.Is(err, domain.ErrMalformedBPMN) || !errors.Is(err, domain.ErrForbiddenXML) {
		t.Fatalf("Update err = %v, want ErrMalformedBPMN wrapping ErrForbiddenXML", err)
	}
}

func TestDraftService_Update_SavesAnyWellFormedXMLWithoutSemanticChecks(t *testing.T) {
	tests := []struct {
		name string
		xml  string
	}{
		{"not a BPMN document at all", "<anything-well-formed/>"},
		{"with declaration and comment", "<?xml version=\"1.0\"?><!-- c --><a x=\"1\"><b>text</b></a>"},
		{"surrounding whitespace", "\n  <a/>\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			cache := mocks.NewMockCacheStore(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewDraftService(service.DraftDeps{
				Cache:     cache,
				Workflows: mocks.NewMockWorkflowRepository(ctrl),
				Versions:  vRepo,
				Compiler:  bpmncompiler.New(),
			})
			tenantID, wfID := uuid.New(), uuid.New()
			cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), gomock.Any(), 30*time.Second).Return(true, nil)
			cache.EXPECT().DelIfEqual(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(&domain.WorkflowVersion{ID: uuid.New(), RecordVersion: 3}, nil)
			vRepo.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).Return(nil)

			xml := tc.xml
			got, err := svc.Update(context.Background(), tenantID, uuid.New(), wfID,
				service.UpdateDraftReq{BPMNXML: &xml, RecordVersion: 3})

			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if got.BPMNXML != tc.xml {
				t.Errorf("stored xml = %q, want %q", got.BPMNXML, tc.xml)
			}
		})
	}
}

func TestDraftService_Update_RenameAloneDoesNotNeedXML(t *testing.T) {
	ctrl := gomock.NewController(t)
	cache := mocks.NewMockCacheStore(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewDraftService(service.DraftDeps{Cache: cache, Workflows: wfRepo, Versions: vRepo, Compiler: bpmncompiler.New()})
	tenantID, wfID := uuid.New(), uuid.New()
	cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), gomock.Any(), 30*time.Second).Return(true, nil)
	cache.EXPECT().DelIfEqual(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(&domain.WorkflowVersion{ID: uuid.New(), RecordVersion: 3, BPMNXML: ""}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{Name: "old"}, nil)
	wfRepo.EXPECT().UpdateMetadata(gomock.Any(), tenantID, wfID, "new", gomock.Any()).Return(nil)
	vRepo.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).Return(nil)

	name := "new"
	if _, err := svc.Update(context.Background(), tenantID, uuid.New(), wfID,
		service.UpdateDraftReq{Name: &name, RecordVersion: 3}); err != nil {
		t.Fatalf("Update: %v", err)
	}
}
