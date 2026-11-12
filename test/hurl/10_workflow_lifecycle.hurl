# Create a workflow, edit its draft, validate, publish, clone, compare, export and archive it.
POST {{host}}/api/v1/workflows
{"key":"hurl-wf-{{run}}","name":"hurl workflow {{run}}","description":"scenario run","bpmn_xml":"<bpmn:definitions xmlns:bpmn='http://www.omg.org/spec/BPMN/20100524/MODEL' xmlns:zeebe='http://camunda.org/schema/zeebe/1.0' xmlns:bpmndi='http://www.omg.org/spec/BPMN/20100524/DI' xmlns:dc='http://www.omg.org/spec/DD/20100524/DC' xmlns:di='http://www.omg.org/spec/DD/20100524/DI' id='Definitions_h' targetNamespace='http://bpmn.io/schema/bpmn'><bpmn:process id='Process_wf_{{run}}' isExecutable='true'><bpmn:laneSet id='LaneSet_1'><bpmn:lane id='Lane_design' name='Design'><bpmn:extensionElements><zeebe:properties><zeebe:property name='dept_id' value='f14abc9f-befa-54ff-b57f-c15ab3090062'/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef><bpmn:flowNodeRef>Task_prep</bpmn:flowNodeRef><bpmn:flowNodeRef>End_1</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet><bpmn:startEvent id='Start_1'/><bpmn:userTask id='Task_prep' name='Prep'><bpmn:extensionElements><zeebe:taskDefinition type='prep'/><zeebe:assignmentDefinition candidateGroups='preparator' candidateUsers='018f2d3c-0000-7000-8000-0000000000a1'/><zeebe:properties><zeebe:property name='dept_id' value='design'/><zeebe:property name='role' value='preparator'/><zeebe:property name='sla_duration' value='48h'/></zeebe:properties></bpmn:extensionElements></bpmn:userTask><bpmn:endEvent id='End_1'/><bpmn:sequenceFlow id='sf1' sourceRef='Start_1' targetRef='Task_prep'/><bpmn:sequenceFlow id='sf2' sourceRef='Task_prep' targetRef='End_1'/></bpmn:process><bpmndi:BPMNDiagram id='D1'><bpmndi:BPMNPlane id='Pl1' bpmnElement='Process_wf_{{run}}'><bpmndi:BPMNShape id='s1' bpmnElement='Start_1'><dc:Bounds x='152' y='82' width='36' height='36'/></bpmndi:BPMNShape><bpmndi:BPMNShape id='s2' bpmnElement='Task_prep'><dc:Bounds x='240' y='60' width='100' height='80'/></bpmndi:BPMNShape><bpmndi:BPMNShape id='s3' bpmnElement='End_1'><dc:Bounds x='400' y='82' width='36' height='36'/></bpmndi:BPMNShape><bpmndi:BPMNEdge id='e1' bpmnElement='sf1'><di:waypoint x='188' y='100'/><di:waypoint x='240' y='100'/></bpmndi:BPMNEdge><bpmndi:BPMNEdge id='e2' bpmnElement='sf2'><di:waypoint x='340' y='100'/><di:waypoint x='400' y='100'/></bpmndi:BPMNEdge></bpmndi:BPMNPlane></bpmndi:BPMNDiagram></bpmn:definitions>"}
HTTP 201
[Captures]
workflow_id: jsonpath "$.workflow_id"
v1: jsonpath "$.version_id"
[Asserts]
jsonpath "$.status" == "DRAFT"
jsonpath "$.version_number" == null

GET {{host}}/api/v1/workflows/{{workflow_id}}
HTTP 200
[Asserts]
jsonpath "$.name" == "hurl workflow {{run}}"
jsonpath "$.has_draft" == true

GET {{host}}/api/v1/workflows
[QueryStringParams]
search: hurl workflow {{run}}
HTTP 200
[Asserts]
jsonpath "$.pagination.total_count" == 1
jsonpath "$.workflows[0].id" == {{workflow_id}}

GET {{host}}/api/v1/workflows/{{workflow_id}}/draft
HTTP 200
[Captures]
record_version: jsonpath "$.record_version"
[Asserts]
jsonpath "$.id" == {{v1}}

