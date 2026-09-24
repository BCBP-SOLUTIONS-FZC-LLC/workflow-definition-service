package service_test

import (
	"context"
	"errors"
	"testing"

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

	compiler.EXPECT().Bundle("<bpmn/>", []string(nil)).Return("<bpmn/>", nil)
	compiler.EXPECT().Validate(gomock.Any(), "<bpmn/>").Return(nil, nil)

	isValid, errs, err := svc.Validate(context.Background(), "<bpmn/>", nil)
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
	compiler.EXPECT().Bundle("<bad/>", []string(nil)).Return("<bad/>", nil)
	compiler.EXPECT().Validate(gomock.Any(), "<bad/>").Return(validationErrs, nil)

	isValid, errs, err := svc.Validate(context.Background(), "<bad/>", nil)
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
	compiler.EXPECT().Bundle("<warn/>", []string(nil)).Return("<warn/>", nil)
	compiler.EXPECT().Validate(gomock.Any(), "<warn/>").Return(validationErrs, nil)

	isValid, errs, err := svc.Validate(context.Background(), "<warn/>", nil)
	if err != nil || !isValid || len(errs) != 1 {
		t.Fatalf("expected valid=true (warnings-only), 1 err: %v %v %v", isValid, errs, err)
	}
}

func TestValidationService_Validate_BundleError(t *testing.T) {
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})

	compiler.EXPECT().Bundle(gomock.Any(), gomock.Any()).Return("", errors.New("malformed module BPMN"))

	isValid, errs, err := svc.Validate(context.Background(), "<bpmn/>", []string{"<bad-module/>"})
	if err == nil || isValid || errs != nil {
		t.Fatalf("expected bundle error and isValid=false, got %v %v %v", isValid, errs, err)
	}
}

func TestValidationService_Validate_CompilerError(t *testing.T) {
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})

	compiler.EXPECT().Bundle(gomock.Any(), gomock.Any()).Return("<broken/>", nil)
	compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, errors.New("parse error"))

	isValid, _, err := svc.Validate(context.Background(), "<broken/>", nil)
	if err == nil || isValid {
		t.Fatal("expected error and isValid=false")
	}
}

func TestValidationService_Validate_InvalidModuleTask_IsInvalid(t *testing.T) {
	svc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New()})

	isValid, errs, err := svc.Validate(context.Background(), moduleMainBPMN, []string{prepReviewModuleNoCandidateUsersBPMN})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if isValid {
		t.Errorf("is_valid = true, want false for a module task with no candidate users")
	}
	found := false
	for _, e := range errs {
		found = found || (e.Code == domain.BPMNErrCandidateUsersEmpty && e.NodeID == "PR_Prep")
	}
	if !found {
		t.Errorf("errors = %+v, want CANDIDATE_USERS_EMPTY on PR_Prep", errs)
	}
}
