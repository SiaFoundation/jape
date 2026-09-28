package jape

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestSonicFallback(t *testing.T) {
	type obj struct {
		Foo string         `json:"foo"`
		Bar map[string]int `json:"bar"`
	}

	srv := httptest.NewServer(Mux(map[string]Handler{
		"POST /echo": func(c Context) {
			var o obj
			if c.Decode(&o) == nil {
				c.Encode(o)
			}
		},
	}))
	defer srv.Close()

	// both implementations must produce exactly what encoding/json does,
	// including sorted map keys and escaped HTML
	o := obj{Foo: "<b>", Bar: map[string]int{"b": 2, "a": 1}}
	expected, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	defer func(old bool) { useSonic = old }(useSonic)
	tests := []struct {
		name     string
		useSonic bool
	}{
		{"sonic", true},
		{"encoding/json", false},
	}

	for _, test := range tests {
		useSonic = test.useSonic

		js, err := marshalJSON(o)
		if err != nil {
			t.Fatalf("%v: failed to marshal: %v", test.name, err)
		}
		resp, err := http.Post(srv.URL+"/echo", applicationJSON, bytes.NewReader(js))
		if err != nil {
			t.Fatalf("%v: failed to send request: %v", test.name, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%v: failed to read response: %v", test.name, err)
		} else if !bytes.Equal(body, expected) {
			t.Fatalf("%v: expected %q, got %q", test.name, expected, body)
		}

		c := Client{BaseURL: srv.URL}
		var got obj
		if err := c.POST(context.Background(), "/echo", o, &got); err != nil {
			t.Fatalf("%v: failed to post: %v", test.name, err)
		} else if !reflect.DeepEqual(got, o) {
			t.Fatalf("%v: expected %v, got %v", test.name, o, got)
		}
	}
}
