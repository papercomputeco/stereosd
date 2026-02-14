# Agent status — list agents polled from agentd.

GET http://localhost/v1/agents

HTTP 200
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$.agents" isCollection
