# Create a module, publish it, read its contract, add a version and archive it.
POST {{host}}/api/v1/modules
{"name":"hurl module {{run}}","bpmn_xml":"<bpmn:definitions xmlns:bpmn='http://www.omg.org/spec/BPMN/20100524/MODEL' xmlns:zeebe='http://camunda.org/schema/zeebe/1.0' xmlns:bpmndi='http://www.omg.org/spec/BPMN/20100524/DI' xmlns:dc='http://www.omg.org/spec/DD/20100524/DC' xmlns:di='http://www.omg.org/spec/DD/20100524/DI' id='Definitions_h' targetNamespace='http://bpmn.io/schema/bpmn'><bpmn:process id='Process_mod_{{run}}' isExecutable='true'><bpmn:laneSet id='LaneSet_1'><bpmn:lane id='Lane_design' name='Design'><bpmn:extensionElements><zeebe:properties><zeebe:property name='dept_id' value='f14abc9f-befa-54ff-b57f-c15ab3090062'/></zeebe:properties></bpmn:extensionElements><bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef><bpmn:flowNodeRef>Task_prep</bpmn:flowNodeRef><bpmn:flowNodeRef>End_1</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet><bpmn:startEvent id='Start_1'/><bpmn:userTask id='Task_prep' name='Prep'><bpmn:extensionElements><zeebe:taskDefinition type='prep'/><zeebe:assignmentDefinition candidateGroups='preparator' candidateUsers='018f2d3c-0000-7000-8000-0000000000a1'/><zeebe:properties><zeebe:property name='dept_id' value='design'/><zeebe:property name='role' value='preparator'/><zeebe:property name='sla_duration' value='48h'/></zeebe:properties></bpmn:extensionElements></bpmn:userTask><bpmn:endEvent id='End_1'/><bpmn:sequenceFlow id='sf1' sourceRef='Start_1' targetRef='Task_prep'/><bpmn:sequenceFlow id='sf2' sourceRef='Task_prep' targetRef='End_1'/></bpmn:process><bpmndi:BPMNDiagram id='D1'><bpmndi:BPMNPlane id='Pl1' bpmnElement='Process_mod_{{run}}'><bpmndi:BPMNShape id='s1' bpmnElement='Start_1'><dc:Bounds x='152' y='82' width='36' height='36'/></bpmndi:BPMNShape><bpmndi:BPMNShape id='s2' bpmnElement='Task_prep'><dc:Bounds x='240' y='60' width='100' height='80'/></bpmndi:BPMNShape><bpmndi:BPMNShape id='s3' bpmnElement='End_1'><dc:Bounds x='400' y='82' width='36' height='36'/></bpmndi:BPMNShape><bpmndi:BPMNEdge id='e1' bpmnElement='sf1'><di:waypoint x='188' y='100'/><di:waypoint x='240' y='100'/></bpmndi:BPMNEdge><bpmndi:BPMNEdge id='e2' bpmnElement='sf2'><di:waypoint x='340' y='100'/><di:waypoint x='400' y='100'/></bpmndi:BPMNEdge></bpmndi:BPMNPlane></bpmndi:BPMNDiagram></bpmn:definitions>"}
HTTP 201
[Captures]
module_id: jsonpath "$.module_id"
v1: jsonpath "$.version_id"
[Asserts]
jsonpath "$.status" == "DRAFT"

GET {{host}}/api/v1/modules/{{module_id}}
HTTP 200
[Asserts]
jsonpath "$.process_id" == "Process_mod_{{run}}"
jsonpath "$.scope" == "tenant"

GET {{host}}/api/v1/modules
[QueryStringParams]
q: hurl module {{run}}
HTTP 200
[Asserts]
jsonpath "$.modules" count == 1

POST {{host}}/api/v1/modules/{{module_id}}/versions/{{v1}}/publish
HTTP 200
[Asserts]
jsonpath "$.status" == "PUBLISHED"
jsonpath "$.version_number" == 1

GET {{host}}/api/v1/modules/{{module_id}}/versions/{{v1}}/contract
HTTP 200
[Asserts]
jsonpath "$.slots" count == 1
jsonpath "$.tasks" count == 1
jsonpath "$.tasks[0].node_id" == "Task_prep"

GET {{host}}/api/v1/modules/{{module_id}}/usages
HTTP 200
[Asserts]
jsonpath "$.usages" count == 0

POST {{host}}/api/v1/modules/{{module_id}}/versions
{}
HTTP 201
[Asserts]
jsonpath "$.status" == "DRAFT"

GET {{host}}/api/v1/modules/{{module_id}}/versions
HTTP 200
[Asserts]
jsonpath "$.versions" count == 2

DELETE {{host}}/api/v1/modules/{{module_id}}
HTTP 200
[Asserts]
jsonpath "$.status" == "ARCHIVED"
