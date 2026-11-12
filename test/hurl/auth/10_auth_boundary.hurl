# Identity and role checks. Run without the shared identity headers: every request states its own.
GET {{host}}/api/v1/workflows
HTTP 401
[Asserts]
jsonpath "$.code" == "UNAUTHORIZED"

GET {{host}}/api/v1/workflows
X-Tenant-Id: not-a-uuid
X-User-Id: 018f2d3c-0000-7000-8000-0000000000a1
X-Tenant-Roles: tenant_admin
HTTP 401
[Asserts]
jsonpath "$.code" == "UNAUTHORIZED"

GET {{host}}/api/v1/workflows
X-Tenant-Id: {{tenant}}
X-User-Id: not-a-uuid
X-Tenant-Roles: tenant_admin
HTTP 401

GET {{host}}/api/v1/workflows
X-Tenant-Id: {{tenant}}
X-User-Id: 018f2d3c-0000-7000-8000-0000000000a1
X-Tenant-Roles: viewer
HTTP 200

POST {{host}}/api/v1/workflows
X-Tenant-Id: {{tenant}}
X-User-Id: 018f2d3c-0000-7000-8000-0000000000a1
X-Tenant-Roles: viewer
{"key":"hurl-viewer-{{run}}","name":"viewer","bpmn_xml":"<a/>"}
HTTP 403
[Asserts]
jsonpath "$.code" == "FORBIDDEN"

POST {{host}}/api/v1/modules
X-Tenant-Id: {{tenant}}
X-User-Id: 018f2d3c-0000-7000-8000-0000000000a1
X-Tenant-Roles: viewer
{"name":"viewer module","bpmn_xml":"<a/>"}
HTTP 403
