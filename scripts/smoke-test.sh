#!/usr/bin/env bash
# Full automated end-to-end smoke test for the definition service.
#
# Covers every HTTP route, the gRPC GetCompiledWorkflow endpoint, and the
# internal /internal/events endpoint (DepartmentMembershipRevoked).
#
# Prerequisites (all must be running before this script):
#   make docker-up                             # postgres + valkey + localstack
#   make migrate-up
#   go run ./cmd/stub/execution &              # gRPC stub :9091, ctrl :9092
#   go run ./cmd/stub/membership &             # HTTP stub :8081
#   ORG_MEMBERSHIP_BASE_URL=http://localhost:8081 EXECUTION_SERVICE_ADDR=localhost:9091 \
#     AWS_USE_STUB=false go run ./cmd/server & # definition service :8080/:9090
#
# PgBouncer note:
#   If running with PgBouncer in front of Postgres, set DATABASE_URL to the
#   PgBouncer DSN (port 6432 by default) when starting the server. The DB_URL
#   variable below is used only for direct psql assertion queries and should
#   always point directly to Postgres (port 5432), bypassing PgBouncer, because
#   psql uses simple queries that PgBouncer in transaction-pool mode restricts.
#
# Optional tools (checks are skipped if missing):
#   grpcurl  (brew install grpcurl)
#   psql     (brew install postgresql)
#
# Usage:
#   ./scripts/smoke-test.sh

set -euo pipefail

HEALTH_ONLY=false
for arg in "$@"; do
  case "$arg" in --health-only) HEALTH_ONLY=true ;; esac
done

HTTP="${HTTP_BASE:-http://localhost:8080}"
API="${HTTP}/api/v1"
GRPC="${GRPC_ADDR:-localhost:9090}"
MEMBERSHIP_CTRL="${MEMBERSHIP_CTRL:-http://localhost:8081}"
EXECUTION_CTRL="${EXECUTION_CTRL:-http://localhost:9092}"
DB_URL="${DB_URL:-postgres://wfdef:wfdef@localhost:5432/workflow_definition?sslmode=disable}"
INTERNAL_TOKEN="${INTERNAL_TOKEN:-}"

# Stable fixture UUIDs matching initial-diagram.bpmn
ALICE="019ef700-0000-7001-8001-000000000001"  # design preparer (UUID v7 required by eligibility check)
BOB="019ef700-0000-7001-9001-000000000002"    # design reviewer (UUID v7 required by eligibility check)

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
PASS=0; FAIL=0
pass() { echo -e "  ${GREEN}✓${NC} $1"; (( PASS++ )) || true; }
fail() { echo -e "  ${RED}✗${NC} $1"; (( FAIL++ )) || true; }
skip() { echo -e "  ${YELLOW}–${NC} $1 (skipped — install: $2)"; }
section() { echo ""; echo -e "${CYAN}══ $1 ══${NC}"; }

HAS_GRPCURL=false; HAS_PSQL=false
command -v grpcurl &>/dev/null && HAS_GRPCURL=true
command -v psql    &>/dev/null && HAS_PSQL=true

wait_http() {
  local url="$1" label="$2" tries=0
  until curl -sf "$url" &>/dev/null; do
    (( tries++ )) || true
    [ $tries -ge 40 ] && { echo "  FATAL: $label not ready after 40s"; exit 1; }
    sleep 1
  done
}

wait_grpc() {
  [ "$HAS_GRPCURL" = true ] || return 0
  local addr="$1" tries=0
  until grpcurl -plaintext "$addr" list &>/dev/null 2>&1; do
    (( tries++ )) || true
    [ $tries -ge 30 ] && { echo "  FATAL: gRPC at $addr not ready"; exit 1; }
    sleep 1
  done
}

membership_eligible() {
  curl -sf -X POST "${MEMBERSHIP_CTRL}/control" \
    -H "Content-Type: application/json" \
    -d '{"eligible":true}' > /dev/null
}
membership_ineligible() {
  curl -sf -X POST "${MEMBERSHIP_CTRL}/control" \
    -H "Content-Type: application/json" \
    -d '{"eligible":false}' > /dev/null
}
execution_no_active() {
  curl -sf -X POST "${EXECUTION_CTRL}/control" \
    -H "Content-Type: application/json" \
    -d '{"has_active":false}' > /dev/null
}
execution_has_active() {
  curl -sf -X POST "${EXECUTION_CTRL}/control" \
    -H "Content-Type: application/json" \
    -d '{"has_active":true}' > /dev/null
}

