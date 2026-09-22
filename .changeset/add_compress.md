---
default: minor
---

# Add Compress

`Compress` is a new `http.Handler` transformer that compresses responses with
zstd or gzip for clients that advertise support for it, e.g.
`jape.Compress(jape.Mux(routes))`. `Client` advertises support and transparently
decompresses such responses.
