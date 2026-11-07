package bpmn_compiler_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func w2bAssignedTaskXML(id, groups, users string) string {
	return fmt.Sprintf(`    <bpmn:userTask id=%q><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups=%q candidateUsers=%q/></bpmn:extensionElements></bpmn:userTask>
`, id, groups, users)
}

func w2bLanedChain(assignments ...[2]string) string {
	var body strings.Builder
	body.WriteString("    <bpmn:startEvent id=\"S\"/>\n")
	prev := "S"
	lane := []string{"S", "E"}
	for i, assignment := range assignments {
		id := fmt.Sprintf("T%d", i)
		body.WriteString(w2bAssignedTaskXML(id, assignment[0], assignment[1]))
		body.WriteString(flowXML(fmt.Sprintf("F_link%d", i), prev, id))
		prev = id
		lane = append(lane, id)
	}
	body.WriteString("    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F_end", prev, "E"))
	return flowDocument(oneLane(lane...), body.String())
}

func w2bLanelessKinds(t *testing.T) map[string]struct{ xml, id string } {
	t.Helper()
	return map[string]struct{ xml, id string }{
		"user task":    {userTaskXML("Work_User"), "Work_User"},
		"send task":    {`    <bpmn:sendTask id="Work_Send"><bpmn:extensionElements><zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="` + validCandidateUser + `"/></bpmn:extensionElements></bpmn:sendTask>` + "\n", "Work_Send"},
		"receive task": {receiveTaskXML("Work_Receive"), "Work_Receive"},
		"call":         {callXML("Work_Call", "Process_M"), "Work_Call"},
		"subprocess": {`    <bpmn:subProcess id="Work_Sub"><bpmn:startEvent id="Work_Sub_S"/>` + userTaskXML("Work_Sub_T") +
			`<bpmn:endEvent id="Work_Sub_E"/>` + flowXML("F_sub1", "Work_Sub_S", "Work_Sub_T") + flowXML("F_sub2", "Work_Sub_T", "Work_Sub_E") + `</bpmn:subProcess>` + "\n", "Work_Sub"},
	}
}

func w2bLanelessDocument(t *testing.T, ids ...string) string {
	t.Helper()
	kinds := w2bLanelessKinds(t)
	byID := map[string]struct{ xml, id string }{}
	for _, k := range kinds {
		byID[k.id] = k
	}
	var nodes, flows strings.Builder
	var diagram []string
	prev := "Start_1"
	for i, id := range ids {
		k := byID[id]
		nodes.WriteString(k.xml)
		link := fmt.Sprintf("F_link%d", i)
		flows.WriteString(flowXML(link, prev, id))
		diagram = append(diagram, id, link)
		if id == "Work_Sub" {
			diagram = append(diagram, "Work_Sub_S", "Work_Sub_T", "Work_Sub_E", "F_sub1", "F_sub2")
		}
		prev = id
	}
	flows.WriteString(flowXML("F_end", prev, "End_1"))
	diagram = append(diagram, "F_end")
	procs := []string{messageDefinition}
	if slices.Contains(ids, "Work_Call") {
		procs = append(procs, moduleWithTask("Process_M", validCandidateUser))
	}
	return lanelessWorkflow(nodes.String(), flows.String(), diagram, procs...)
}

func TestCompileForPublish_LanelessWorkflow_RefusesEveryKindOfWorkButCompileDraftAccepts(t *testing.T) {
	kinds := w2bLanelessKinds(t)
	var names []string
	for name := range kinds {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		id := kinds[name].id
		t.Run(name, func(t *testing.T) {
			bpmnXML := w2bLanelessDocument(t, id)

			_, err := bpmn_compiler.New().CompileForPublish(context.Background(), bpmnXML)
			var refused *domain.ValidationFailedError
			if !errors.As(err, &refused) {
				t.Fatalf("CompileForPublish err = %v, want a ValidationFailedError", err)
			}
			if len(refused.Errors) != 1 || refused.Errors[0].Code != domain.BPMNErrMissingDeptID || refused.Errors[0].NodeID != id ||
				refused.Errors[0].Severity != domain.SeverityError {
				t.Errorf("errors = %+v, want exactly one MISSING_DEPT_ID error on %s", refused.Errors, id)
			}

			draft, err := bpmn_compiler.New().CompileDraft(context.Background(), bpmnXML)
			if err != nil {
				t.Fatalf("CompileDraft err = %v, want a draft to compile without lanes", err)
			}
			if draft == nil || draft.MainPlan != "Main" {
				t.Errorf("CompileDraft = %+v, want the Main plan", draft)
			}
		})
	}
}

