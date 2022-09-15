package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
)

const Doc = `enforce jape client/server parity

The japecheck analysis reports mismatches between the API endpoints
defined by a server and the methods defined by a client.
`

var Analyzer = &analysis.Analyzer{
	Name:             "japecheck",
	Doc:              Doc,
	Run:              run,
	RunDespiteErrors: true,
}

var clientPrefix string
var serverPrefix string

func init() {
	Analyzer.Flags.StringVar(&clientPrefix, "cprefix", "", "client endpoint URL prefix to trim")
	Analyzer.Flags.StringVar(&serverPrefix, "sprefix", "", "server endpoint URL prefix to trim")
}

type route struct {
	method string
	path   string

	request  ast.Expr
	response ast.Expr

	seen bool
}

func (r route) String() string { return r.method + " " + r.path }

func evalConstString(expr ast.Expr, info *types.Info) string {
	switch v := expr.(type) {
	case *ast.BasicLit:
		lit, err := strconv.Unquote(v.Value)
		if err != nil {
			return v.Value
		}
		return lit
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			panic(fmt.Sprintf("unhandled op type (%v)", v.Op))
		}
		return evalConstString(v.X, info) + evalConstString(v.Y, info)
	case *ast.CallExpr:
		if types.ExprString(v.Fun) == "fmt.Sprintf" {
			return evalConstString(v.Args[0], info)
		}
		return "%s"
	case *ast.Ident:
		return "%s"
	default:
		panic(fmt.Sprintf("unhandled expr type (%T)", expr))
	}
}

func parseRouteDef(kv *ast.KeyValueExpr, pass *analysis.Pass) (*route, bool) {
	typeof := func(e ast.Expr) types.Type {
		t, ok := pass.TypesInfo.Types[e]
		if !ok {
			return nil
		}
		return t.Type
	}

	methodPath := strings.Fields(evalConstString(kv.Key, pass.TypesInfo))
	if len(methodPath) != 2 {
		pass.Report(analysis.Diagnostic{
			Pos:     kv.Pos(),
			Message: fmt.Sprintf("Server defines invalid route: %q", methodPath),
		})
		return nil, false
	}

	r := &route{
		method: methodPath[0],
		path:   strings.TrimPrefix(methodPath[1], serverPrefix),
	}

	// lookup funcBody
	gotoDef := func(id *ast.Ident) ast.Node {
		obj := pass.TypesInfo.ObjectOf(id)
		for _, file := range pass.Files {
			path, _ := astutil.PathEnclosingInterval(file, obj.Pos(), obj.Pos())
			if len(path) == 1 {
				continue // not the right file
			}
			for _, n := range path {
				if f, ok := n.(*ast.FuncDecl); ok && f.Name.Name == id.Name {
					return f.Body
				}
			}
		}
		return nil
	}
	var funcBody ast.Node
	switch v := kv.Value.(type) {
	case *ast.FuncLit:
		funcBody = v.Body
	case *ast.Ident:
		funcBody = gotoDef(v)
	case *ast.SelectorExpr:
		funcBody = gotoDef(v.Sel)
	}
	if funcBody == nil {
		pass.Report(analysis.Diagnostic{
			Pos:     kv.Pos(),
			Message: "Could not locate handler definition",
		})
		return nil, false
	}

	ast.Inspect(funcBody, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); !ok {
			return true
		} else if sel, ok := call.Fun.(*ast.SelectorExpr); !ok {
			return true
		} else if typ := typeof(sel.X); typ == nil || typ.String() != "go.sia.tech/jape.Context" {
			return true
		} else {
			switch sel.Sel.Name {
			case "Custom":
				// TODO: validate
				r.request = call.Args[0]
				r.response = call.Args[1]
			case "Decode":
				if r.method == "GET" || r.method == "DELETE" {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Pos(),
						Message: fmt.Sprintf("%v routes should not read a request object", r.method),
					})
					return false
				}
				if _, ok := typeof(call.Args[0]).(*types.Pointer); !ok {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Args[0].Pos(),
						Message: "Decode called on non-pointer value",
					})
					return false
				}
				if r.request != nil && !types.Identical(typeof(call.Args[0]), typeof(r.request)) {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Pos(),
						Message: fmt.Sprintf("Decode called on %v, but was previously called on %v", typeof(call.Args[0]), typeof(r.request)),
					})
					return false
				}
				r.request = call.Args[0]
			case "Encode":
				if r.method == "PUT" || r.method == "DELETE" {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Pos(),
						Message: fmt.Sprintf("%v routes should not write a response object", r.method),
					})
					return false
				}
				if r.response != nil && !types.Identical(typeof(call.Args[0]), typeof(r.response)) {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Pos(),
						Message: fmt.Sprintf("Encode called on %v, but was previously called on %v", typeof(call.Args[0]), typeof(r.response)),
					})
					return false
				}
				r.response = call.Args[0]
			}
			return false
		}
	})

	if r.method == "GET" && r.response == nil {
		pass.Report(analysis.Diagnostic{
			Pos:     funcBody.Pos(),
			Message: fmt.Sprintf("%v routes should write a response object", r.method),
		})
		return nil, false
	}

	return r, true
}

