package jape

import (
	"fmt"
	"net/http"

	"github.com/klauspost/compress/gzhttp"
)

// compressibleContentTypes are the content types worth compressing; content
// type parameters are ignored when matching.
var compressibleContentTypes = []string{
	applicationJSON,
	"application/cbor",
	"application/octet-stream",
	"text/html",
	"text/plain",
}

// minCompressSize is the smallest response worth compressing.
const minCompressSize = 1024 // 1 KB

// compress is a http.Handler transformer that compresses responses for clients
// that advertise support for it, preferring zstd over gzip. Responses that are
// too small, or of a content type that does not compress well, are passed
// through untouched. The options are static, so the transformer is built once.
var compress = func() func(http.Handler) http.HandlerFunc {
	wrapper, err := gzhttp.NewWrapper(
		gzhttp.ContentTypes(compressibleContentTypes),
		gzhttp.MinSize(minCompressSize),
	)
	if err != nil {
		panic(fmt.Sprintf("invalid compression options: %v", err))
	}
	return wrapper
}()
