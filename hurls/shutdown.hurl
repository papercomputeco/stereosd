# Shutdown — initiate graceful shutdown.
# WARNING: This will actually shut down the VM when run inside StereOS.

POST http://localhost/v1/shutdown
Content-Type: application/json
{
  "reason": "operator requested",
  "grace_period_sec": 10
}

HTTP 202
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$.status" == "shutting_down"
