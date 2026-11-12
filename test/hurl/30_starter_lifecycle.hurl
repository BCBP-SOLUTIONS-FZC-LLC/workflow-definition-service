# Create a starter from raw BPMN, read, list and delete it; then make one from a published workflow version.
POST {{host}}/api/v1/starters
{"name":"hurl starter {{run}}","description":"scenario run","bpmn_xml":"<bpmn:definitions xmlns:bpmn='http://www.omg.org/spec/BPMN/20100524/MODEL' xmlns:zeebe='http://camunda.org/schema/zeebe/1.0' xmlns:bpmndi='http://www.omg.org/spec/BPMN/20100524/DI' xmlns:dc='http://www.omg.org/spec/DD/20100524/DC' xmlns:di='http://www.omg.org/spec/DD/20100524/DI' id='Definitions_h' targetNamespace='http://bpmn.io/schema/bpmn'><bpmn:process id='Process_st_{{run}}' isExecutable='true'><bpmn:laneSet id='LaneSet_1'><bpmn:lane id='Lane_design' name='Design'><bpmn:extensionElements><zeebe:properties><zeebe:property name='dept_id' value='f14abc9f-befa-54ff-b57f-c15ab3090062'/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef><bpmn:flowNodeRef>Task_prep</bpmn:flowNodeRef><bpmn:flowNodeRef>End_1</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet><bpmn:startEvent id='Start_1'/><bpmn:userTask id='Task_prep' name='Prep'><bpmn:extensionElements><zeebe:taskDefinition type='prep'/><zeebe:assignmentDefinition candidateGroups='preparator' candidateUsers='018f2d3c-0000-7000-8000-0000000000a1'/><zeebe:properties><zeebe:property name='dept_id' value='design'/><zeebe:property name='role' value='preparator'/><zeebe:property name='sla_duration' value='48h'/></zeebe:properties></bpmn:extensionElements></bpmn:userTask><bpmn:endEvent id='End_1'/><bpmn:sequenceFlow id='sf1' sourceRef='Start_1' targetRef='Task_prep'/><bpmn:sequenceFlow id='sf2' sourceRef='Task_prep' targetRef='End_1'/></bpmn:process><bpmndi:BPMNDiagram id='D1'><bpmndi:BPMNPlane id='Pl1' bpmnElement='Process_st_{{run}}'><bpmndi:BPMNShape id='s1' bpmnElement='Start_1'><dc:Bounds x='152' y='82' width='36' height='36'/></bpmndi:BPMNShape><bpmndi:BPMNShape id='s2' bpmnElement='Task_prep'><dc:Bounds x='240' y='60' width='100' height='80'/></bpmndi:BPMNShape><bpmndi:BPMNShape id='s3' bpmnElement='End_1'><dc:Bounds x='400' y='82' width='36' height='36'/></bpmndi:BPMNShape><bpmndi:BPMNEdge id='e1' bpmnElement='sf1'><di:waypoint x='188' y='100'/><di:waypoint x='240' y='100'/></bpmndi:BPMNEdge><bpmndi:BPMNEdge id='e2' bpmnElement='sf2'><di:waypoint x='340' y='100'/><di:waypoint x='400' y='100'/></bpmndi:BPMNEdge></bpmndi:BPMNPlane></bpmndi:BPMNDiagram></bpmn:definitions>"}
HTTP 201
[Captures]
starter_id: jsonpath "$.id"
[Asserts]
jsonpath "$.scope" == "tenant"

GET {{host}}/api/v1/starters/{{starter_id}}
HTTP 200
[Asserts]
jsonpath "$.name" == "hurl starter {{run}}"
jsonpath "$.created_at" != "0001-01-01T00:00:00Z"

GET {{host}}/api/v1/starters
[QueryStringParams]
q: hurl starter {{run}}
HTTP 200
[Asserts]
jsonpath "$.starters" count == 1