TENANT_ID="${TENANT_ID:-$(uuidgen | tr '[:upper:]' '[:lower:]')}"
USER_ID="${USER_ID:-$(uuidgen | tr '[:upper:]' '[:lower:]')}"

api_get()    { curl -sf -X GET    "$1" -H "x-tenant-id: ${TENANT_ID}" -H "x-user-id: ${USER_ID}"; }
api_post()   { curl -sf -X POST   "$1" -H "x-tenant-id: ${TENANT_ID}" -H "x-user-id: ${USER_ID}" \
                    -H "Content-Type: application/json" -d "$2"; }
api_put()    { curl -sf -X PUT    "$1" -H "x-tenant-id: ${TENANT_ID}" -H "x-user-id: ${USER_ID}" \
                    -H "Content-Type: application/json" -d "$2"; }
api_delete() { curl -sf -X DELETE "$1" -H "x-tenant-id: ${TENANT_ID}" -H "x-user-id: ${USER_ID}"; }

# Returns HTTP status code only, does not fail on 4xx/5xx.
api_status() {
  curl -s -o /dev/null -w "%{http_code}" -X "${1}" "${2}" \
    -H "x-tenant-id: ${TENANT_ID}" -H "x-user-id: ${USER_ID}" \
    -H "Content-Type: application/json" ${3:+-d "$3"}
}

jq_field() { python3 -c "import json,sys; d=json.load(sys.stdin); print(d$(echo "$2" | sed "s/\./']['/g; s/^/['/; s/$/']/" ))" 2>/dev/null <<< "$1"; }
first_id()  { python3 -c "import json,sys; d=json.load(sys.stdin); print(d['items'][0]['id'] if d.get('items') else d['id'])" 2>/dev/null <<< "$1"; }
get_id()         { python3 -c "import json,sys; d=json.load(sys.stdin); print(d['id'])" 2>/dev/null <<< "$1"; }
get_version_id() { python3 -c "import json,sys; d=json.load(sys.stdin); print(d['version_id'])" 2>/dev/null <<< "$1"; }
get_workflow_id() { python3 -c "import json,sys; d=json.load(sys.stdin); print(d['workflow_id'])" 2>/dev/null <<< "$1"; }

# Uses the canonical Zeebe properties; alice is preparer, bob is reviewer.
BPMN_VALID='<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
    xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
    xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
    xmlns:di="http://www.omg.org/spec/DD/20100524/DI"
    id="Definitions_smoke" targetNamespace="http://bpmn.io/schema/bpmn"
    exporter="Workflow Engine" exporterVersion="1.0">
  <bpmn:process id="Process_smoke" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="Design">
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_review</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
    <bpmn:userTask id="Task_prep" name="Design Prep">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer"/>
        <zeebe:properties>
          <zeebe:property name="dept_id" value="design"/>
          <zeebe:property name="role" value="preparer"/>
          <zeebe:property name="default_user_ids" value="550e8400-e29b-41d4-a716-446655440000"/>
          <zeebe:property name="sla_duration" value="48h"/>
        </zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:userTask id="Task_review" name="Design Review">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer"/>
        <zeebe:properties>
          <zeebe:property name="dept_id" value="design"/>
          <zeebe:property name="role" value="reviewer"/>
          <zeebe:property name="default_user_ids" value="661f9511-f3ac-52e5-b827-557766551111"/>
          <zeebe:property name="sla_duration" value="24h"/>
        </zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="EndEvent_1" name="End"/>
    <bpmn:sequenceFlow id="sf1" sourceRef="StartEvent_1" targetRef="Task_prep"/>
    <bpmn:sequenceFlow id="sf2" sourceRef="Task_prep" targetRef="Task_review"/>
    <bpmn:sequenceFlow id="sf3" sourceRef="Task_review" targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_smoke">
      <bpmndi:BPMNShape id="StartEvent_1_di" bpmnElement="StartEvent_1">
        <dc:Bounds x="152" y="82" width="36" height="36"/>
      </bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Task_prep_di" bpmnElement="Task_prep">
        <dc:Bounds x="240" y="60" width="100" height="80"/>
      </bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Task_review_di" bpmnElement="Task_review">
        <dc:Bounds x="400" y="60" width="100" height="80"/>
      </bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="EndEvent_1_di" bpmnElement="EndEvent_1">
        <dc:Bounds x="562" y="82" width="36" height="36"/>
      </bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="sf1_di" bpmnElement="sf1">
        <di:waypoint x="188" y="100"/>
        <di:waypoint x="240" y="100"/>
      </bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="sf2_di" bpmnElement="sf2">
        <di:waypoint x="340" y="100"/>
        <di:waypoint x="400" y="100"/>
      </bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="sf3_di" bpmnElement="sf3">
        <di:waypoint x="500" y="100"/>
        <di:waypoint x="562" y="100"/>
      </bpmndi:BPMNEdge>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>'