PUT {{host}}/api/v1/workflows/{{workflow_id}}/draft
{"record_version":{{record_version}},"bpmn_xml":"<bpmn:definitions xmlns:bpmn='http://www.omg.org/spec/BPMN/20100524/MODEL' xmlns:zeebe='http://camunda.org/schema/zeebe/1.0' xmlns:bpmndi='http://www.omg.org/spec/BPMN/20100524/DI' xmlns:dc='http://www.omg.org/spec/DD/20100524/DC' xmlns:di='http://www.omg.org/spec/DD/20100524/DI' id='Definitions_h' targetNamespace='http://bpmn.io/schema/bpmn'><bpmn:process id='Process_wf_{{run}}' isExecutable='true'><bpmn:laneSet id='LaneSet_1'><bpmn:lane id='Lane_design' name='Design'><bpmn:extensionElements><zeebe:properties><zeebe:property name='dept_id' value='f14abc9f-befa-54ff-b57f-c15ab3090062'/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef><bpmn:flowNodeRef>Task_prep</bpmn:flowNodeRef><bpmn:flowNodeRef>End_1</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet><bpmn:startEvent id='Start_1'/><bpmn:userTask id='Task_prep' name='Prep'><bpmn:extensionElements><zeebe:taskDefinition type='prep'/><zeebe:assignmentDefinition candidateGroups='preparator' candidateUsers='018f2d3c-0000-7000-8000-0000000000a1'/><zeebe:properties><zeebe:property name='dept_id' value='design'/><zeebe:property name='role' value='preparator'/><zeebe:property name='sla_duration' value='48h'/></zeebe:properties></bpmn:extensionElements></bpmn:userTask><bpmn:endEvent id='End_1'/><bpmn:sequenceFlow id='sf1' sourceRef='Start_1' targetRef='Task_prep'/><bpmn:sequenceFlow id='sf2' sourceRef='Task_prep' targetRef='End_1'/></bpmn:process><bpmndi:BPMNDiagram id='D1'><bpmndi:BPMNPlane id='Pl1' bpmnElement='Process_wf_{{run}}'><bpmndi:BPMNShape id='s1' bpmnElement='Start_1'><dc:Bounds x='152' y='82' width='36' height='36'/></bpmndi:BPMNShape><bpmndi:BPMNShape id='s2' bpmnElement='Task_prep'><dc:Bounds x='240' y='60' width='100' height='80'/></bpmndi:BPMNShape><bpmndi:BPMNShape id='s3' bpmnElement='End_1'><dc:Bounds x='400' y='82' width='36' height='36'/></bpmndi:BPMNShape><bpmndi:BPMNEdge id='e1' bpmnElement='sf1'><di:waypoint x='188' y='100'/><di:waypoint x='240' y='100'/></bpmndi:BPMNEdge><bpmndi:BPMNEdge id='e2' bpmnElement='sf2'><di:waypoint x='340' y='100'/><di:waypoint x='400' y='100'/></bpmndi:BPMNEdge></bpmndi:BPMNPlane></bpmndi:BPMNDiagram></bpmn:definitions>"}
HTTP 200

GET {{host}}/api/v1/workflows/{{workflow_id}}/versions/{{v1}}/validation
HTTP 200
[Asserts]
jsonpath "$.is_valid" == true

POST {{host}}/api/v1/workflows/{{workflow_id}}/versions/{{v1}}/publish
HTTP 200
[Asserts]
jsonpath "$.status" == "PUBLISHED"
jsonpath "$.version_number" == 1

GET {{host}}/api/v1/workflows/{{workflow_id}}/versions
HTTP 200
[Asserts]
jsonpath "$.versions" count == 1

POST {{host}}/api/v1/workflows/{{workflow_id}}/versions/{{v1}}/clone
{"new_key":"hurl-clone-{{run}}","new_name":"hurl clone {{run}}"}
HTTP 201
[Captures]
clone_workflow_id: jsonpath "$.workflow_id"

GET {{host}}/api/v1/workflows/{{workflow_id}}/versions/{{v1}}/export
HTTP 200

GET {{host}}/api/v1/workflows/{{workflow_id}}/versions/{{v1}}/nodes
HTTP 200

POST {{host}}/api/v1/workflows/{{workflow_id}}/archive
HTTP 200

POST {{host}}/api/v1/workflows/{{workflow_id}}/archive
HTTP 409
[Asserts]
jsonpath "$.code" == "NO_ACTIVE_VERSION"
