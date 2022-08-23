package checkapi

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const Doc = `compare client and server API routes

The checkapi analysis reports mismatches between the API endpoints
defined by a server and the methods defined by a client.
`

var ApiAnalyzer = &analysis.Analyzer{
	Name:             "checkapi",
	Doc:              Doc,
	Run:              run,
	RunDespiteErrors: true,
}

var clientPrefix string
var serverPrefix string

func init() {
	ApiAnalyzer.Flags.StringVar(&clientPrefix, "cprefix", "", "client endpoint URL prefix to trim")
	ApiAnalyzer.Flags.StringVar(&serverPrefix, "sprefix", "", "server endpoint URL prefix to trim")
}

type route struct {
	url    string
	method string
	// functionName string

	// requestTypes []string
	responseType string

	seen bool
}

func (r route) String() string { return r.method + " " + r.url }

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

func parseServer(file *ast.File, info *types.Info) (routes map[string]*route) {
	routes = make(map[string]*route)
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			selector, ok := v.Fun.(*ast.SelectorExpr)
			if !ok {
				return false
			} else if typ, ok := info.Types[selector.X]; !ok || typ.Type.String() != "*github.com/julienschmidt/httprouter.Router" {
				return false
			}

			// standardize url
			url := exprToString(v.Args[0], info, "")
			split := strings.Split(url, "/")
			for i := range split {
				// "/api/address/:id" -> "/api/address/%s"
				if strings.HasPrefix(split[i], ":") || strings.HasPrefix(split[i], "*") {
					split[i] = "%s"
				}
			}
			url = strings.TrimPrefix(strings.Join(split, "/"), serverPrefix)

			r := &route{
				url:    url,
				method: selector.Sel.Name,
			}

			functionName := v.Args[1].(*ast.SelectorExpr).Sel.Name
			for _, v := range file.Decls {
				switch v := v.(type) {
				case *ast.FuncDecl:
					if v.Recv == nil {
						continue
					} else if len(v.Recv.List) != 1 {
						continue
					} else if types.ExprString(v.Recv.List[0].Type) != "*server" {
						continue
					} else if v.Name == nil {
						continue
					} else if v.Name.Name != functionName {
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
						} else if ident, ok := call.Fun.(*ast.Ident); !ok || ident.Name != "WriteJSON" {
							continue
						} else if len(call.Args) != 2 {
							continue
						}
						r.responseType = info.Types[call.Args[1]].Type.String()
					}
				}
			}

			routes[r.String()] = r
		}
		return true
	})

	return
}

func run(pass *analysis.Pass) (interface{}, error) {
	// find client and server definitions
	var clientFile, serverFile *ast.File
	for _, file := range pass.Files {
		if file.Scope.Lookup("Client") != nil {
			clientFile = file
		} else if file.Scope.Lookup("server") != nil {
			serverFile = file
		}
	}
	if serverFile == nil {
		return nil, nil
	} else if clientFile == nil {
		return nil, errors.New("no Client definition found")
	}

	// parse server routes and compare to client routes
	routes := parseServer(serverFile, pass.TypesInfo)
	ast.Inspect(clientFile, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			var r route
			if len(v.Args) < 1 {
				return false
			}
			r.url = exprToString(v.Args[0], pass.TypesInfo, "")

			selector, ok := v.Fun.(*ast.SelectorExpr)
			if !ok {
				return false
			} else if selector.Sel == nil || selector.X == nil {
				return false
			} else if typ, ok := pass.TypesInfo.Types[selector.X]; !ok || !(strings.HasPrefix(typ.Type.String(), "*") && strings.HasSuffix(typ.Type.String(), "/api.Client")) {
				return false
			} else if selector.Sel.Name != "get" && selector.Sel.Name != "post" && selector.Sel.Name != "put" && selector.Sel.Name != "delete" {
				return false
			}
			// c.get -> "GET". c.put -> "PUT", etc
			r.method = strings.ToUpper(selector.Sel.Name)

			if r.method != "PUT" {
				responseType := pass.TypesInfo.Types[v.Args[len(v.Args)-1]].Type
				if pointer, ok := responseType.(*types.Pointer); ok {
					r.responseType = pointer.Elem().String()
				}
			}

			// remove query strings
			if split := strings.Split(r.url, "?"); len(split) > 1 {
				r.url = split[0]
			}
			split := strings.Split(r.url, "/")
			for i := range split {
				// replace all format strings with %s
				if strings.HasPrefix(split[i], "%") && len(split[i]) > 1 {
					split[i] = "%s"
				}
			}
			r.url = strings.TrimPrefix(strings.Join(split, "/"), clientPrefix)

			// compare against server
			sr, ok := routes[r.String()]
			if !ok {
				pass.Report(analysis.Diagnostic{
					Pos:     n.Pos(),
					Message: fmt.Sprintf("Client references route not defined by server: %v", r),
				})
				return true
			} else if sr.seen {
				pass.Report(analysis.Diagnostic{
					Pos:     n.Pos(),
					Message: fmt.Sprintf("Client references %v multiple times", r),
				})
				return true
			}
			sr.seen = true
			if r.responseType != sr.responseType {
				pass.Report(analysis.Diagnostic{
					Pos:     n.Pos(),
					Message: fmt.Sprintf("Client has wrong response type for %v (should %v)", r.url, sr.responseType),
				})
			}

		}
		return true
	})

	for _, r := range routes {
		if !r.seen {
			pass.Report(analysis.Diagnostic{
				Pos:     clientFile.Pos(),
				Message: fmt.Sprintf("Client missing method for %v", r),
			})
		}
	}

	return nil, nil
}