BPMN_INVALID='<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    id="Definitions_bad" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_bad" isExecutable="true">
    <bpmn:startEvent id="start"/>
  </bpmn:process>
</bpmn:definitions>'

BPMN_JSON=$(python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' <<< "$BPMN_VALID")
BPMN_INVALID_JSON=$(python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' <<< "$BPMN_INVALID")

# BPMN variant for internal-events tests: candidateUsers set so assignees are
# stored in workflow_node_assignee at publish time, enabling revocation tests.
BPMN_IE='<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
    xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
    xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
    xmlns:di="http://www.omg.org/spec/DD/20100524/DI"
    id="Definitions_ie" targetNamespace="http://bpmn.io/schema/bpmn"
    exporter="Workflow Engine" exporterVersion="1.0">
  <bpmn:process id="Process_ie" isExecutable="true">
    <bpmn:laneSet id="LaneSet_ie">
      <bpmn:lane id="Lane_ie" name="Design">
        <bpmn:flowNodeRef>SE_ie</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_ie_prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_ie_review</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EE_ie</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="SE_ie" name="Start"/>
    <bpmn:userTask id="Task_ie_prep" name="Design Prep">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="'"${ALICE}"'"/>
        <zeebe:properties>
          <zeebe:property name="dept_id" value="design"/>
          <zeebe:property name="role" value="preparer"/>
          <zeebe:property name="sla_duration" value="48h"/>
        </zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:userTask id="Task_ie_review" name="Design Review">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="'"${BOB}"'"/>
        <zeebe:properties>
          <zeebe:property name="dept_id" value="design"/>
          <zeebe:property name="role" value="reviewer"/>
          <zeebe:property name="sla_duration" value="24h"/>
        </zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="EE_ie" name="End"/>
    <bpmn:sequenceFlow id="sf_ie1" sourceRef="SE_ie" targetRef="Task_ie_prep"/>
    <bpmn:sequenceFlow id="sf_ie2" sourceRef="Task_ie_prep" targetRef="Task_ie_review"/>
    <bpmn:sequenceFlow id="sf_ie3" sourceRef="Task_ie_review" targetRef="EE_ie"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_ie">
    <bpmndi:BPMNPlane id="BPMNPlane_ie" bpmnElement="Process_ie">
      <bpmndi:BPMNShape id="SE_ie_di" bpmnElement="SE_ie"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Task_ie_prep_di" bpmnElement="Task_ie_prep"><dc:Bounds x="240" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Task_ie_review_di" bpmnElement="Task_ie_review"><dc:Bounds x="400" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="EE_ie_di" bpmnElement="EE_ie"><dc:Bounds x="562" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="sf_ie1_di" bpmnElement="sf_ie1"><di:waypoint x="188" y="100"/><di:waypoint x="240" y="100"/></bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="sf_ie2_di" bpmnElement="sf_ie2"><di:waypoint x="340" y="100"/><di:waypoint x="400" y="100"/></bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="sf_ie3_di" bpmnElement="sf_ie3"><di:waypoint x="500" y="100"/><di:waypoint x="562" y="100"/></bpmndi:BPMNEdge>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>'
BPMN_IE_JSON=$(python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' <<< "$BPMN_IE")


echo "  tenant_id = ${TENANT_ID}"
echo "  user_id   = ${USER_ID}"

section "0 · Readiness"
echo "  Waiting for services..."
wait_http "${HTTP}/healthz" "HTTP server"

if [ "$HEALTH_ONLY" = "false" ]; then
  # Reset membership stub to eligible — also serves as the readiness probe, since
  # the control endpoint always returns 200. A bare GET / would fail when the stub
  # was left in ineligible state from a prior run.
  local_tries=0
  until curl -sf -X POST "${MEMBERSHIP_CTRL}/control" \
       -H "Content-Type: application/json" -d '{"eligible":true}' &>/dev/null; do
    (( local_tries++ )) || true
    [ $local_tries -ge 40 ] && { echo "  FATAL: membership stub not ready after 40s"; exit 1; }
    sleep 1
  done

  wait_grpc "${GRPC}"
  membership_eligible
  execution_no_active
fi
pass "All services ready"

section "1 · Health endpoints"
STATUS=$(api_status GET "${HTTP}/healthz")
[ "$STATUS" = "200" ] && pass "GET /healthz → 200" || fail "GET /healthz → $STATUS"

STATUS=$(api_status GET "${HTTP}/readyz")
[ "$STATUS" = "200" ] && pass "GET /readyz → 200" || fail "GET /readyz → $STATUS"

STATUS=$(api_status GET "${HTTP}/metrics")
[ "$STATUS" = "200" ] && pass "GET /metrics → 200" || fail "GET /metrics → $STATUS"

if [ "$HEALTH_ONLY" = "true" ]; then
  echo ""
  echo -e "${GREEN}✓ health-only smoke test passed${NC}"
  exit 0
fi

section "2 · Workflow CRUD"

WF_RESP=$(api_post "${API}/workflows" \
  "{\"key\":\"smoke-test-wf\",\"name\":\"smoke-test\",\"description\":\"automated e2e\",\"bpmn_xml\":${BPMN_JSON}}") \
  || { fail "POST /workflows failed"; exit 1; }
WF_ID=$(get_workflow_id "$WF_RESP")
V1_ID=$(get_version_id "$WF_RESP")
[ -n "$WF_ID" ] && pass "POST /workflows → 201, workflow_id=${WF_ID}" || fail "no workflow_id in response"
[ -n "$V1_ID" ] && pass "POST /workflows → initial draft version_id=${V1_ID}" || fail "no version_id in response"

STATUS=$(api_status GET "${API}/workflows/${WF_ID}")
[ "$STATUS" = "200" ] && pass "GET /workflows/:id → 200" || fail "GET /workflows/:id → $STATUS"

# Verify name in detail response
WF_NAME=$(api_get "${API}/workflows/${WF_ID}" | python3 -c "import json,sys; print(json.load(sys.stdin).get('name',''))" 2>/dev/null)
[ "$WF_NAME" = "smoke-test" ] \
  && pass "GET /workflows/:id name field = 'smoke-test'" \
  || fail "GET /workflows/:id name = '${WF_NAME}' (want smoke-test)"

LIST_RESP=$(api_get "${API}/workflows") || { fail "GET /workflows failed"; exit 1; }
echo "$LIST_RESP" | grep -q "$WF_ID" \
  && pass "GET /workflows list contains created workflow" \
  || fail "GET /workflows list missing workflow"

STATUS=$(api_status GET "${API}/workflows/00000000-0000-0000-0000-000000000001")
[ "$STATUS" = "404" ] && pass "GET /workflows/<nonexistent> → 404" || fail "GET /workflows/<nonexistent> → $STATUS (want 404)"

section "3 · BPMN validation"

VAL_RESP=$(api_post "${API}/workflows/validate" "{\"bpmn_xml\":${BPMN_JSON}}")
VALID_FLAG=$(python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('is_valid','?'))" <<< "$VAL_RESP" 2>/dev/null)
[ "$VALID_FLAG" = "True" ] || [ "$VALID_FLAG" = "true" ] \
  && pass "POST /validate (valid BPMN) → is_valid=true" \
  || fail "POST /validate (valid BPMN) → is_valid=${VALID_FLAG}"

VAL_BAD=$(api_post "${API}/workflows/validate" "{\"bpmn_xml\":${BPMN_INVALID_JSON}}")
VALID_BAD=$(python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('is_valid','?'))" <<< "$VAL_BAD" 2>/dev/null)
[ "$VALID_BAD" = "False" ] || [ "$VALID_BAD" = "false" ] \
  && pass "POST /validate (invalid BPMN) → is_valid=false with errors" \
  || fail "POST /validate (invalid BPMN) → is_valid=${VALID_BAD} (want false)"

section "4 · Draft lifecycle"

pass "initial draft created with workflow (version_id=${V1_ID})"

STATUS=$(api_status GET "${API}/workflows/${WF_ID}/draft")
[ "$STATUS" = "200" ] && pass "GET /draft → 200" || fail "GET /draft → $STATUS"

STATUS=$(api_status PUT "${API}/workflows/${WF_ID}/draft" \
  "{\"bpmn_xml\":${BPMN_JSON}}")
[ "$STATUS" = "200" ] && pass "PUT /draft (update BPMN) → 200" || fail "PUT /draft → $STATUS"

# Duplicate draft → 409
STATUS=$(api_status POST "${API}/workflows/${WF_ID}/draft" \
  "{\"bpmn_xml\":${BPMN_JSON}}")
[ "$STATUS" = "409" ] && pass "POST /draft (duplicate) → 409" || fail "POST /draft duplicate → $STATUS (want 409)"

section "5 · Publish"

membership_eligible
STATUS=$(api_status POST "${API}/workflows/${WF_ID}/versions/${V1_ID}/publish")
[ "$STATUS" = "200" ] \
  && pass "POST /publish → 200" \
  || fail "POST /publish → $STATUS (want 200)"

STATUS=$(api_status POST "${API}/workflows/${WF_ID}/versions/${V1_ID}/publish")
[ "$STATUS" = "409" ] \
  && pass "POST /publish (already published) → 409" \
  || fail "POST /publish duplicate → $STATUS (want 409)"

section "6 · Version read"

STATUS=$(api_status GET "${API}/workflows/${WF_ID}/versions/${V1_ID}")
[ "$STATUS" = "200" ] && pass "GET /versions/:id → 200" || fail "GET /versions/:id → $STATUS"

STATUS=$(api_status GET "${API}/workflows/${WF_ID}/versions")
[ "$STATUS" = "200" ] && pass "GET /versions (list) → 200" || fail "GET /versions → $STATUS"

STATUS=$(api_status GET "${API}/workflows/${WF_ID}/versions/00000000-0000-0000-0000-000000000002")
[ "$STATUS" = "404" ] && pass "GET /versions/<nonexistent> → 404" || fail "GET /versions/<nonexistent> → $STATUS (want 404)"

# Export BPMN
EXPORT_STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "x-tenant-id: ${TENANT_ID}" -H "x-user-id: ${USER_ID}" \
  "${API}/workflows/${WF_ID}/versions/${V1_ID}/export")
[ "$EXPORT_STATUS" = "200" ] && pass "GET /versions/:id/export → 200" || fail "GET /versions/:id/export → $EXPORT_STATUS"

section "7 · Promote"

STATUS=$(api_status POST "${API}/workflows/${WF_ID}/versions/${V1_ID}/promote")
[ "$STATUS" = "200" ] \
  && pass "POST /versions/:id/promote (published version) → 200" \
  || fail "POST /versions/:id/promote → $STATUS (want 200)"

# Promote already-active version → 200 no-op
STATUS=$(api_status POST "${API}/workflows/${WF_ID}/versions/${V1_ID}/promote")
[ "$STATUS" = "200" ] \
  && pass "POST /versions/:id/promote (already active — no-op) → 200" \
  || fail "POST /versions/:id/promote already-active → $STATUS (want 200)"

# Promote a draft version → 409
DRAFT2_RESP=$(api_post "${API}/workflows/${WF_ID}/draft" '{}') \
  || { fail "POST /draft (for promote test) failed"; exit 1; }
V_DRAFT_ID=$(get_version_id "$DRAFT2_RESP")
STATUS=$(api_status POST "${API}/workflows/${WF_ID}/versions/${V_DRAFT_ID}/promote")
[ "$STATUS" = "409" ] \
  && pass "POST /versions/:id/promote (draft) → 409" \
  || fail "POST /versions/:id/promote draft → $STATUS (want 409)"

section "8 · Clone"

# Discard the current draft first so clone can complete
STATUS=$(api_status DELETE "${API}/workflows/${WF_ID}/draft")
[ "$STATUS" = "200" ] || [ "$STATUS" = "204" ] \
  && pass "DELETE /draft → ${STATUS}" \
  || fail "DELETE /draft → $STATUS (want 200 or 204)"

CLONE_RESP=$(api_post "${API}/workflows/${WF_ID}/versions/${V1_ID}/clone" \
  '{"new_key":"smoke-test-clone","new_name":"Smoke Clone","new_description":"cloned"}') \
  || { fail "POST /clone failed"; exit 1; }
CLONE_WF_ID=$(get_workflow_id "$CLONE_RESP")
CLONE_V_ID=$(get_version_id "$CLONE_RESP")
[ -n "$CLONE_WF_ID" ] \
  && pass "POST /clone → 201, new_workflow_id=${CLONE_WF_ID}" \
  || fail "no workflow id in clone response: $CLONE_RESP"

section "9 · Version diff"

# Publish the cloned draft so we have two PUBLISHED versions to diff
membership_eligible
STATUS=$(api_status POST "${API}/workflows/${CLONE_WF_ID}/versions/${CLONE_V_ID}/publish")
[ "$STATUS" = "200" ] \
  && pass "POST /publish (clone draft) → 200" \
  || fail "POST /publish clone → $STATUS"

# Diff within the original workflow (need two versions — publish a second one)
# Use V1 as both sides of the diff for simplicity; result should be empty diff
DIFF_STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "x-tenant-id: ${TENANT_ID}" -H "x-user-id: ${USER_ID}" \
  "${API}/workflows/${WF_ID}/versions/${V1_ID}/diff/${V1_ID}")
[ "$DIFF_STATUS" = "200" ] \
  && pass "GET /versions/:version_id/diff/:target_version_id → 200 (self-diff)" \
  || fail "GET /versions/:version_id/diff/:target_version_id → $DIFF_STATUS"

section "10 · gRPC GetCompiledWorkflow"

if [ "$HAS_GRPCURL" = true ]; then
  # Published version → success with compiled_plan_json
  GRPC_RESP=$(grpcurl -plaintext \
    -d "{\"tenant_id\":\"${TENANT_ID}\",\"workflow_version_id\":\"${V1_ID}\"}" \
    "$GRPC" workflow.definition.v1.DefinitionService/GetCompiledWorkflow 2>&1) || true
  echo "$GRPC_RESP" | grep -q "versionId" \
    && pass "GetCompiledWorkflow (PUBLISHED) → returns version data" \
    || fail "GetCompiledWorkflow PUBLISHED: unexpected response: $GRPC_RESP"

  echo "$GRPC_RESP" | grep -q "compiledPlanJson" \
    && pass "GetCompiledWorkflow (PUBLISHED) → compiled_plan_json populated" \
    || fail "GetCompiledWorkflow PUBLISHED: missing compiled_plan_json"

  # DRAFT version → no compiled plan (empty string)
  DRAFT_FOR_GRPC=$(api_post "${API}/workflows/${WF_ID}/draft" '{}')
  V_GRPC_DRAFT=$(get_version_id "$DRAFT_FOR_GRPC")
  GRPC_DRAFT=$(grpcurl -plaintext \
    -d "{\"tenant_id\":\"${TENANT_ID}\",\"workflow_version_id\":\"${V_GRPC_DRAFT}\"}" \
    "$GRPC" workflow.definition.v1.DefinitionService/GetCompiledWorkflow 2>&1) || true
  echo "$GRPC_DRAFT" | grep -q "versionId" \
    && pass "GetCompiledWorkflow (DRAFT) → returns version data" \
    || fail "GetCompiledWorkflow DRAFT: unexpected: $GRPC_DRAFT"

  # Discard the draft we just created for gRPC test
  api_delete "${API}/workflows/${WF_ID}/draft" > /dev/null 2>&1 || true

  # NOT_FOUND
  GRPC_NF=$(grpcurl -plaintext \
    -d "{\"tenant_id\":\"${TENANT_ID}\",\"workflow_version_id\":\"00000000-0000-0000-0000-000000000003\"}" \
    "$GRPC" workflow.definition.v1.DefinitionService/GetCompiledWorkflow 2>&1) || true
  echo "$GRPC_NF" | grep -qi "NotFound\|not_found" \
    && pass "GetCompiledWorkflow (not found) → NOT_FOUND" \
    || fail "GetCompiledWorkflow not-found: expected NOT_FOUND, got: $GRPC_NF"

  # INVALID_ARGUMENT — bad tenant UUID
  GRPC_BADID=$(grpcurl -plaintext \
    -d "{\"tenant_id\":\"not-a-uuid\",\"workflow_version_id\":\"${V1_ID}\"}" \
    "$GRPC" workflow.definition.v1.DefinitionService/GetCompiledWorkflow 2>&1) || true
  echo "$GRPC_BADID" | grep -qi "InvalidArgument\|invalid_argument" \
    && pass "GetCompiledWorkflow (bad UUID) → INVALID_ARGUMENT" \
    || fail "GetCompiledWorkflow bad-uuid: expected INVALID_ARGUMENT, got: $GRPC_BADID"

  # INVALID_ARGUMENT — bad version UUID
  GRPC_BADV=$(grpcurl -plaintext \
    -d "{\"tenant_id\":\"${TENANT_ID}\",\"workflow_version_id\":\"not-a-uuid\"}" \
    "$GRPC" workflow.definition.v1.DefinitionService/GetCompiledWorkflow 2>&1) || true
  echo "$GRPC_BADV" | grep -qi "InvalidArgument\|invalid_argument" \
    && pass "GetCompiledWorkflow (bad version UUID) → INVALID_ARGUMENT" \
    || fail "GetCompiledWorkflow bad-version-uuid: expected INVALID_ARGUMENT, got: $GRPC_BADV"
else
  skip "gRPC tests" "brew install grpcurl"
fi

section "11 · Internal Events — DepartmentMembershipRevoked"

# The shared workflow-events consumer forwards DepartmentMembershipRevoked to
# POST /internal/events. This section simulates that by calling the endpoint
# directly (no SQS dependency). Set INTERNAL_TOKEN to match INTERNAL_API_TOKEN
# in the server config if the token check is enabled.

# Create a workflow in DRAFT state (never published — assignees not yet stored,
# so revocation does not invalidate it; tests the event-recording path only).
WF2_RESP=$(api_post "${API}/workflows" \
  "{\"key\":\"ie-draft-wf\",\"name\":\"ie-draft-test\",\"description\":\"internal events draft invalidation\",\"bpmn_xml\":${BPMN_IE_JSON}}")
WF2_ID=$(get_workflow_id "$WF2_RESP")
V2_DRAFT_ID=$(get_version_id "$WF2_RESP")
[ -n "$V2_DRAFT_ID" ] && pass "Setup: created draft workflow for internal events test" || fail "Setup: draft creation failed"

# Send DepartmentMembershipRevoked for alice (design/preparer)
EVENT1_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
REVOKE_MSG=$(cat <<JSON
{
  "id": "${EVENT1_ID}",
  "type": "DepartmentMembershipRevoked",
  "tenant_id": "${TENANT_ID}",
  "source": "iam-svc",
  "data": {
    "user_id": "${ALICE}",
    "department_id": "Design",
    "role": "preparer"
  }
}
JSON
)

IE_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
  "${HTTP}/internal/events" \
  -H "Content-Type: application/json" \
  ${INTERNAL_TOKEN:+-H "x-internal-token: ${INTERNAL_TOKEN}"} \
  -d "$REVOKE_MSG")
