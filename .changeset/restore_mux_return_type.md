---
default: patch
---

# restore Mux return type

#56 by @lukechampine

#54 broke compatibility by changing the return type of `Mux`. This PR restores it, by inlining the gzhttp wrapping into `adaptor`.
