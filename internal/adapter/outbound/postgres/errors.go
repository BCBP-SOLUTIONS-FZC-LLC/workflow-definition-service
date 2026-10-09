package postgres

import (
	"errors"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	constraintWorkflowBusinessKey      = "workflow_tenant_id_business_key_key"
	constraintWorkflowVersionDraft     = "idx_wv_single_draft"
	constraintWorkflowVersionPublished = "uq_workflow_version_published"
	constraintModuleVersionDraft       = "idx_mv_single_draft"
	constraintModuleVersionPublished   = "uq_module_version_published"
	constraintModuleProcessIDTenant    = "uq_module_process_id_tenant"
	constraintModuleProcessIDGlobal    = "uq_module_process_id_global"
)

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if pgcommon.IsUniqueViolation(err) {
		switch pgcommon.ConstraintName(err) {
		case constraintWorkflowBusinessKey:
			return domain.ErrDuplicateBusinessKey
		case constraintWorkflowVersionDraft:
			return domain.ErrDraftAlreadyExists
		case constraintWorkflowVersionPublished:
			return domain.ErrDraftConcurrency
		case constraintModuleVersionDraft:
			return domain.ErrDraftAlreadyExists
		case constraintModuleVersionPublished:
			return domain.ErrDraftConcurrency
		case constraintModuleProcessIDTenant, constraintModuleProcessIDGlobal:
			return domain.ErrModuleProcessIDTaken
		}
	}
	return err
}
