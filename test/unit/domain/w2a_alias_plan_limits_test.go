package domain_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
)

func TestConnectorRestAlias_Validate_AliasNameCharacterSet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		alias   string
		wantErr bool
	}{
		{name: "hyphen and underscore only", alias: "-_", wantErr: false},
		{name: "a single hyphen", alias: "-", wantErr: false},
		{name: "a single underscore", alias: "_", wantErr: false},
		{name: "every allowed class together", alias: "aZ09_-x", wantErr: false},
		{name: "64 characters of the full set", alias: strings.Repeat("aZ9_-", 12) + "aZ9_", wantErr: false},
		{name: "65 characters", alias: strings.Repeat("a", 65), wantErr: true},
		{name: "an accented letter", alias: "café", wantErr: true},
		{name: "a letter outside the latin alphabet", alias: "日本", wantErr: true},
		{name: "a trailing newline", alias: "tender\n", wantErr: true},
		{name: "a leading newline", alias: "\ntender", wantErr: true},
		{name: "a trailing space", alias: "tender ", wantErr: true},
		{name: "a NUL byte", alias: "a\x00b", wantErr: true},
		{name: "a colon", alias: "a:b", wantErr: true},
		{name: "a backslash", alias: `a\b`, wantErr: true},
		{name: "a percent sign", alias: "a%2fb", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := validRestAlias()
			a.Alias = tc.alias

			err := a.Validate()

			if !tc.wantErr {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrInvalidConnectorAliasInput)
			assert.Contains(t, err.Error(), "alias must match")
			assert.Contains(t, err.Error(), "at most 64 characters")
		})
	}
}

func TestConnectorRestAlias_Validate_PathTemplateLengthIsCountedInBytes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "2048 ASCII bytes", path: "/" + strings.Repeat("a", 2047), wantErr: false},
		{name: "2049 ASCII bytes", path: "/" + strings.Repeat("a", 2048), wantErr: true},
		{name: "2048 bytes made of fewer, multi-byte characters", path: "/" + strings.Repeat("é", 1023) + "a", wantErr: false},
		{name: "2049 bytes made of 1025 characters", path: "/" + strings.Repeat("é", 1024), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := validRestAlias()
			a.PathTemplate = tc.path

			err := a.Validate()

			if !tc.wantErr {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrInvalidConnectorAliasInput)
			assert.Contains(t, err.Error(), "pathTemplate is longer than 2048")
		})
	}
}

func TestDraftConcurrencyError_MatchesTheSentinelBareOrWrapped(t *testing.T) {
	t.Parallel()

	const sentinelText = "draft concurrency violation"
	typed := &domain.DraftConcurrencyError{Current: 7}

	t.Run("the error text is the sentinel text", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, sentinelText, typed.Error())
		assert.Equal(t, sentinelText, domain.ErrDraftConcurrency.Error())
	})

	t.Run("a typed error matches the sentinel and carries its version", func(t *testing.T) {
		t.Parallel()
		assert.ErrorIs(t, typed, domain.ErrDraftConcurrency)
		var got *domain.DraftConcurrencyError
		require.ErrorAs(t, typed, &got)
		assert.Equal(t, int64(7), got.Current)
	})

	t.Run("wrapped once and twice with %w it still matches and keeps its version", func(t *testing.T) {
		t.Parallel()
		once := fmt.Errorf("update draft: %w", typed)
		twice := fmt.Errorf("handler: %w", once)
		for _, err := range []error{once, twice} {
			assert.ErrorIs(t, err, domain.ErrDraftConcurrency)
			var got *domain.DraftConcurrencyError
			require.ErrorAs(t, err, &got)
			assert.Equal(t, int64(7), got.Current)
			assert.Contains(t, err.Error(), sentinelText)
		}
	})

	t.Run("the bare sentinel matches itself but holds no version", func(t *testing.T) {
		t.Parallel()
		bare := fmt.Errorf("update draft: %w", domain.ErrDraftConcurrency)
		assert.ErrorIs(t, bare, domain.ErrDraftConcurrency)
		var got *domain.DraftConcurrencyError
		assert.False(t, errors.As(bare, &got), "a sentinel without a typed wrapper has no current version to extract")
	})

	t.Run("it does not match an unrelated sentinel", func(t *testing.T) {
		t.Parallel()
		assert.NotErrorIs(t, typed, domain.ErrNotFound)
		assert.NotErrorIs(t, fmt.Errorf("wrap: %w", typed), domain.ErrVersionNotDraft)
	})

	t.Run("a zero version is still a typed match", func(t *testing.T) {
		t.Parallel()
		zero := fmt.Errorf("wrap: %w", &domain.DraftConcurrencyError{})
		assert.ErrorIs(t, zero, domain.ErrDraftConcurrency)
		var got *domain.DraftConcurrencyError
		require.ErrorAs(t, zero, &got)
		assert.Equal(t, int64(0), got.Current)
	})
}

func w2aPlanWithStages(n int) *dsl.CompiledCollaboration {
	stages := make([]dsl.StageDef, n)
	for i := range stages {
		stages[i] = dsl.StageDef{Type: "send_task", NodeID: fmt.Sprintf("S%d", i)}
	}
	plan := &dsl.CompiledPlan{Name: "main", Departments: []dsl.DepartmentDef{{ID: "Ops", Label: "Ops", Stages: stages}}}
	return &dsl.CompiledCollaboration{MainPlan: plan.Name, Plans: []*dsl.CompiledPlan{plan}, SchemaVersion: dsl.CurrentSchemaVersion}
}

func TestMaxPlanStages_IsOneThousandAndBoundsCallExpansion(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 1000, domain.MaxPlanStages)

	t.Run("a plan of 1000 stages expands", func(t *testing.T) {
		t.Parallel()
		out, err := dsl.ExpandCalls(w2aPlanWithStages(1000), domain.MaxPlanStages)
		require.NoError(t, err)
		require.Len(t, out.Plans, 1)
		assert.Len(t, out.Plans[0].Departments[0].Stages, 1000)
	})

	t.Run("a plan of 1001 stages is refused", func(t *testing.T) {
		t.Parallel()
		out, err := dsl.ExpandCalls(w2aPlanWithStages(1001), domain.MaxPlanStages)
		require.ErrorIs(t, err, dsl.ErrPlanTooLarge)
		assert.Nil(t, out)
	})
}
