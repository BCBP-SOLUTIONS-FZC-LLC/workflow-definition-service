package service_test

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestValidationService_Validate_Valid(t *testing.T) {
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})

	compiler.EXPECT().Validate(gomock.Any(), "<bpmn/>").Return(nil, nil)

	isValid, errs, err := svc.Validate(context.Background(), "<bpmn/>")
	if err != nil || !isValid || len(errs) != 0 {
		t.Fatalf("expected valid=true, no errs: %v %v %v", isValid, errs, err)
	}
}

func TestValidationService_Validate_Invalid(t *testing.T) {
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})

	validationErrs := []domain.BPMNValidationError{
		{Code: domain.BPMNErrNoStartEvent, Message: "missing start event"},
	}
	compiler.EXPECT().Validate(gomock.Any(), "<bad/>").Return(validationErrs, nil)

	isValid, errs, err := svc.Validate(context.Background(), "<bad/>")
	if err != nil || isValid || len(errs) != 1 {
		t.Fatalf("expected valid=false, 1 err: %v %v %v", isValid, errs, err)
	}
	if errs[0].Code != domain.BPMNErrNoStartEvent {
		t.Errorf("unexpected error code: %s", errs[0].Code)
	}
}

func TestValidationService_Validate_CompilerError(t *testing.T) {
	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler})

	compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, errors.New("parse error"))

	isValid, _, err := svc.Validate(context.Background(), "<broken/>")
	if err == nil || isValid {
		t.Fatal("expected error and isValid=false")
	}
}
