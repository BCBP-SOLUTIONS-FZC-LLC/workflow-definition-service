# The shared answers of the API: problem bodies, status codes and headers.
GET {{host}}/api/v1/workflows/018f2d3c-0000-7000-8000-00000000dead
HTTP 404
[Asserts]
header "Content-Type" contains "application/problem+json"
jsonpath "$.code" == "NOT_FOUND"
jsonpath "$.detail" == "resource not found"
jsonpath "$.status" == 404

GET {{host}}/api/v1/workflows/not-a-uuid
HTTP 400
[Asserts]
jsonpath "$.code" == "BAD_REQUEST"

PATCH {{host}}/api/v1/workflows
HTTP 405
[Asserts]
header "Allow" contains "GET"
header "Allow" contains "POST"
jsonpath "$.code" == "METHOD_NOT_ALLOWED"

GET {{host}}/api/v1/workflows/
HTTP 404

GET {{host}}/api/v1/workflows
[QueryStringParams]
is_valid: yes
HTTP 400
[Asserts]
jsonpath "$.code" == "INVALID_INPUT"
jsonpath "$.invalid_params[0].name" == "is_valid"

POST {{host}}/api/v1/workflows
{"key":"hurl-blank","name":"   ","bpmn_xml":"<a/>"}
HTTP 422
[Asserts]
jsonpath "$.code" == "INVALID_INPUT"

POST {{host}}/api/v1/workflows
{"key":"hurl-notxml","name":"not xml","bpmn_xml":"<a><b></a>"}
HTTP 400
[Asserts]
jsonpath "$.code" == "INVALID_BPMN_XML"
