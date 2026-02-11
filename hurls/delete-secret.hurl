# Delete secret — remove a previously injected secret.
# Assumes TEST_SECRET was injected via inject-secret.hurl.

DELETE http://localhost/v1/secrets/TEST_SECRET

HTTP 200
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$.status" == "ok"
jsonpath "$.name" == "TEST_SECRET"