func TestCompileForPublish_LanelessWorkflowWithAllKindsOfWork_NamesEachNode(t *testing.T) {
	bpmnXML := w2bLanelessDocument(t, "Work_Sub", "Work_Call", "Work_Receive", "Work_Send", "Work_User")

	_, err := bpmn_compiler.New().CompileForPublish(context.Background(), bpmnXML)

	var refused *domain.ValidationFailedError
	if !errors.As(err, &refused) {
		t.Fatalf("CompileForPublish err = %v, want a ValidationFailedError", err)
	}
	var got []string
	for _, e := range refused.Errors {
		if e.Code != domain.BPMNErrMissingDeptID || e.Severity != domain.SeverityError {
			t.Errorf("error = %+v, want only MISSING_DEPT_ID errors", e)
		}
		got = append(got, e.NodeID)
	}
	slices.Sort(got)
	want := []string{"Work_Call", "Work_Receive", "Work_Send", "Work_Sub", "Work_User"}
	if !slices.Equal(got, want) {
		t.Errorf("nodes refused = %v, want %v: the work inside the subprocess is not named on its own", got, want)
	}
}

func TestCompile_LanedWorkflow_CompileDraftAndCompileForPublishAgree(t *testing.T) {
	bpmnXML := tenderDocument(userTaskXML("Award"))

	published, err := bpmn_compiler.New().CompileForPublish(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("CompileForPublish: %v", err)
	}
	draft, err := bpmn_compiler.New().CompileDraft(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("CompileDraft: %v", err)
	}

	for name, collab := range map[string]*dsl.CompiledCollaboration{"draft": draft, "publish": published} {
		if collab.MainPlan != "Main" || len(collab.Plans) != 1 || collab.Plans[0].Name != "Main" {
			t.Errorf("%s: main plan %q with %d plans, want the Main plan alone", name, collab.MainPlan, len(collab.Plans))
		}
		if collab.SchemaVersion != dsl.CurrentSchemaVersion {
			t.Errorf("%s: schema version %d, want %d", name, collab.SchemaVersion, dsl.CurrentSchemaVersion)
		}
		award := stageNamed(t, collab.Plans[0], "Award")
		if !slices.Equal(award.DefaultAssignees, []string{validCandidateUser}) {
			t.Errorf("%s: Award default assignees = %v, want the candidate user", name, award.DefaultAssignees)
		}
		if d := departmentNamed(t, collab.Plans[0], collab.Plans[0].Departments[0].ID); d.IAMDepartmentID != opsDeptID {
			t.Errorf("%s: department maps to IAM department %q, want %q", name, d.IAMDepartmentID, opsDeptID)
		}
	}
	if !reflect.DeepEqual(draft, published) {
		t.Errorf("a valid workflow compiles differently as a draft than for publish:\n draft   %+v\n publish %+v", draft, published)
	}
}

func TestCompile_UnnamedProcess_PlanNameDefaultsToTheProcessIDOnlyForCollaborations(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "A") + flowXML("F2", "A", "E")
	named := flowDocument(oneLane("S", "A", "E"), body)
	unnamed := strings.Replace(named, `<bpmn:process id="Process_Main" name="Main"`, `<bpmn:process id="Process_Main"`, 1)
	if unnamed == named {
		t.Fatal("the fixture did not drop the process name")
	}
	ctx := context.Background()

	plan, err := bpmn_compiler.New().Compile(ctx, unnamed)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if plan.Name != "" {
		t.Errorf("Compile plan name = %q, want it left empty: only the collaboration forms default it", plan.Name)
	}

	draft, err := bpmn_compiler.New().CompileDraft(ctx, unnamed)
	if err != nil {
		t.Fatalf("CompileDraft: %v", err)
	}
	published, err := bpmn_compiler.New().CompileForPublish(ctx, unnamed)
	if err != nil {
		t.Fatalf("CompileForPublish: %v", err)
	}
	for name, collab := range map[string]*dsl.CompiledCollaboration{"draft": draft, "publish": published} {
		if collab.MainPlan != "Process_Main" || len(collab.Plans) != 1 || collab.Plans[0].Name != "Process_Main" {
			t.Errorf("%s: main plan %q, plans %d (first %q), want the process id Process_Main on both", name, collab.MainPlan, len(collab.Plans), collab.Plans[0].Name)
		}
	}

	namedDraft, err := bpmn_compiler.New().CompileDraft(ctx, named)
	if err != nil {
		t.Fatalf("CompileDraft of the named process: %v", err)
	}
	if namedDraft.MainPlan != "Main" || namedDraft.Plans[0].Name != "Main" {
		t.Errorf("named process: main plan %q (plan %q), want its own name kept", namedDraft.MainPlan, namedDraft.Plans[0].Name)
	}
}

