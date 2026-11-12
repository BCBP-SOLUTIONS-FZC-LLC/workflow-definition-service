package domain_test

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestModuleInUseError_MatchesTheSentinelBareOrWrapped(t *testing.T) {
	t.Parallel()

	uses := []domain.ModuleUse{{WorkflowID: uuid.New(), WorkflowKey: "procurement", Status: domain.VersionStatusDraft}}
	typed := &domain.ModuleInUseError{Uses: uses}

	t.Run("the error text is the sentinel text", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "module is in use", typed.Error())
		assert.Equal(t, typed.Error(), domain.ErrModuleInUse.Error())
	})

	t.Run("bare and wrapped it matches the sentinel and keeps its uses", func(t *testing.T) {
		t.Parallel()
		wrapped := fmt.Errorf("archive module: %w", fmt.Errorf("check: %w", typed))
		for _, err := range []error{typed, wrapped} {
			assert.ErrorIs(t, err, domain.ErrModuleInUse)
			var got *domain.ModuleInUseError
			require.ErrorAs(t, err, &got)
			assert.Equal(t, uses, got.Uses)
		}
	})
}
