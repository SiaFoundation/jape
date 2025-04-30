package jape

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"testing"

	"lukechampine.com/frand"
)

func TestRequestTooLarge(t *testing.T) {
	type (
		req struct {
			Foo []byte `json:"foo"`
		}

		resp struct {
			Bar string `json:"bar"`
		}
	)

	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()

	s := &http.Server{
		Handler: Mux(map[string]Handler{
			"POST /foo": func(c Context) {
				var r req
				if c.DecodeLimit(&r, 1000) != nil {
					return
				}

				resp := resp{
					Bar: hex.EncodeToString(r.Foo),
				}
				c.Encode(resp)
			},
		}),
	}
	defer s.Close()

	go func() {
		if err := s.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	}()

	c := Client{
		BaseURL:  "http://" + l.Addr().String(),
		Password: "",
	}

	var r resp
	err = c.req(context.Background(), http.MethodPost, "/foo", req{
		Foo: frand.Bytes(1001),
	}, &r)
	if err.Error() != "request body too large" {
		t.Fatalf(`expected "request body too large", got %q`, err)
	}

	content := frand.Bytes(64)
	err = c.req(context.Background(), http.MethodPost, "/foo", req{
		Foo: content,
	}, &r)
	if err != nil {
		t.Fatalf(`unexpected error: %v`, err)
	} else if r.Bar != hex.EncodeToString(content) {
		t.Fatalf(`expected %q, got %q`, hex.EncodeToString(content), r.Bar)
	}
}
