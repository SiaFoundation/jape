## 0.14.4 (2026-09-25)

### Features

#### Add compression by default if client supports it

Wrap both client and server handler using `github.com/klauspost/compress/gzhttp`. `Mux` now returns `http.Handler` (compressed wrapper) instead of `*httprouter.Router`.

### Fixes

#### restore Mux return type

##56 by @lukechampine

##54 broke compatibility by changing the return type of `Mux`. This PR restores it, by inlining the gzhttp wrapping into `adaptor`.

## 0.14.3 (2026-08-31)

### Fixes

- Update sonic dependency to 1.15.3

## 0.14.2 (2026-08-25)

### Fixes

#### Use sonic instead of encoding/json

Use bytedance's `sonic` library for encoding JSON instead of Go's standard `encoding/json`.

## 0.14.1 (2025-09-22)

### Fixes

- Add Content-Length to server response
- Update golang.org/x/tools from 0.32.0 to 0.37.0.

## 0.14.0 (2025-04-30)

### Breaking Changes

#### Limit the default size of incoming request bodies to 10MB

## Add `Context.DecodeLimit` to override the size limit

## 0.13.1 (2025-04-26)

### Fixes

- Automate releases
- Fix off-by-one in japecheck
