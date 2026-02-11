# Ping — basic health check
# Verifies the stereosd HTTP API is reachable.

GET http://localhost/v1/ping

HTTP 200
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$.status" == "ok"