func parseRouteCall(call *ast.CallExpr, pass *analysis.Pass) *route {
	if call.Fun.(*ast.SelectorExpr).Sel.Name == "Custom" {
		return &route{
			method:   evalConstString(call.Args[0], pass.TypesInfo),
			path:     strings.TrimPrefix(evalConstString(call.Args[1], pass.TypesInfo), clientPrefix),
			request:  call.Args[2],
			response: call.Args[3],
		}
	}

	r := &route{
		method: call.Fun.(*ast.SelectorExpr).Sel.Name,
		path:   strings.TrimPrefix(evalConstString(call.Args[0], pass.TypesInfo), clientPrefix),
	}
	switch r.method {
	case "GET":
		r.response = call.Args[1]
	case "POST":
		r.request = call.Args[1]
		r.response = call.Args[2]
	case "PUT":
		r.request = call.Args[1]
	}
	return r
}

func definesClient(file *ast.File, pass *analysis.Pass) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		} else if sel, ok := call.Fun.(*ast.SelectorExpr); !ok {
			return true
		} else if typ, ok := pass.TypesInfo.Types[sel.X]; ok && typ.Type.String() == "go.sia.tech/jape.Client" {
			found = true
			return false
		}
		return true
	})
	return found
}

func definesServer(file *ast.File, pass *analysis.Pass) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		} else if e, ok := n.(ast.Expr); !ok {
			return true
		} else if typ, ok := pass.TypesInfo.Types[e]; ok && typ.Type.String() == "map[string]go.sia.tech/jape.Handler" {
			found = true
			return false
		}
		return true
	})
	return found
}

func run(pass *analysis.Pass) (interface{}, error) {
	// find client and server definitions
	var clientFile, serverFile *ast.File
	for _, file := range pass.Files {
		if definesClient(file, pass) {
			clientFile = file
		}
		if definesServer(file, pass) {
			serverFile = file
		}
	}
	if serverFile == nil {
		return nil, nil // irrelevant package
	} else if clientFile == nil {
		return nil, errors.New("no Client definition found")
	}
	typeof := func(n ast.Node) types.Type {
		e, ok := n.(ast.Expr)
		if !ok {
			return nil
		}
		t, ok := pass.TypesInfo.Types[e]
		if !ok {
			return nil
		}
		return t.Type
	}

	// parse server routes
	routes := make(map[string]*route)
	done := false
	ast.Inspect(serverFile, func(n ast.Node) bool {
		if done {
			return false
		} else if typ := typeof(n); typ == nil || typ.String() != "map[string]go.sia.tech/jape.Handler" {
			return true
		}
		for _, elt := range n.(*ast.CompositeLit).Elts {
			r, ok := parseRouteDef(elt.(*ast.KeyValueExpr), pass)
			if !ok {
				continue
			}
			// normalize path
			split := strings.Split(r.path, "/")
			for i := range split {
				if strings.HasPrefix(split[i], ":") || strings.HasPrefix(split[i], "*") {
					split[i] = "%s"
				}
			}
			key := r.method + " " + strings.Join(split, "/")
			routes[key] = r
		}
		done = true
		return false
	})

	// compare to client routes
	ast.Inspect(clientFile, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		} else if sel, ok := call.Fun.(*ast.SelectorExpr); !ok {
			return true
		} else if typ := typeof(sel.X); typ == nil || typ.String() != "go.sia.tech/jape.Client" {
			return true
		} else if m := sel.Sel.Name; m != "GET" && m != "POST" && m != "PUT" && m != "DELETE" && m != "Custom" {
			return true
		}
		r := parseRouteCall(call, pass)

		// normalize path
		path := r.path
		if split := strings.Split(r.path, "?"); len(split) > 1 {
			path = split[0]
		}
		split := strings.Split(path, "/")
		for i := range split {
			if strings.HasPrefix(split[i], "%") && len(split[i]) > 1 {
				split[i] = "%s"
			}
		}
		key := r.method + " " + strings.Join(split, "/")

		// compare against server
		sr, ok := routes[key]
		if !ok {
			pass.Report(analysis.Diagnostic{
				Pos:     n.Pos(),
				Message: fmt.Sprintf("Client references route not defined by server: %v", r),
			})
			return true
		} else if sr.seen {
			// TODO: maybe allow this?
			pass.Report(analysis.Diagnostic{
				Pos:     n.Pos(),
				Message: fmt.Sprintf("Client references %v multiple times", r),
			})
			return true
		}
		sr.seen = true

		if r.request != nil {
			got := typeof(r.request)
			want := typeof(sr.request)
			if p, ok := want.(*types.Pointer); ok {
				want = p.Elem()
			}
			if !types.Identical(got, want) {
				pass.Report(analysis.Diagnostic{
					Pos:     r.request.Pos(),
					Message: fmt.Sprintf("Client has wrong request type for %v (got %v, should be %v)", sr, got, want),
				})
			}
		}
		if r.response != nil {
			got := typeof(r.response)
			want := types.Type(types.NewPointer(typeof(sr.response)))
			if sr.response == nil {
				want = types.Typ[types.UntypedNil]
			}
			if !types.Identical(got, want) {
				pass.Report(analysis.Diagnostic{
					Pos:     r.response.Pos(),
					Message: fmt.Sprintf("Client has wrong response type for %v (got %v, should be %v)", sr, got, want),
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
