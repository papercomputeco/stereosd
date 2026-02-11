# List mounts — enumerate active shared directory mounts.

GET http://localhost/v1/mounts

HTTP 200
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$.mounts" isCollection
