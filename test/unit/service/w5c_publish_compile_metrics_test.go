package service_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func TestVersionService_Publish_CompilerFails_RecordsACompileSampleAndWrapsTheCause(t *testing.T) {
	registerServiceMetrics(t)
	w := coreSvcBNewPublishWire(t)
	cause := errors.New("unsupported element")
	w.versions.EXPECT().GetByID(gomock.Any(), w.tenantID, w.vID).Return(w.draft, nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	w.compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(nil, cause)
	compileBefore := coreSvcBHistogramCount(t, observability.WFCompileDurationSeconds)

	got, err := w.publish()

	require.ErrorIs(t, err, cause)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "compile bpmn: unsupported element")
	assert.Equal(t, compileBefore+1, coreSvcBHistogramCount(t, observability.WFCompileDurationSeconds), "a failed compile still records its duration")
}
