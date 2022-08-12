# checkapi

Program to check consistency in new siad client/server API interfaces.  It parses the source code of a given API module (i.e. go.sia.tech/renterd/api) and ensures that each route has the same URL, HTTP method, and response type. 

## Usage

Install:
```
$ go install go.sia.tech/checkapi
```

Example usage:
```
$ cd $GOPATH/src/go.sia.tech/walletd
$ checkapi ./api
```