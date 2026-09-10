package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type ModuleDeps struct {
	Transactor       port.Transactor
	GlobalTransactor port.Transactor
	Modules          port.ModuleRepository
	GlobalModules    port.ModuleRepository
	Versions         port.ModuleVersionRepository
	GlobalVersions   port.ModuleVersionRepository
	Compiler         port.PlanCompiler
	Log              port.Logger
}

type ModuleService struct {
	transactor       port.Transactor
	globalTransactor port.Transactor
	modules          port.ModuleRepository
	globalModules    port.ModuleRepository
	versions         port.ModuleVersionRepository
	globalVersions   port.ModuleVersionRepository
	compiler         port.PlanCompiler
	log              port.Logger
}

func NewModuleService(d ModuleDeps) *ModuleService {
	return &ModuleService{
		transactor:       d.Transactor,
		globalTransactor: d.GlobalTransactor,
		modules:          d.Modules,
		globalModules:    d.GlobalModules,
		versions:         d.Versions,
		globalVersions:   d.GlobalVersions,
		compiler:         d.Compiler,
		log:              logOrNoop(d.Log),
	}
}

// transactorFor/modulesFor/versionsFor must always be called together with the
// same scope — a transaction and the repos used inside it have to share one
// pool (RunInTx stashes its tx in ctx; a repo backed by a different pool
// would silently ignore it and open its own connection instead).
func (s *ModuleService) transactorFor(scope domain.CatalogScope) port.Transactor {
	if scope == domain.ScopeGlobal {
		return s.globalTransactor
	}
	return s.transactor
}

func (s *ModuleService) modulesFor(scope domain.CatalogScope) port.ModuleRepository {
	if scope == domain.ScopeGlobal {
		return s.globalModules
	}
	return s.modules
}

func (s *ModuleService) versionsFor(scope domain.CatalogScope) port.ModuleVersionRepository {
	if scope == domain.ScopeGlobal {
		return s.globalVersions
	}
	return s.versions
}

func (s *ModuleService) List(
	ctx context.Context,
	callerTenantID uuid.UUID,
	filter port.ModuleFilter,
) ([]*domain.Module, int64, error) {
	modules, total, err := s.modules.List(ctx, callerTenantID, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("list modules: %w", err)
	}
	return modules, total, nil
}

type CreateModuleReq struct {
	Name        string
	Description string
	BPMNXML     string
	Scope       domain.CatalogScope
}

func (s *ModuleService) Create(
	ctx context.Context,
	callerTenantID, userID uuid.UUID,
	req CreateModuleReq,
) (*domain.Module, *domain.ModuleVersion, error) {
	scope := req.Scope
	if scope == "" {
		scope = domain.ScopeTenant
	}

	errs, err := s.compiler.Validate(ctx, req.BPMNXML)
	if err != nil {
		return nil, nil, err
	}
	if len(errs) > 0 {
		return nil, nil, &domain.ValidationFailedError{Errors: errs}
	}
	processID, err := s.compiler.ProcessID(ctx, req.BPMNXML)
	if err != nil {
		return nil, nil, err
	}

	moduleID, err := uuid.NewV7()
	if err != nil {
		return nil, nil, fmt.Errorf("generate module id: %w", err)
	}
	versionID, err := uuid.NewV7()
	if err != nil {
		return nil, nil, fmt.Errorf("generate module version id: %w", err)
	}

	var tenantID *uuid.UUID
	if scope != domain.ScopeGlobal {
		tenantID = &callerTenantID
	}
	m := &domain.Module{
		ID:              moduleID,
		TenantID:        tenantID,
		Scope:           scope,
		Name:            req.Name,
		Description:     req.Description,
		CreatedByUserID: &userID,
	}
	v := &domain.ModuleVersion{
		ID:              versionID,
		ModuleID:        moduleID,
		TenantID:        tenantID,
		Scope:           scope,
		Status:          domain.VersionStatusDraft,
		BPMNXML:         req.BPMNXML,
		ProcessID:       processID,
		IsValid:         true,
		CreatedByUserID: &userID,
	}

	modules := s.modulesFor(scope)
	versions := s.versionsFor(scope)
	if err := s.transactorFor(scope).RunInTx(ctx, func(ctx context.Context) error {
		if err := modules.Create(ctx, m); err != nil {
			return err
		}
		return versions.Create(ctx, v)
	}); err != nil {
		return nil, nil, fmt.Errorf("create module: %w", err)
	}
	s.log.Info("module created", map[string]any{
		"tenant_id": callerTenantID.String(),
		"user_id":   userID.String(),
		"module_id": m.ID.String(),
		"scope":     string(scope),
	})
	return m, v, nil
}

func (s *ModuleService) Get(
	ctx context.Context,
	callerTenantID, moduleID uuid.UUID,
) (*domain.Module, *domain.ModuleVersion, error) {
	m, err := s.modules.GetByID(ctx, callerTenantID, moduleID)
	if err != nil {
		return nil, nil, fmt.Errorf("get module: %w", err)
	}
	if m.ActiveVersionID == nil {
		return m, nil, nil
	}
	v, err := s.versions.GetByID(ctx, callerTenantID, *m.ActiveVersionID)
	if err != nil {
		return nil, nil, fmt.Errorf("get active module version: %w", err)
	}
	return m, v, nil
}

func (s *ModuleService) ListVersions(
	ctx context.Context,
	callerTenantID, moduleID uuid.UUID,
	page, limit int,
) ([]*domain.ModuleVersion, int64, error) {
	versions, total, err := s.versions.ListByModule(ctx, callerTenantID, moduleID, page, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list module versions: %w", err)
	}
	return versions, total, nil
}