POST {{host}}/api/v1/workflows
{"key":"hurl-stw-{{run}}","name":"hurl starter source {{run}}","bpmn_xml":"<bpmn:definitions xmlns:bpmn='http://www.omg.org/spec/BPMN/20100524/MODEL' xmlns:zeebe='http://camunda.org/schema/zeebe/1.0' xmlns:bpmndi='http://www.omg.org/spec/BPMN/20100524/DI' xmlns:dc='http://www.omg.org/spec/DD/20100524/DC' xmlns:di='http://www.omg.org/spec/DD/20100524/DI' id='Definitions_h' targetNamespace='http://bpmn.io/schema/bpmn'><bpmn:process id='Process_stw_{{run}}' isExecutable='true'><bpmn:laneSet id='LaneSet_1'><bpmn:lane id='Lane_design' name='Design'><bpmn:extensionElements><zeebe:properties><zeebe:property name='dept_id' value='f14abc9f-befa-54ff-b57f-c15ab3090062'/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef><bpmn:flowNodeRef>Task_prep</bpmn:flowNodeRef><bpmn:flowNodeRef>End_1</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet><bpmn:startEvent id='Start_1'/><bpmn:userTask id='Task_prep' name='Prep'><bpmn:extensionElements><zeebe:taskDefinition type='prep'/><zeebe:assignmentDefinition candidateGroups='preparator' candidateUsers='018f2d3c-0000-7000-8000-0000000000a1'/><zeebe:properties><zeebe:property name='dept_id' value='design'/><zeebe:property name='role' value='preparator'/><zeebe:property name='sla_duration' value='48h'/></zeebe:properties></bpmn:extensionElements></bpmn:userTask><bpmn:endEvent id='End_1'/><bpmn:sequenceFlow id='sf1' sourceRef='Start_1' targetRef='Task_prep'/><bpmn:sequenceFlow id='sf2' sourceRef='Task_prep' targetRef='End_1'/></bpmn:process><bpmndi:BPMNDiagram id='D1'><bpmndi:BPMNPlane id='Pl1' bpmnElement='Process_stw_{{run}}'><bpmndi:BPMNShape id='s1' bpmnElement='Start_1'><dc:Bounds x='152' y='82' width='36' height='36'/></bpmndi:BPMNShape><bpmndi:BPMNShape id='s2' bpmnElement='Task_prep'><dc:Bounds x='240' y='60' width='100' height='80'/></bpmndi:BPMNShape><bpmndi:BPMNShape id='s3' bpmnElement='End_1'><dc:Bounds x='400' y='82' width='36' height='36'/></bpmndi:BPMNShape><bpmndi:BPMNEdge id='e1' bpmnElement='sf1'><di:waypoint x='188' y='100'/><di:waypoint x='240' y='100'/></bpmndi:BPMNEdge><bpmndi:BPMNEdge id='e2' bpmnElement='sf2'><di:waypoint x='340' y='100'/><di:waypoint x='400' y='100'/></bpmndi:BPMNEdge></bpmndi:BPMNPlane></bpmndi:BPMNDiagram></bpmn:definitions>"}
HTTP 201
[Captures]
wf_id: jsonpath "$.workflow_id"
wf_v1: jsonpath "$.version_id"

POST {{host}}/api/v1/starters/from-workflow-version/{{wf_v1}}
{"name":"hurl starter from workflow {{run}}"}
HTTP 409
[Asserts]
jsonpath "$.code" == "INVALID_VERSION_STATUS"

POST {{host}}/api/v1/workflows/{{wf_id}}/versions/{{wf_v1}}/publish
HTTP 200

POST {{host}}/api/v1/starters/from-workflow-version/{{wf_v1}}
{"name":"hurl starter from workflow {{run}}"}
HTTP 201
[Captures]
starter2_id: jsonpath "$.id"

DELETE {{host}}/api/v1/starters/{{starter_id}}
HTTP 200

DELETE {{host}}/api/v1/starters/{{starter2_id}}
HTTP 200

GET {{host}}/api/v1/starters/{{starter_id}}
HTTP 404
[Asserts]
jsonpath "$.code" == "NOT_FOUND"
