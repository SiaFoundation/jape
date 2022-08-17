package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
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

func replaceAddStrings(expr *ast.BinaryExpr, str string) string {
	if expr.Op != token.ADD {
		return str
	}

	if _, ok := expr.X.(*ast.BinaryExpr); ok {
		str = replaceAddStrings(expr, str)
	} else if _, ok := expr.X.(*ast.BasicLit); ok {
		lit, err := strconv.Unquote(expr.X.(*ast.BasicLit).Value)
		if err != nil {
			return ""
		}
		str += lit
	} else {
		str += "%s"
	}

	if _, ok := expr.Y.(*ast.BinaryExpr); ok {
		str = replaceAddStrings(expr, str)
	} else if _, ok := expr.Y.(*ast.BasicLit); ok {
		lit, err := strconv.Unquote(expr.Y.(*ast.BasicLit).Value)
		if err != nil {
			return ""
		}
		str += lit
	} else {
		str += "%s"
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

						if expr, ok := v.Args[0].(*ast.BasicLit); ok {
							url, err := strconv.Unquote(expr.Value)
							if err != nil {
								continue
							}
							r.url = url
						} else if expr, ok := v.Args[0].(*ast.CallExpr); ok {
							if len(expr.Args) == 0 {
								continue
							}
							// if fmt.Sprintf, get first argument (format string)
							url, err := strconv.Unquote(types.ExprString(expr.Args[0]))
							if err != nil {
								continue
							}
							r.url = url
						} else if expr, ok := v.Args[0].(*ast.BinaryExpr); ok && expr.Op == token.ADD {
							r.url = replaceAddStrings(expr, "")
						}

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
				if expr, ok := call.Args[0].(*ast.BasicLit); ok {
					url, err := strconv.Unquote(expr.Value)
					if err != nil {
						continue
					}
					r.url = url
				} else if expr, ok := call.Args[0].(*ast.BinaryExpr); ok && expr.Op == token.ADD {
					r.url = replaceAddStrings(expr, "")
				}

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
		// remove query strings
		for i := range client {
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
			client[i].url = strings.TrimPrefix(strings.Join(split, "/"), "/api")
		}
		for i := range server {
			split := strings.Split(server[i].url, "/")
			for i := range split {
				// "/api/address/:id" -> "/api/address/%s"
				if strings.HasPrefix(split[i], ":") || strings.HasPrefix(split[i], "*") {
					split[i] = "%s"
				}
			}
			server[i].url = strings.TrimPrefix(strings.Join(split, "/"), "/api")
		}

		routes := make(map[string][2]route)
		for _, r := range client {
			key := r.method + " " + r.url
			m := routes[key]
			m[0] = r
			routes[key] = m
		}
		for _, r := range server {
			key := r.method + " " + r.url
			m := routes[key]
			m[1] = r
			routes[key] = m
		}

		for url := range routes {
			m := routes[url]
			c, s := m[0], m[1]
			if len(c.url) == 0 && len(s.url) == 0 {
				continue
			} else if len(c.url) == 0 {
				fmt.Fprintf(os.Stderr, "Client missing route found on server: %s %s \n", s.method, s.url)
			} else if len(s.url) == 0 {
				fmt.Fprintf(os.Stderr, "Server missing route found on client: %s %s\n", c.method, c.url)
			} else if c.responseType != s.responseType {
				fmt.Fprintf(os.Stderr, "Client has different return type (%s) than server (%s) on route %s\n", c.responseType, s.responseType, url)
			}
		}

	}
}