[ "$IE_STATUS" = "200" ] || [ "$IE_STATUS" = "204" ] \
  && pass "POST /internal/events DepartmentMembershipRevoked → ${IE_STATUS}" \
  || fail "POST /internal/events → ${IE_STATUS} (want 200/204)"

# Re-send same event_id — must be idempotent (dedup via processed_events table)
IE_STATUS2=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
  "${HTTP}/internal/events" \
  -H "Content-Type: application/json" \
  ${INTERNAL_TOKEN:+-H "x-internal-token: ${INTERNAL_TOKEN}"} \
  -d "$REVOKE_MSG")
[ "$IE_STATUS2" = "200" ] || [ "$IE_STATUS2" = "204" ] \
  && pass "POST /internal/events re-send (dedup) → ${IE_STATUS2}" \
  || fail "POST /internal/events dedup → ${IE_STATUS2} (want 200/204)"

if [ "$HAS_PSQL" = true ]; then
  IS_VALID=$(psql "$DB_URL" -tAc \
    "SELECT is_valid FROM workflow_version WHERE id='${V2_DRAFT_ID}'" 2>/dev/null || echo "?")
  [ "$IS_VALID" = "t" ] || [ "$IS_VALID" = "true" ] \
    && pass "DRAFT version is_valid unchanged (assignees only stored at publish)" \
    || fail "DRAFT version is_valid='${IS_VALID}' (want true — draft was never published)"

  PROC_COUNT=$(psql "$DB_URL" -tAc \
    "SELECT COUNT(*) FROM processed_event WHERE event_id='${EVENT1_ID}'" 2>/dev/null || echo "?")
  [ "$PROC_COUNT" = "1" ] \
    && pass "processed_event contains event_id=${EVENT1_ID}" \
    || fail "processed_event count=${PROC_COUNT} (want 1)"

  # Second send must not add a second processed_event row
  PROC_AFTER=$(psql "$DB_URL" -tAc \
    "SELECT COUNT(*) FROM processed_event WHERE event_id='${EVENT1_ID}'" 2>/dev/null || echo "?")
  [ "$PROC_AFTER" = "1" ] \
    && pass "processed_event still exactly 1 row after re-delivery (idempotent)" \
    || fail "processed_event count=${PROC_AFTER} after re-delivery (want 1)"
