---
default: patch
---

# Fall back to encoding/json on CPUs without PCLMULQDQ

bytedance's `sonic` library uses the PCLMULQDQ instruction without checking for support, crashing older amd64 CPUs that do not have it. On those CPUs, jape now uses Go's standard `encoding/json` instead.