func (s *ModuleService) GetVersion(
	ctx context.Context,
	callerTenantID, moduleID, versionID uuid.UUID,
) (*domain.ModuleVersion, error) {
	v, err := s.versions.GetByID(ctx, callerTenantID, versionID)
	if err != nil {
		return nil, fmt.Errorf("get module version: %w", err)
	}
	if v.ModuleID != moduleID {
		return nil, domain.ErrNotFound
	}
	return v, nil
}

type AddModuleVersionReq struct {
	BPMNXML *string
}

func (s *ModuleService) AddVersion(
	ctx context.Context,
	callerTenantID, userID, moduleID uuid.UUID,
	req AddModuleVersionReq,
) (*domain.ModuleVersion, error) {
	m, err := s.modules.GetByID(ctx, callerTenantID, moduleID)
	if err != nil {
		return nil, fmt.Errorf("get module: %w", err)
	}

	bpmnXML, err := s.resolveVersionXML(ctx, callerTenantID, m, req.BPMNXML)
	if err != nil {
		return nil, err
	}

	errs, err := s.compiler.Validate(ctx, bpmnXML)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, &domain.ValidationFailedError{Errors: errs}
	}
	processID, err := s.compiler.ProcessID(ctx, bpmnXML)
	if err != nil {
		return nil, err
	}

	versionID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate module version id: %w", err)
	}
	v := &domain.ModuleVersion{
		ID:              versionID,
		ModuleID:        moduleID,
		TenantID:        m.TenantID,
		Scope:           m.Scope,
		Status:          domain.VersionStatusDraft,
		BPMNXML:         bpmnXML,
		ProcessID:       processID,
		IsValid:         true,
		CreatedByUserID: &userID,
	}
	if err := s.versionsFor(m.Scope).Create(ctx, v); err != nil {
		return nil, fmt.Errorf("add module version: %w", err)
	}
	s.log.Info("module version added", map[string]any{
		"tenant_id":  callerTenantID.String(),
		"user_id":    userID.String(),
		"module_id":  moduleID.String(),
		"version_id": v.ID.String(),
	})
	return v, nil
}

func (s *ModuleService) resolveVersionXML(
	ctx context.Context,
	callerTenantID uuid.UUID,
	m *domain.Module,
	bpmnXML *string,
) (string, error) {
	if bpmnXML != nil {
		return *bpmnXML, nil
	}
	if m.ActiveVersionID == nil {
		return "", domain.ErrNoActiveVersion
	}
	active, err := s.versions.GetByID(ctx, callerTenantID, *m.ActiveVersionID)
	if err != nil {
		return "", fmt.Errorf("get active module version: %w", err)
	}
	return active.BPMNXML, nil
}

func (s *ModuleService) Publish(
	ctx context.Context,
	callerTenantID, userID, moduleID, versionID uuid.UUID,
) (*domain.ModuleVersion, error) {
	draft, err := s.versions.GetByID(ctx, callerTenantID, versionID)
	if err != nil {
		return nil, fmt.Errorf("get module version: %w", err)
	}
	if draft.ModuleID != moduleID {
		return nil, domain.ErrNotFound
	}
	if draft.Status != domain.VersionStatusDraft {
		return nil, domain.ErrInvalidVersionStatus
	}
	if _, err := s.compiler.Compile(ctx, draft.BPMNXML); err != nil {
		return nil, err
	}

	versions := s.versionsFor(draft.Scope)
	modules := s.modulesFor(draft.Scope)
	var versionNumber int32
	if err := s.transactorFor(draft.Scope).RunInTxWithRetry(ctx, func(ctx context.Context) error {
		n, err := versions.NextVersionNumber(ctx, moduleID)
		if err != nil {
			return fmt.Errorf("next module version number: %w", err)
		}
		versionNumber = n
		if err := versions.Publish(ctx, versionID, versionNumber); err != nil {
			return err
		}
		return modules.UpdateActiveVersion(ctx, moduleID, &versionID)
	}); err != nil {
		return nil, fmt.Errorf("publish module version: %w", err)
	}

	draft.Status = domain.VersionStatusPublished
	draft.VersionNumber = &versionNumber
	s.log.Info("module version published", map[string]any{
		"tenant_id":      callerTenantID.String(),
		"user_id":        userID.String(),
		"module_id":      moduleID.String(),
		"version_id":     versionID.String(),
		"version_number": versionNumber,
	})
	return draft, nil
}

func (s *ModuleService) Archive(ctx context.Context, callerTenantID, userID, moduleID uuid.UUID) error {
	m, err := s.modules.GetByID(ctx, callerTenantID, moduleID)
	if err != nil {
		return fmt.Errorf("get module: %w", err)
	}
	if m.ActiveVersionID == nil {
		return domain.ErrNoActiveVersion
	}

	versions := s.versionsFor(m.Scope)
	modules := s.modulesFor(m.Scope)
	activeVersionID := *m.ActiveVersionID
	if err := s.transactorFor(m.Scope).RunInTx(ctx, func(ctx context.Context) error {
		if err := versions.Archive(ctx, activeVersionID); err != nil {
			return err
		}
		return modules.UpdateActiveVersion(ctx, moduleID, nil)
	}); err != nil {
		return fmt.Errorf("archive module: %w", err)
	}
	s.log.Info("module archived", map[string]any{
		"tenant_id":  callerTenantID.String(),
		"user_id":    userID.String(),
		"module_id":  moduleID.String(),
		"version_id": activeVersionID.String(),
	})
	return nil
}
