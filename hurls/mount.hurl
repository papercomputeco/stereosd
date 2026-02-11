# Mount — mount a shared directory via virtio-fs.
# NOTE: This will fail outside a VM unless stereosd uses a mock commander.

POST http://localhost/v1/mounts
Content-Type: application/json
{
  "tag": "workspace0",
  "guest_path": "/workspace",
  "fs_type": "virtiofs"
}

HTTP 201
[Asserts]
header "Content-Type" contains "application/json"
jsonpath "$.status" == "ok"
jsonpath "$.tag" == "workspace0"
jsonpath "$.guest_path" == "/workspace"