func TestCompile_DocTypeInAnyCase_IsMalformedAndForbiddenXML(t *testing.T) {
	const root = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"/>`
	declarations := map[string]string{
		"lowercase doctype":    `<!doctype x [<!entity a "b">]>`,
		"mixed case doctype":   `<!DocType x [<!EnTiTy a "b">]>`,
		"lowercase entity":     `<!entity a SYSTEM "file:///etc/passwd">`,
		"mixed case entity":    `<!eNtItY a "b">`,
		"doctype without body": `<!dOcTyPe x>`,
	}
	entrypoints := map[string]func(c *bpmn_compiler.Compiler, ctx context.Context, bpmnXML string) error{
		"Compile": func(c *bpmn_compiler.Compiler, ctx context.Context, x string) error {
			_, err := c.Compile(ctx, x)
			return err
		},
		"CompileForPublish": func(c *bpmn_compiler.Compiler, ctx context.Context, x string) error {
			_, err := c.CompileForPublish(ctx, x)
			return err
		},
		"CompileDraft": func(c *bpmn_compiler.Compiler, ctx context.Context, x string) error {
			_, err := c.CompileDraft(ctx, x)
			return err
		},
		"Validate": func(c *bpmn_compiler.Compiler, ctx context.Context, x string) error {
			_, err := c.Validate(ctx, x)
			return err
		},
		"ValidateForPublish": func(c *bpmn_compiler.Compiler, ctx context.Context, x string) error {
			_, err := c.ValidateForPublish(ctx, x)
			return err
		},
		"ProcessID": func(c *bpmn_compiler.Compiler, ctx context.Context, x string) error {
			_, err := c.ProcessID(ctx, x)
			return err
		},
	}
	for declName, declaration := range declarations {
		for entryName, run := range entrypoints {
			t.Run(declName+"/"+entryName, func(t *testing.T) {
				t.Parallel()
				err := run(bpmn_compiler.New(), context.Background(), `<?xml version="1.0"?>`+declaration+root)
				if !errors.Is(err, domain.ErrMalformedBPMN) {
					t.Errorf("error = %v, want it to wrap ErrMalformedBPMN (the client sees invalid XML)", err)
				}
				if !errors.Is(err, domain.ErrForbiddenXML) {
					t.Errorf("error = %v, want it to wrap ErrForbiddenXML (the security log sees it)", err)
				}
			})
		}
	}
}

func w2bHashReferences(t *testing.T, definitions string) string {
	t.Helper()
	return hashOf(t, receiveFlow(definitions))
}

func TestHash_IgnoresTheOrderOfMessagesErrorsAndMessageFlows(t *testing.T) {
	const m1, m2 = `<bpmn:message id="M1" name="msg1"/>`, `<bpmn:message id="M2" name="msg2"/>`
	const e1, e2 = `<bpmn:error id="Err1" errorCode="E_ONE"/>`, `<bpmn:error id="Err2" errorCode="E_TWO"/>`

	if a, b := w2bHashReferences(t, m1+m2), w2bHashReferences(t, m2+m1); a != b {
		t.Errorf("declaring the messages in another order changed the hash: %s vs %s", a, b)
	}
	if a, b := w2bHashReferences(t, m1+e1+e2), w2bHashReferences(t, m1+e2+e1); a != b {
		t.Errorf("declaring the errors in another order changed the hash: %s vs %s", a, b)
	}
	if w2bHashReferences(t, m1+m2) == w2bHashReferences(t, m1) {
		t.Error("dropping a message did not change the hash")
	}
	if w2bHashReferences(t, m1+e1+e2) == w2bHashReferences(t, m1+e1) {
		t.Error("dropping an error did not change the hash")
	}

	flows := strings.Split(strings.TrimSpace(tenderMessageFlows), "\n")
	reordered := flows[1] + "\n" + flows[0] + "\n"
	lanes := []flowLane{{"Ops", opsDeptID, []string{"S", "Prepare", "Send_Invite", "Receive_Bid", "Award", "E"}}}
	body := tenderBody(userTaskXML("Award"))
	if a, b := hashOf(t, poolsDocument(lanes, body, tenderMessageFlows)), hashOf(t, poolsDocument(lanes, body, reordered)); a != b {
		t.Errorf("declaring the message flows in another order changed the hash: %s vs %s", a, b)
	}
}

