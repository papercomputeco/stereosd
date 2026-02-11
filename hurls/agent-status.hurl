# Agent status — report an agent's status to stereosd (from agentd).

POST http://localhost/v1/agents/status
Content-Type: application/json
{
  "name": "opencode",
  "running": true,
  "session": "opencode-main"
}

HTTP 200
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$.status" == "ok"
