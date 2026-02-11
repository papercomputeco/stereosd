# Inject secret — write a secret to the tmpfs secret store.

POST http://localhost/v1/secrets
Content-Type: application/json
{
  "name": "TEST_SECRET",
  "value": "s3cret-value-123"
}

HTTP 201
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$.status" == "ok"
jsonpath "$.name" == "TEST_SECRET"
