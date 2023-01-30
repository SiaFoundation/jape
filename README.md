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

To install `japecheck`, run `go install go.sia.tech/jape/japecheck`.
You can then run it as a pre-commit hook like so:

```bash
echo -e "#!/usr/bin/env bash\njapecheck ./api" > .git/hooks/pre-commit
chmod +x .git/hooks/pre-commit
```

## Use with Github Actions

To use with [action-golang-analysis](https://github.com/SiaFoundation/action-golang-analysis), create `.github/workflows/analyzer.yml` in your repository with the following contents:

```
name: Analyzer
on:
  pull_request:
    branches: [ master ]
  push:
    branches: [ master ]

jobs:
  analyzer:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v3
      - uses: SiaFoundation/action-golang-analysis@HEAD
        with:
          analyzers: |
            go.sia.tech/jape.Analyzer
```

It can also be added as a job within your existing workflow, like in [renterd](https://github.com/SiaFoundation/renterd/blob/master/.github/workflows/test.yml#L50).
