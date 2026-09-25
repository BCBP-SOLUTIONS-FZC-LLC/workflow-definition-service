package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestValidationService_Validate_Valid(t *testing.T) {
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})

	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().Validate(gomock.Any(), "<bpmn/>").Return(nil, nil)

	isValid, errs, err := svc.Validate(context.Background(), uuid.New(), "<bpmn/>")
	if err != nil || !isValid || len(errs) != 0 {
		t.Fatalf("expected valid=true, no errs: %v %v %v", isValid, errs, err)
	}
}

func TestValidationService_Validate_Invalid(t *testing.T) {
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})

	validationErrs := []domain.BPMNValidationError{
		{Code: domain.BPMNErrNoStartEvent, Message: "missing start event", Severity: domain.SeverityError},
	}
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bad/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bad/>", gomock.Any()).Return("<bad/>", nil)
	compiler.EXPECT().Validate(gomock.Any(), "<bad/>").Return(validationErrs, nil)

	isValid, errs, err := svc.Validate(context.Background(), uuid.New(), "<bad/>")
	if err != nil || isValid || len(errs) != 1 {
		t.Fatalf("expected valid=false, 1 err: %v %v %v", isValid, errs, err)
	}
	if errs[0].Code != domain.BPMNErrNoStartEvent {
		t.Errorf("unexpected error code: %s", errs[0].Code)
	}
}

func TestValidationService_Validate_WarningsOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})

	validationErrs := []domain.BPMNValidationError{
		{Code: domain.BPMNWarnUnknownStageType, Message: "unknown stage type", Severity: domain.SeverityWarning},
	}
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<warn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<warn/>", gomock.Any()).Return("<warn/>", nil)
	compiler.EXPECT().Validate(gomock.Any(), "<warn/>").Return(validationErrs, nil)

	isValid, errs, err := svc.Validate(context.Background(), uuid.New(), "<warn/>")
	if err != nil || !isValid || len(errs) != 1 {
		t.Fatalf("expected valid=true (warnings-only), 1 err: %v %v %v", isValid, errs, err)
	}
}

func TestValidationService_Validate_BundleError(t *testing.T) {
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})

	compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(nil, nil, nil)

	compiler.EXPECT().BundleModules(gomock.Any(), gomock.Any()).Return("", errors.New("malformed module BPMN"))

	isValid, errs, err := svc.Validate(context.Background(), uuid.New(), "<bpmn/>")
	if err == nil || isValid || errs != nil {
		t.Fatalf("expected bundle error and isValid=false, got %v %v %v", isValid, errs, err)
	}
}

func TestValidationService_Validate_CompilerError(t *testing.T) {
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})

	compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(nil, nil, nil)
	compiler.EXPECT().BundleModules(gomock.Any(), gomock.Any()).Return("<broken/>", nil)
	compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, errors.New("parse error"))

	isValid, _, err := svc.Validate(context.Background(), uuid.New(), "<broken/>")
	if err == nil || isValid {
		t.Fatal("expected error and isValid=false")
	}
}

func TestValidationService_Validate_InvalidModuleTask_IsInvalid(t *testing.T) {
	ctrl := gomock.NewController(t)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	tenantID := uuid.New()
	expectLibrary(modules, tenantID, map[libraryKey]*domain.ModuleVersion{
		{"Process_PrepReview", 0}: libraryVersion(uuid.New(), "Process_PrepReview", 1, prepReviewModuleInvalidCandidateUserBPMN),
	})
	svc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New(), ModuleVersions: modules})

	isValid, errs, err := svc.Validate(context.Background(), tenantID, moduleMainBPMN)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if isValid {
		t.Errorf("is_valid = true, want false for a module task naming an invalid user")
	}
	found := false
	for _, e := range errs {
		found = found || (e.Code == domain.BPMNErrInvalidCandidateUser && e.NodeID == "PR_Prep")
	}
	if !found {
		t.Errorf("errors = %+v, want INVALID_CANDIDATE_USER on PR_Prep", errs)
	}
}
