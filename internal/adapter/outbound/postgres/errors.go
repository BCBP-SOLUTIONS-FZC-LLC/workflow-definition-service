package postgres

import (
	"errors"
	"fmt"
	"net"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	constraintWorkflowTemplateKey      = "workflow_tenant_id_template_key_key"
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
		case constraintWorkflowTemplateKey:
			return domain.ErrDuplicateTemplateKey
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
	return asUnavailable(err)
}

func asUnavailable(err error) error {
	if err == nil || errors.Is(err, domain.ErrUpstreamUnavailable) || !isUnavailable(err) {
		return err
	}
	return fmt.Errorf("%w: %w", domain.ErrUpstreamUnavailable, err)
}

func isUnavailable(err error) bool {
	var dialErr *pgconn.ConnectError
	var netErr net.Error
	return pgcommon.IsConnectionException(err) ||
		pgcommon.IsInsufficientResources(err) ||
		pgcommon.IsOperatorIntervention(err) ||
		pgcommon.IsQueryCanceled(err) ||
		pgcommon.IsPoolClosed(err) ||
		errors.As(err, &dialErr) ||
		errors.As(err, &netErr)
}
