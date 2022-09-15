package jape

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/julienschmidt/httprouter"
)

// AuthMiddleware enforces HTTP Basic Authentication on the provided handler.
func AuthMiddleware(handler http.Handler, requiredPass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if _, password, ok := req.BasicAuth(); !ok || password != requiredPass {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, req)
	})
}

// A Context contains the values relevant to an HTTP handler.
type Context struct {
	ResponseWriter http.ResponseWriter
	Request        *http.Request
	PathParams     httprouter.Params
}

// Error writes err to the response body and returns it.
func (c Context) Error(err error, status int) error {
	http.Error(c.ResponseWriter, err.Error(), status)
	return err
}

// Check conditionally writes an error. If err is non-nil, Check prefixes it
// with msg, writes it to the response body (with status code 500), and returns
// it. Otherwise it returns nil.
func (c Context) Check(msg string, err error) error {
	if err != nil {
		return c.Error(fmt.Errorf("%v: %w", msg, err), http.StatusInternalServerError)
	}
	return nil
}

// Encode writes the JSON encoding of v to the response body.
func (c Context) Encode(v interface{}) {
	c.ResponseWriter.Header().Set("Content-Type", "application/json")
	// encode nil slices as [] and nil maps as {} (instead of null)
	if val := reflect.ValueOf(v); val.Kind() == reflect.Slice && val.Len() == 0 {
		c.ResponseWriter.Write([]byte("[]\n"))
		return
	} else if val.Kind() == reflect.Map && val.Len() == 0 {
		c.ResponseWriter.Write([]byte("{}\n"))
		return
	}
	enc := json.NewEncoder(c.ResponseWriter)
	enc.SetIndent("", "\t")
	enc.Encode(v)
}

// Decode decodes the JSON of the request body into v. If decoding fails, Decode
// writes an error to the response body and returns it.
func (c Context) Decode(v interface{}) error {
	if err := json.NewDecoder(c.Request.Body).Decode(v); err != nil {
		return c.Error(fmt.Errorf("couldn't decode request type (%T): %w", v, err), http.StatusBadRequest)
	}
	return nil
}

// PathParam returns the value of a path parameter. If the parameter is
// undefined, it returns the empty string.
func (c Context) PathParam(param string) string {
	return c.PathParams.ByName(param)
}

// DecodeParam decodes the specified path parameter into v, which must implement
// one of the following methods:
//
//   UnmarshalText([]byte) error
//   LoadString(string) error
//
// If decoding fails, DecodeParam writes an error to the response body and
// returns it.
func (c Context) DecodeParam(param string, v interface{}) error {
	var err error
	switch v := v.(type) {
	case interface{ UnmarshalText([]byte) error }:
		err = v.UnmarshalText([]byte(c.PathParam(param)))
	case interface{ LoadString(string) error }:
		err = v.LoadString(c.PathParam(param))
	default:
		panic("unsupported type")
	}
	if err != nil {
		return c.Error(fmt.Errorf("couldn't parse param %q: %w", param, err), http.StatusBadRequest)
	}
	return nil
}

// DecodeForm decodes the form value with the specified key into v, which must
// implement one of the following methods:
//
//   UnmarshalText([]byte) error
//   LoadString(string) error
//
// The following basic types are also supported:
//
//   *int
//   *bool
//
// If decoding fails, DecodeForm writes an error to the response body and
// returns it. If the form value is empty, no error is returned and v is
// unchanged.
func (c Context) DecodeForm(key string, v interface{}) error {
	value := c.Request.FormValue(key)
	if value == "" {
		return nil
	}
	var err error
	switch v := v.(type) {
	case interface{ UnmarshalText([]byte) error }:
		err = v.UnmarshalText([]byte(value))
	case interface{ LoadString(string) error }:
		err = v.LoadString(value)
	case *int:
		*v, err = strconv.Atoi(value)
	case *bool:
		*v, err = strconv.ParseBool(value)
	default:
		panic(fmt.Sprintf("unsupported type %T", v))
	}
	if err != nil {
		return c.Error(fmt.Errorf("invalid form value %q: %w", key, err), http.StatusBadRequest)
	}
	return nil
}

// A Handler handles HTTP requests.
type Handler func(Context)

func adaptor(h Handler) httprouter.Handle {
	return func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		h(Context{ResponseWriter: w, Request: req, PathParams: ps})
	}
}

// Mux returns an http.Handler for the provided set of routes. The map keys must
// contain both the method and path of the route, separated by whitespace, e.g.
// "GET /foo/:bar".
func Mux(routes map[string]Handler) *httprouter.Router {
	router := httprouter.New()
	for path, h := range routes {
		fs := strings.Fields(path)
		if len(fs) != 2 {
			panic(fmt.Sprintf("invalid route %q", path))
		}
		method, path := fs[0], fs[1]
		switch method {
		case "GET":
			router.GET(path, adaptor(h))
		case "POST":
			router.POST(path, adaptor(h))
		case "PUT":
			router.PUT(path, adaptor(h))
		case "DELETE":
			router.DELETE(path, adaptor(h))
		default:
			panic(fmt.Sprintf("unhandled method %q", method))
		}
	}
	return router
}
