# jape

[![GoDoc](https://godoc.org/go.sia.tech/jape?status.svg)](https://godoc.org/go.sia.tech/jape)

`jape` is a "micro-framework" for building JSON-based HTTP APIs. It includes:

- A generic client type that speaks JSON
- A `Context` type for handlers, with convenient methods for JSON and error handling
- A static analyzer that ensures parity between client and server definitions

## Usage

```go
import (
    "net/http"
    "go.sia.tech/jape"
)

func helloHandler(jc jape.Context) {
    var greeting string
    if jc.Decode(&greeting) == nil {
        jc.Encode(greeting + ", " + jc.PathParam("name"))
    }
}

func NewServer() http.Handler {
    return jape.Mux(map[string]jape.Handler{
        "POST /hello/:name": helloHandler,
    })
}

type Client struct {
    c jape.Client
}

func (c Client) Hello(name, greeting string) (resp int, err error) {
    err = c.c.POST("/hello/"+name, greeting, &resp)
    return
}
```

Did you notice the error in the example code? If we run `japecheck`, it reports:

```
example.go:24:43 Client has wrong response type for POST /hello/:name (got int, should be string)
```

Note that `japecheck` is defined in a separate module to avoid dragging in
unnecessary dependencies. To install it, run `go install go.sia.tech/jape/japecheck`.
You can then run it as a pre-commit hook like so:

```bash
echo -e "#!/usr/bin/env bash\njapecheck ./api" > .git/hooks/pre-commit
chmod +x .git/hooks/pre-commit
```