func TestHash_ChangesWithTheOrderOfElementsAndFlows(t *testing.T) {
	flows := []string{flowXML("F1", "S", "A"), flowXML("F2", "A", "B"), flowXML("F3", "B", "E")}
	document := func(first, second string, flowOrder ...int) string {
		body := "    <bpmn:startEvent id=\"S\"/>\n" + first + second + "    <bpmn:endEvent id=\"E\"/>\n"
		for _, i := range flowOrder {
			body += flows[i]
		}
		return flowDocument(oneLane("S", "A", "B", "E"), body)
	}
	a, b := userTaskXML("A"), userTaskXML("B")
	base := hashOf(t, document(a, b, 0, 1, 2))

	if again := hashOf(t, document(a, b, 0, 1, 2)); again != base {
		t.Fatalf("the same document hashed twice differently: %s vs %s", base, again)
	}
	if got := hashOf(t, document(a, b, 2, 1, 0)); got == base {
		t.Error("reordering the sequence flows did not change the hash")
	}
	if got := hashOf(t, document(b, a, 0, 1, 2)); got == base {
		t.Error("reordering the user tasks did not change the hash")
	}
}

func w2bModuleProcess(tasks ...string) string {
	var nodes, flows strings.Builder
	prev := "Process_M_Start"
	for i, task := range tasks {
		nodes.WriteString(task)
		flows.WriteString(flowXML(fmt.Sprintf("Process_M_F%d", i), prev, fmt.Sprintf("Task_%d", i)))
		prev = fmt.Sprintf("Task_%d", i)
	}
	flows.WriteString(flowXML("Process_M_FEnd", prev, "Process_M_End"))
	return `  <bpmn:process id="Process_M" isExecutable="true">
    <bpmn:startEvent id="Process_M_Start"/>
` + nodes.String() + `    <bpmn:endEvent id="Process_M_End"/>
` + flows.String() + `  </bpmn:process>`
}

func w2bModuleWarningTask() string { return w2bAssignedTaskXML("Task_0", "preparator", "") }

func w2bModuleBlockingTask(id string) string { return w2bAssignedTaskXML(id, "", validCandidateUser) }

func w2bIssuesBySeverity(errs []domain.BPMNValidationError) (warnings, blocking []domain.BPMNErrorCode) {
	for _, e := range errs {
		if e.Severity == domain.SeverityWarning {
			warnings = append(warnings, e.Code)
		} else {
			blocking = append(blocking, e.Code)
		}
	}
	return warnings, blocking
}

func TestCompileModule_WarningsOnly_CompilesAndValidateModuleStillReportsThem(t *testing.T) {
	bpmnXML := moduleDocument(w2bModuleProcess(w2bModuleWarningTask()))
	ctx := context.Background()

	errs, err := bpmn_compiler.New().ValidateModule(ctx, bpmnXML, "Process_M", false)
	if err != nil {
		t.Fatalf("ValidateModule: %v", err)
	}
	warnings, blocking := w2bIssuesBySeverity(errs)
	if !slices.Equal(warnings, []domain.BPMNErrorCode{domain.BPMNErrCandidateUsersEmpty}) || len(blocking) != 0 {
		t.Errorf("ValidateModule warnings %v, blocking %v; want the one CANDIDATE_USERS_EMPTY warning and nothing blocking", warnings, blocking)
	}

	collab, _, err := bpmn_compiler.New().CompileModule(ctx, bpmnXML, "Process_M")
	if err != nil {
		t.Fatalf("CompileModule: %v, want a module with only warnings to compile", err)
	}
	if collab == nil || collab.MainPlan != "Process_M" {
		t.Errorf("CompileModule = %+v, want the Process_M plan", collab)
	}
}

func TestCompileModule_BlockingIssue_DropsTheWarningsValidateModuleKeeps(t *testing.T) {
	bpmnXML := moduleDocument(w2bModuleProcess(w2bModuleWarningTask(), w2bModuleBlockingTask("Task_1")))
	ctx := context.Background()

	validated, err := bpmn_compiler.New().ValidateModule(ctx, bpmnXML, "Process_M", false)
	if err != nil {
		t.Fatalf("ValidateModule: %v", err)
	}
	warnings, blocking := w2bIssuesBySeverity(validated)
	if !slices.Equal(warnings, []domain.BPMNErrorCode{domain.BPMNErrCandidateUsersEmpty}) ||
		!slices.Equal(blocking, []domain.BPMNErrorCode{domain.BPMNErrCandidateGroupsEmpty}) {
		t.Fatalf("ValidateModule warnings %v, blocking %v; want the warning kept beside the blocking issue", warnings, blocking)
	}

	collab, _, err := bpmn_compiler.New().CompileModule(ctx, bpmnXML, "Process_M")
	var refused *domain.ValidationFailedError
	if !errors.As(err, &refused) {
		t.Fatalf("CompileModule = %+v, %v; want a ValidationFailedError", collab, err)
	}
	if collab != nil {
		t.Errorf("CompileModule returned a plan with its refusal: %+v", collab)
	}
	gotWarnings, gotBlocking := w2bIssuesBySeverity(refused.Errors)
	if len(gotWarnings) != 0 || !slices.Equal(gotBlocking, []domain.BPMNErrorCode{domain.BPMNErrCandidateGroupsEmpty}) {
		t.Errorf("CompileModule refusal warnings %v, blocking %v; want the blocking issue alone", gotWarnings, gotBlocking)
	}
}