else
  skip "DB assertions" "brew install postgresql"
fi

# Also test with a PUBLISHED version — should invalidate and emit outbox event
WF3_RESP=$(api_post "${API}/workflows" \
  "{\"key\":\"ie-pub-wf\",\"name\":\"ie-pub-test\",\"description\":\"ie published invalidation\",\"bpmn_xml\":${BPMN_IE_JSON}}")
WF3_ID=$(get_workflow_id "$WF3_RESP")
V3_DRAFT_ID=$(get_version_id "$WF3_RESP")
membership_eligible
STATUS=$(api_status POST "${API}/workflows/${WF3_ID}/versions/${V3_DRAFT_ID}/publish")
[ "$STATUS" = "200" ] && pass "Setup: published version for published-invalidation test" \
  || fail "Setup: publish failed → $STATUS"

EVENT2_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
REVOKE_MSG2=$(cat <<JSON
{
  "id": "${EVENT2_ID}",
  "type": "DepartmentMembershipRevoked",
  "tenant_id": "${TENANT_ID}",
  "source": "iam-svc",
  "data": {
    "user_id": "${BOB}",
    "department_id": "Design",
    "role": "reviewer"
  }
}
JSON
)

IE_STATUS3=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
  "${HTTP}/internal/events" \
  -H "Content-Type: application/json" \
  ${INTERNAL_TOKEN:+-H "x-internal-token: ${INTERNAL_TOKEN}"} \
  -d "$REVOKE_MSG2")
