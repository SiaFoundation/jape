package jape

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
)

// A Client provides methods for interacting with an API server.
type Client struct {
	BaseURL  string
	Password string
}

func (c *Client) req(method string, route string, data, resp interface{}) error {
	var body io.Reader
	if data != nil {
		js, _ := json.Marshal(data)
		body = bytes.NewReader(js)
	}
	req, err := http.NewRequest(method, fmt.Sprintf("%v%v", c.BaseURL, route), body)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Password != "" {
		req.SetBasicAuth("", c.Password)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer io.Copy(ioutil.Discard, r.Body)
	defer r.Body.Close()
	if !(200 <= r.StatusCode && r.StatusCode < 300) {
		err, _ := ioutil.ReadAll(r.Body)
		return errors.New(string(err))
	}
	if resp == nil {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(resp)
}

// GET performs a GET request, decoding the response into r.
func (c *Client) GET(route string, r interface{}) error { return c.req("GET", route, nil, r) }

// POST performs a POST request. If d is non-nil, it is encoded as the request
// body. If r is non-nil, the response is decoded into it.
func (c *Client) POST(route string, d, r interface{}) error { return c.req("POST", route, d, r) }

// PUT performs a PUT request, encoding d as the request body.
func (c *Client) PUT(route string, d interface{}) error { return c.req("PUT", route, d, nil) }

// DELETE performs a DELETE request.
func (c *Client) DELETE(route string) error { return c.req("DELETE", route, nil, nil) }

// Custom is a no-op that simply declares the request and response types used by
// a client method. This allows japecheck to be used on endpoints that do not
// speak JSON.
func (c *Client) Custom(method, route string, d, r interface{}) {}
