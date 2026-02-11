# Health — full health status
# Returns lifecycle state, uptime, and agent info.

GET http://localhost/v1/health

HTTP 200
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$.state" exists
jsonpath "$.uptime_seconds" >= 0