[ "$IE_STATUS3" = "200" ] || [ "$IE_STATUS3" = "204" ] \
  && pass "POST /internal/events (PUBLISHED version revoke) → ${IE_STATUS3}" \
  || fail "POST /internal/events published-revoke → ${IE_STATUS3}"

if [ "$HAS_PSQL" = true ]; then
  PUB_VALID=$(psql "$DB_URL" -tAc \
    "SELECT is_valid FROM workflow_version WHERE id='${V3_DRAFT_ID}'" 2>/dev/null || echo "?")
  [ "$PUB_VALID" = "f" ] || [ "$PUB_VALID" = "false" ] \
    && pass "PUBLISHED version is_valid=false after revocation" \
    || fail "PUBLISHED version is_valid='${PUB_VALID}' (want false)"

  PUB_OUTBOX=$(psql "$DB_URL" -tAc \
    "SELECT COUNT(*) FROM outbox_events WHERE payload::text LIKE '%${V3_DRAFT_ID}%'" 2>/dev/null || echo "?")
  [ "$PUB_OUTBOX" = "1" ] \
    && pass "Outbox event created for PUBLISHED invalidation" \
    || fail "outbox_events count=${PUB_OUTBOX} for PUBLISHED version (want 1)"
fi

section "12 · Archive workflow"

