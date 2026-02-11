# List secrets — enumerate injected secret names.
# Run after inject-secret.hurl to see results.

GET http://localhost/v1/secrets

HTTP 200
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$.secrets" isCollection
