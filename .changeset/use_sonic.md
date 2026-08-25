---
default: major
---

# Use sonic instead of encoding/json

Use bytedance's `sonic` library for encoding JSON instead of Go's standard `encoding/json`.  Applications using the jape server should now ensure that they do not depend on the ordering of JSON keys in responses or expect HTML contained in JSON strings to be escaped as a protection against XSS.
