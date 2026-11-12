package bpmn_compiler_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const emptySubProcessXML = `    <bpmn:subProcess id="SP"><bpmn:startEvent id="SPs"/><bpmn:endEvent id="SPe"/><bpmn:sequenceFlow id="SPf" sourceRef="SPs" targetRef="SPe"/></bpmn:subProcess>
`

func ignoredInnerLaneXML(taskIDs ...string) string {
	refs := ""
	for _, id := range taskIDs {
		refs += "<bpmn:flowNodeRef>" + id + "</bpmn:flowNodeRef>"
	}
	return `<bpmn:lane id="IL" name="Vendor"><bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/><zeebe:property name="dept_id" value="` + engDeptID + `"/></zeebe:properties></bpmn:extensionElements>` + refs + `</bpmn:lane>`
}

func workingInnerLaneXML(taskIDs ...string) string {
	refs := ""
	for _, id := range taskIDs {
		refs += "<bpmn:flowNodeRef>" + id + "</bpmn:flowNodeRef>"
	}
	return `<bpmn:lane id="WL" name="Work"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="` + legalDeptID + `"/></zeebe:properties></bpmn:extensionElements>` + refs + `</bpmn:lane>`
}

func subProcessXML(lanes string, nodes ...string) string {
	return `    <bpmn:subProcess id="SP"><bpmn:laneSet id="LS">` + lanes + `</bpmn:laneSet><bpmn:startEvent id="SPs"/>` + strings.Join(nodes, "") + `<bpmn:endEvent id="SPe"/>` + `</bpmn:subProcess>
`
}

func timerOnTaskLeadingToSubProcess(subProcess string) string {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("UT") + subProcess + "    <bpmn:endEvent id=\"E\"/>\n" +
		boundaryXML("B", "UT", timerP1D) +
		flowXML("F1", "S", "UT") + flowXML("F2", "UT", "SP") + flowXML("F3", "SP", "E") + flowXML("F4", "B", "SP")
	return flowDocument([]flowLane{{"Ops", opsDeptID, []string{"S", "UT", "SP", "B", "E"}}}, body)
}

func assertRefusedEverywhere(t *testing.T, doc string, code domain.BPMNErrorCode, node string) {
	t.Helper()
	c := bpmn_compiler.New()
	ctx := context.Background()

	for name, validate := range map[string]func(context.Context, string) ([]domain.BPMNValidationError, error){
		"Validate":           c.Validate,
		"ValidateForPublish": c.ValidateForPublish,
	} {
		errs, err := validate(ctx, doc)
		require.NoError(t, err, name)
		found := findCode(errs, code)
		require.NotNil(t, found, "%s: no %s in %+v", name, code, errs)
		require.Equal(t, node, found.NodeID, name)
	}

	_, err := c.CompileForPublish(ctx, doc)
	var vf *domain.ValidationFailedError
	require.True(t, errors.As(err, &vf), "CompileForPublish = %v, want a validation failure", err)
	require.NotNil(t, findCode(vf.Errors, code))

	_, err = c.CompileDraft(ctx, doc)
	require.True(t, errors.As(err, &vf), "CompileDraft = %v, want a validation failure", err)
	require.NotNil(t, findCode(vf.Errors, code))
}

func TestBoundaryToAnEmptySubProcess_IsRefusedAsUnsupportedBoundaryTarget(t *testing.T) {
	assertRefusedEverywhere(t, timerOnTaskLeadingToSubProcess(emptySubProcessXML), domain.BPMNErrUnsupportedBoundaryTarget, "B")
}

func TestBoundaryToASubProcessWhoseOnlyTaskIsInAnIgnoredLane_CompilesWithTheTargetAtTheLane(t *testing.T) {
	sp := subProcessXML(ignoredInnerLaneXML("SPt"), `<bpmn:userTask id="SPt"/>`,
		flowXML("SPf", "SPs", "SPt"), flowXML("SPg", "SPt", "SPe"))
	doc := timerOnTaskLeadingToSubProcess(sp)
	c := bpmn_compiler.New()
	ctx := context.Background()

	for name, validate := range map[string]func(context.Context, string) ([]domain.BPMNValidationError, error){
		"Validate":           c.Validate,
		"ValidateForPublish": c.ValidateForPublish,
	} {
		errs, err := validate(ctx, doc)
		require.NoError(t, err, name)
		for _, e := range errs {
			require.NotEqual(t, domain.SeverityError, e.Severity, "%s: blocking finding %+v", name, e)
		}
		require.Nil(t, findCode(errs, domain.BPMNErrUnsupportedBoundaryTarget), name)
	}

	published, err := c.CompileForPublish(ctx, doc)
	require.NoError(t, err)
	drafted, err := c.CompileDraft(ctx, doc)
	require.NoError(t, err)

	for name, collab := range map[string]*dsl.CompiledCollaboration{"CompileForPublish": published, "CompileDraft": drafted} {
		main := planNamed(t, collab, "Main")
		timer := stageNamed(t, main, "UT").BoundaryTimer
		require.NotNil(t, timer, name)
		require.False(t, timer.Terminates, name)
		require.Equal(t, "Ops", timer.TargetDept, "%s: the sub-process holds no segment, so the target is its lane", name)
	}
}

func TestBoundaryToASubProcessWithAnIgnoredTaskThenAWorkingTask_StillCompiles(t *testing.T) {
	sp := subProcessXML(ignoredInnerLaneXML("SPt")+workingInnerLaneXML("SPu"),
		`<bpmn:userTask id="SPt"/>`, userTaskXML("SPu"),
		flowXML("SPf", "SPs", "SPt"), flowXML("SPg", "SPt", "SPu"), flowXML("SPh", "SPu", "SPe"))
	doc := timerOnTaskLeadingToSubProcess(sp)
	c := bpmn_compiler.New()

	errs, err := c.ValidateForPublish(context.Background(), doc)
	require.NoError(t, err)
	require.Empty(t, errs)
	_, err = c.CompileForPublish(context.Background(), doc)
	require.NoError(t, err)
	_, err = c.CompileDraft(context.Background(), doc)
	require.NoError(t, err)
}

func TestBoundaryToAnIgnoredLaneSubProcessInALanelessDraft_CompilesWithTheTargetAtTheImplicitLane(t *testing.T) {
	sp := subProcessXML(ignoredInnerLaneXML("SPt"), `<bpmn:userTask id="SPt"/>`,
		flowXML("SPf", "SPs", "SPt"), flowXML("SPg", "SPt", "SPe"))
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("UT") + sp + "    <bpmn:endEvent id=\"E\"/>\n" +
		boundaryXML("B", "UT", timerP1D) +
		flowXML("F1", "S", "UT") + flowXML("F2", "UT", "SP") + flowXML("F3", "SP", "E") + flowXML("F4", "B", "SP")
	doc := flowDocument(nil, body)
	c := bpmn_compiler.New()
	ctx := context.Background()

	errs, err := c.Validate(ctx, doc)
	require.NoError(t, err)
	require.Nil(t, findCode(errs, domain.BPMNErrUnsupportedBoundaryTarget))
	drafted, err := c.CompileDraft(ctx, doc)
	require.NoError(t, err)

	timer := stageNamed(t, planNamed(t, drafted, "Main"), "UT").BoundaryTimer
	require.NotNil(t, timer)
	require.False(t, timer.Terminates)
	require.NotEmpty(t, timer.TargetDept)
}
