---
default: minor
---

# Add compression by default if client supports it

Wrap both client and server handler using `github.com/klauspost/compress/gzhttp`. `Mux` now returns `http.Handler` (compressed wrapper) instead of `*httprouter.Router`.