func TestModule_WarningThenCompileRefusal_ValidateModuleKeepsBothAndCompileModuleKeepsTheRefusal(t *testing.T) {
	bpmnXML := strings.Replace(chainedSplitsWithoutAJoin(30), `candidateUsers="`+validCandidateUser+`"`, `candidateUsers=""`, 1)
	ctx := context.Background()

	errs, err := bpmn_compiler.New().ValidateModule(ctx, bpmnXML, "Process_Main", false)
	if err != nil {
		t.Fatalf("ValidateModule: %v", err)
	}
	warnings, blocking := w2bIssuesBySeverity(errs)
	if !slices.Equal(warnings, []domain.BPMNErrorCode{domain.BPMNErrCandidateUsersEmpty}) ||
		!slices.Equal(blocking, []domain.BPMNErrorCode{domain.BPMNErrTaskLimitExceeded}) {
		t.Fatalf("ValidateModule warnings %v, blocking %v; want the warning, then the refusal from compiling appended", warnings, blocking)
	}
	if last := errs[len(errs)-1]; last.Code != domain.BPMNErrTaskLimitExceeded {
		t.Errorf("last issue = %+v, want the compile refusal appended after the warnings", last)
	}

	_, _, err = bpmn_compiler.New().CompileModule(ctx, bpmnXML, "Process_Main")
	var refused *domain.ValidationFailedError
	if !errors.As(err, &refused) {
		t.Fatalf("CompileModule err = %v, want a ValidationFailedError", err)
	}
	gotWarnings, gotBlocking := w2bIssuesBySeverity(refused.Errors)
	if len(gotWarnings) != 0 || !slices.Equal(gotBlocking, []domain.BPMNErrorCode{domain.BPMNErrTaskLimitExceeded}) {
		t.Errorf("CompileModule refusal warnings %v, blocking %v; want TASK_LIMIT_EXCEEDED alone", gotWarnings, gotBlocking)
	}
}

func TestCompile_WarningsOnly_CompilesAndAnErrorRefusesWithTheWholeIssueList(t *testing.T) {
	ctx := context.Background()
	warningOnly := w2bLanedChain([2]string{"preparator", ""})

	plan, err := bpmn_compiler.New().Compile(ctx, warningOnly)
	if err != nil {
		t.Fatalf("Compile with only a warning: %v", err)
	}
	if plan == nil || plan.Name != "Main" || stageNamed(t, plan, "T0").NodeID != "T0" {
		t.Errorf("Compile = %+v, want the Main plan holding the task that drew the warning", plan)
	}

	withError := w2bLanedChain([2]string{"preparator", ""}, [2]string{"", validCandidateUser})
	plan, err = bpmn_compiler.New().Compile(ctx, withError)
	var refused *domain.ValidationFailedError
	if !errors.As(err, &refused) {
		t.Fatalf("Compile = %+v, %v; want a ValidationFailedError", plan, err)
	}
	if plan != nil {
		t.Errorf("Compile returned a plan with its refusal: %+v", plan)
	}
	warnings, blocking := w2bIssuesBySeverity(refused.Errors)
	if !slices.Equal(warnings, []domain.BPMNErrorCode{domain.BPMNErrCandidateUsersEmpty}) ||
		!slices.Equal(blocking, []domain.BPMNErrorCode{domain.BPMNErrCandidateGroupsEmpty}) {
		t.Errorf("refusal warnings %v, blocking %v; want the whole list: the error and the warning beside it", warnings, blocking)
	}
	for _, e := range refused.Errors {
		if e.Code == domain.BPMNErrCandidateUsersEmpty && e.NodeID != "T0" || e.Code == domain.BPMNErrCandidateGroupsEmpty && e.NodeID != "T1" {
			t.Errorf("issue %+v names the wrong node", e)
		}
	}
}
