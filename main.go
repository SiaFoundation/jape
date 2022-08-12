package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
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
			for _, result := range v.Type.Results.List {
				if types.ExprString(result.Type) != "error" {
					r.responseType = info.Types[result.Type].Type.String()
				}
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

						if expr, ok := v.Args[0].(*ast.BasicLit); ok {
							r.url = expr.Value
						} else if expr, ok := v.Args[0].(*ast.CallExpr); ok {
							if len(expr.Args) == 0 {
								continue
							}
							// if fmt.Sprintf, get first argument (format string)
							r.url = types.ExprString(expr.Args[0])
						}

						selector, ok := v.Fun.(*ast.SelectorExpr)
						if !ok {
							continue
						} else if selector.Sel == nil {
							continue
						}
						// c.get -> "GET". c.put -> "PUT", etc
						r.method = strings.ToUpper(selector.Sel.Name)
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

				var r route
				call, ok := v.X.(*ast.CallExpr)
				if !ok {
					continue
				} else if len(call.Args) != 2 {
					continue
				}
				r.url = call.Args[0].(*ast.BasicLit).Value

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
	if len(os.Args) == 1 {
		panic("Provide an API module directory to check")
	}

	for _, arg := range os.Args[1:] {
		pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo}, arg)
		if err != nil {
			panic(err)
		} else if len(pkgs) < 1 {
			panic("failed to find any packages")
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
		// remove query strings
		for i := range client {
			split := strings.Split(client[i].url, "?")
			if len(split) > 1 {
				client[i].url = split[0]
			}
		}
		// "/api/address/:id" -> "/api/address/%s"
		for i := range server {
			split := strings.Split(server[i].url, "/")
			for i := range split {
				if strings.HasPrefix(split[i], ":") {
					split[i] = "%s"
				}
			}
			server[i].url = strings.Join(split, "/")
		}

		routes := make(map[string][2]route)
		for _, r := range client {
			m := routes[r.url]
			m[0] = r
			routes[r.url] = m
		}
		for _, r := range server {
			m := routes[r.url]
			m[1] = r
			routes[r.url] = m
		}

		error := func(c, s route) {
			fmt.Fprintf(os.Stderr, "proble with client function %s (%s) or server function %s (%s)\n", c.functionName, c.url, s.functionName, s.url)
		}
		for url := range routes {
			m := routes[url]
			c, s := m[0], m[1]
			if c.url != s.url {
				error(c, s)
				fmt.Fprintf(os.Stderr, "missing route: client has url %s, server has url %s\n", c.url, s.url)
			} else if c.method != s.method {
				error(c, s)
				fmt.Fprintf(os.Stderr, "client has method %s, server has method %s\n", c.method, s.method)
			} else if c.responseType != s.responseType {
				error(c, s)
				fmt.Fprintf(os.Stderr, "client has return type %s, server has return type %s\n", c.responseType, s.responseType)
			}
		}

	}
}
