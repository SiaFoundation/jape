package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	apiPackage = "api"

	clientFilename = "client.go"
	serverFilename = "server.go"

	clientType = "*Client"
	serverType = "*server"

	newServer = "NewServer"
	writeJSON = "WriteJSON"
)

type route struct {
	url          string
	method       string
	functionName string

	requestTypes []string
	responseType string
}

func exprToString(expr ast.Expr, info *types.Info, str string) string {
	switch v := expr.(type) {
	case *ast.BasicLit:
		lit, err := strconv.Unquote(v.Value)
		if err != nil {
			return ""
		}
		str += lit
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			break
		}
		str = exprToString(v.X, info, str)
		str = exprToString(v.Y, info, str)
	case *ast.CallExpr:
		if len(v.Args) == 0 {
			returnType, ok := info.Types[v].Type.(*types.Basic)
			if !ok {
				break
			} else if returnType.Info() == types.IsString {
				str += "%s"
			}
		} else if types.ExprString(v.Fun) == "fmt.Sprintf" {
			// if Sprintf, get first argument
			str = exprToString(v.Args[0], info, str)
		}
	case *ast.Ident:
		if typ, ok := info.Types[v].Type.(*types.Basic); ok && typ.Info() == types.IsString {
			str += "%s"
		}
	}

	return str
}

func parseClient(file *ast.File, info *types.Info) (routes []route) {
	for _, decl := range file.Decls {
		switch v := decl.(type) {
		case *ast.FuncDecl:
			if v.Recv == nil {
				continue
			} else if len(v.Recv.List) != 1 {
				continue
			} else if types.ExprString(v.Recv.List[0].Type) != clientType {
				continue
			} else if v.Name == nil {
				continue
			} else if !ast.IsExported(v.Name.Name) {
				continue
			} else if v.Type == nil {
				continue
			}

			r := route{
				functionName: v.Name.Name,
			}
			for _, param := range v.Type.Params.List {
				r.requestTypes = append(r.requestTypes, info.Types[param.Type].Type.String())
			}

			for _, v := range v.Body.List {
				switch v := v.(type) {
				case *ast.AssignStmt:
					if len(v.Lhs) != 1 || len(v.Rhs) != 1 {
						continue
					} else if types.ExprString(v.Lhs[0]) != "err" {
						continue
					}

					switch v := v.Rhs[0].(type) {
					case *ast.CallExpr:
						if len(v.Args) < 1 {
							continue
						}

						r.url = exprToString(v.Args[0], info, "")

						selector, ok := v.Fun.(*ast.SelectorExpr)
						if !ok {
							continue
						} else if selector.Sel == nil {
							continue
						}
						// c.get -> "GET". c.put -> "PUT", etc
						r.method = strings.ToUpper(selector.Sel.Name)

						if r.method != "PUT" {
							responseType := info.Types[v.Args[len(v.Args)-1]].Type
							if pointer, ok := responseType.(*types.Pointer); ok {
								r.responseType = pointer.Elem().String()
							}
						}
					}
				}
			}
			routes = append(routes, r)
		}
	}

	return
}

func parseServer(file *ast.File, info *types.Info) (routes []route) {
	for _, decl := range file.Decls {
		switch v := decl.(type) {
		case *ast.FuncDecl:
			if v.Name == nil {
				continue
			} else if v.Name.Name != newServer {
				continue
			}

			for _, v := range v.Body.List {
				v, ok := v.(*ast.ExprStmt)
				if !ok {
					continue
				}

				call, ok := v.X.(*ast.CallExpr)
				if !ok {
					continue
				} else if len(call.Args) != 2 {
					continue
				}

				var r route
				r.url = exprToString(call.Args[0], info, "")

				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				r.method = selector.Sel.Name

				r.functionName = call.Args[1].(*ast.SelectorExpr).Sel.Name
				for _, v := range file.Decls {
					switch v := v.(type) {
					case *ast.FuncDecl:
						if v.Recv == nil {
							continue
						} else if len(v.Recv.List) != 1 {
							continue
						} else if types.ExprString(v.Recv.List[0].Type) != serverType {
							continue
						} else if v.Name == nil {
							continue
						} else if v.Name.Name != r.functionName {
							continue
						}

						for _, v := range v.Body.List {
							v, ok := v.(*ast.ExprStmt)
							if !ok {
								continue
							}
							call, ok := v.X.(*ast.CallExpr)
							if !ok {
								continue
							} else if ident, ok := call.Fun.(*ast.Ident); !ok || ident.Name != writeJSON {
								continue
							} else if len(call.Args) != 2 {
								continue
							}
							r.responseType = info.Types[call.Args[1]].Type.String()
						}
					}
				}
				routes = append(routes, r)
			}
		}
	}

	return
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("checkapi: ")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: checkapi [flags] [directory]\n\nFlags:")
		flag.PrintDefaults()
	}

	clientEndpointPrefix := flag.String("c", "/api", "client endpoint URL prefix to trim")
	serverEndpointPrefix := flag.String("s", "/api", "server endpoint URL prefix to trim")

	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		return
	}

	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo}, args[0])
	if err != nil {
		panic(err)
	} else if len(pkgs) < 1 {
		panic("Failed to find any packages")
	}
	api := pkgs[0]

	var client, server []route
	for i, file := range api.Syntax {
		base := filepath.Base(api.CompiledGoFiles[i])
		if base == clientFilename {
			client = parseClient(file, api.TypesInfo)
		} else if base == serverFilename {
			server = parseServer(file, api.TypesInfo)
		}
	}

	// standardize urls
	for i := range client {
		// remove query strings
		if split := strings.Split(client[i].url, "?"); len(split) > 1 {
			client[i].url = split[0]
		}

		split := strings.Split(client[i].url, "/")
		for i := range split {
			// replace all format strings with %s
			if strings.HasPrefix(split[i], "%") && len(split[i]) > 1 {
				split[i] = "%s"
			}
		}
		client[i].url = strings.TrimPrefix(strings.Join(split, "/"), *clientEndpointPrefix)
	}
	for i := range server {
		split := strings.Split(server[i].url, "/")
		for i := range split {
			// "/api/address/:id" -> "/api/address/%s"
			if strings.HasPrefix(split[i], ":") || strings.HasPrefix(split[i], "*") {
				split[i] = "%s"
			}
		}
		server[i].url = strings.TrimPrefix(strings.Join(split, "/"), *serverEndpointPrefix)
	}

	routes := make(map[string]struct{ c, s route })
	for _, r := range client {
		key := r.method + " " + r.url

		m := routes[key]
		m.c = r
		routes[key] = m
	}
	for _, r := range server {
		key := r.method + " " + r.url

		m := routes[key]
		m.s = r
		routes[key] = m
	}

	var errors []string
	for url := range routes {
		m := routes[url]
		if len(m.c.url) == 0 && len(m.s.url) == 0 {
			continue
		} else if len(m.c.url) == 0 {
			errors = append(errors, fmt.Sprintf("Client missing route found on server: %s %s", m.s.method, m.s.url))
		} else if len(m.s.url) == 0 {
			errors = append(errors, fmt.Sprintf("Server missing route found on client: %s %s", m.c.method, m.c.url))
		} else if m.c.responseType != m.s.responseType {
			errors = append(errors, fmt.Sprintf("Client has different return type (%s) than server (%s) on route %s", m.c.responseType, m.s.responseType, url))
		}
	}
	sort.Slice(errors, func(i, j int) bool {
		return errors[i] < errors[j]
	})

	for _, error := range errors {
		fmt.Fprintln(os.Stderr, error)
	}
}