# Create a fresh workflow with a published version to archive
WF_ARC_RESP=$(api_post "${API}/workflows" \
  "{\"key\":\"archive-test-wf\",\"name\":\"archive-test\",\"description\":\"for archive test\",\"bpmn_xml\":${BPMN_JSON}}")
WF_ARC_ID=$(get_workflow_id "$WF_ARC_RESP")
ARC_V_ID=$(get_version_id "$WF_ARC_RESP")
membership_eligible
api_post "${API}/workflows/${WF_ARC_ID}/versions/${ARC_V_ID}/publish" '{}' > /dev/null

execution_has_active
STATUS=$(api_status POST "${API}/workflows/${WF_ARC_ID}/archive")
[ "$STATUS" = "409" ] \
  && pass "POST /archive (has active instances) → 409" \
  || fail "POST /archive with active instances → $STATUS (want 409)"

execution_no_active
STATUS=$(api_status POST "${API}/workflows/${WF_ARC_ID}/archive")
[ "$STATUS" = "200" ] \
  && pass "POST /archive (no active instances) → 200" \
  || fail "POST /archive no-active → $STATUS (want 200)"

# Archive already archived → 409
STATUS=$(api_status POST "${API}/workflows/${WF_ARC_ID}/archive")
[ "$STATUS" = "409" ] \
  && pass "POST /archive (already archived) → 409" \
  || fail "POST /archive already-archived → $STATUS (want 409)"

section "13 · Auth boundary"

STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${API}/workflows")
[ "$STATUS" = "401" ] \
  && pass "GET /workflows without auth headers → 401" \
  || fail "GET /workflows without auth → $STATUS (want 401)"

section "Summary"
TOTAL=$(( PASS + FAIL ))
echo ""
echo -e "  ${GREEN}Passed: ${PASS}/${TOTAL}${NC}   ${RED}Failed: ${FAIL}/${TOTAL}${NC}"
echo ""
if [ $FAIL -eq 0 ]; then
  echo -e "  ${GREEN}All checks passed.${NC}"
  exit 0
else
  echo -e "  ${RED}${FAIL} check(s) failed — see above.${NC}"
  exit 1
fi
