package jape

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestCompression(t *testing.T) {
	// compressible is large enough to be worth compressing; tooSmall is not
	compressible := make([]string, 200)
	for i := range compressible {
		compressible[i] = "a compressible response body"
	}
	tooSmall := []string{"below the minimum size for compression"}

	srv := httptest.NewServer(Mux(map[string]Handler{
		"GET /compressed": func(c Context) {
			c.Encode(compressible)
		},
		"GET /uncompressed": func(c Context) {
			c.Encode(tooSmall)
		},
	}))
	defer srv.Close()

	// this client does not advertise compression of its own accord, so that the
	// response is seen exactly as the server sent it
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	defer client.CloseIdleConnections()

	tests := []struct {
		route          string
		acceptEncoding string
		expected       string
	}{
		{"/compressed", "gzip", "gzip"},
		{"/compressed", "zstd", "zstd"},
		{"/compressed", "", ""},
		{"/uncompressed", "gzip, zstd", ""},
	}

	for _, test := range tests {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+test.route, nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("Accept-Encoding", test.acceptEncoding)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to send request: %v", err)
		}
		resp.Body.Close()

		if enc := resp.Header.Get("Content-Encoding"); enc != test.expected {
			t.Fatalf("expected %v %q to be encoded with %q, got %q", test.route, test.acceptEncoding, test.expected, enc)
		}
	}

	// the jape client must handle both a response the server chose to compress
	// and one it passed through
	c := Client{BaseURL: srv.URL}

	var got []string
	if err := c.GET(context.Background(), "/compressed", &got); err != nil {
		t.Fatalf("failed to get compressed response: %v", err)
	} else if !reflect.DeepEqual(got, compressible) {
		t.Fatalf("expected %q, got %q", compressible, got)
	}

	got = nil
	if err := c.GET(context.Background(), "/uncompressed", &got); err != nil {
		t.Fatalf("failed to get uncompressed response: %v", err)
	} else if !reflect.DeepEqual(got, tooSmall) {
		t.Fatalf("expected %q, got %q", tooSmall, got)
	}
}
