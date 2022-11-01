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
	"golang.org/x/tools/go/analysis/passes/ctrlflow"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/cfg"
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
	Requires: []*analysis.Analyzer{
		inspect.Analyzer,
		ctrlflow.Analyzer,
	},
}

var clientPrefix string
var serverPrefix string

func init() {
	Analyzer.Flags.StringVar(&clientPrefix, "cprefix", "", "client endpoint URL prefix to trim")
	Analyzer.Flags.StringVar(&serverPrefix, "sprefix", "", "server endpoint URL prefix to trim")
}

func isPtr(t types.Type) bool {
	_, ok := t.(*types.Pointer)
	return ok
}

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

type serverParam struct {
	name string
	typ  types.Type
}

type serverRoute struct {
	method string
	path   string

	pathParams  []serverParam
	queryParams map[string]types.Type
	request     types.Type
	response    types.Type

	seen bool
}

func (r serverRoute) String() string { return r.method + " " + r.path }

func (r serverRoute) normalizedRoute() string {
	split := strings.Split(r.path, "/")
	for i := range split {
		if strings.HasPrefix(split[i], ":") || strings.HasPrefix(split[i], "*") {
			split[i] = "%s"
		}
	}
	return r.method + " " + strings.Join(split, "/")
}

func parseServerRoute(kv *ast.KeyValueExpr, pass *analysis.Pass) (*serverRoute, bool) {
	typeof := func(e ast.Expr) types.Type { return pass.TypesInfo.TypeOf(e) }

	methodPath := strings.Fields(evalConstString(kv.Key, pass.TypesInfo))
	if len(methodPath) != 2 {
		pass.Report(analysis.Diagnostic{
			Pos:     kv.Pos(),
			Message: fmt.Sprintf("Server defines invalid route: %q", methodPath),
		})
		return nil, false
	}

	r := &serverRoute{
		method:      methodPath[0],
		path:        strings.TrimPrefix(methodPath[1], serverPrefix),
		queryParams: make(map[string]types.Type),
	}
	// parse path params
	for _, param := range strings.Split(r.path, "/") {
		if strings.HasPrefix(param, ":") || strings.HasPrefix(param, "*") {
			r.pathParams = append(r.pathParams, serverParam{name: param[1:]})
		}
	}

	// lookup funcBody
	gotoDef := func(id *ast.Ident) (*ast.FuncDecl, ast.Node) {
		obj := pass.TypesInfo.ObjectOf(id)
		for _, file := range pass.Files {
			path, _ := astutil.PathEnclosingInterval(file, obj.Pos(), obj.Pos())
			if len(path) == 1 {
				continue // not the right file
			}
			for _, n := range path {
				if f, ok := n.(*ast.FuncDecl); ok && f.Name.Name == id.Name {
					return f, f.Body
				}
			}
		}
		return nil, nil
	}
	var fl *ast.FuncLit
	var fd *ast.FuncDecl
	var funcBody ast.Node
	switch v := kv.Value.(type) {
	case *ast.FuncLit:
		fl, funcBody = v, v.Body
	case *ast.Ident:
		fd, funcBody = gotoDef(v)
	case *ast.SelectorExpr:
		fd, funcBody = gotoDef(v.Sel)
	}
	if funcBody == nil {
		pass.Report(analysis.Diagnostic{
			Pos:     kv.Pos(),
			Message: "Could not locate handler definition",
		})
		return nil, false
	} else if fl == nil && fd == nil {
		pass.Report(analysis.Diagnostic{
			Pos:     kv.Pos(),
			Message: "Could not locate handler function declaration or literal",
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
				r.request = typeof(call.Args[0])
				r.response = typeof(call.Args[1])
				if r.request != types.Typ[types.UntypedNil] && !isPtr(r.request) {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Args[0].Pos(),
						Message: "request type must be a pointer",
					})
					return false
				}
				switch r.method {
				case "GET":
					if r.request != types.Typ[types.UntypedNil] {
						pass.Report(analysis.Diagnostic{
							Pos:     call.Args[0].Pos(),
							Message: fmt.Sprintf("%v routes should not read a request object", r.method),
						})
						return false
					}
					if r.response == types.Typ[types.UntypedNil] {
						pass.Report(analysis.Diagnostic{
							Pos:     call.Args[0].Pos(),
							Message: fmt.Sprintf("%v routes should write a response object", r.method),
						})
						return false
					}
				case "POST":
				case "PUT":
					if r.request == types.Typ[types.UntypedNil] {
						pass.Report(analysis.Diagnostic{
							Pos:     call.Args[0].Pos(),
							Message: fmt.Sprintf("%v routes should read a request object", r.method),
						})
						return false
					}
					if r.response != types.Typ[types.UntypedNil] {
						pass.Report(analysis.Diagnostic{
							Pos:     call.Args[1].Pos(),
							Message: fmt.Sprintf("%v routes should not write a response object", r.method),
						})
						return false
					}
				case "DELETE":
					if r.request != types.Typ[types.UntypedNil] {
						pass.Report(analysis.Diagnostic{
							Pos:     call.Args[0].Pos(),
							Message: fmt.Sprintf("%v routes should not read a request object", r.method),
						})
						return false
					}
					if r.response != nil {
						pass.Report(analysis.Diagnostic{
							Pos:     call.Args[1].Pos(),
							Message: fmt.Sprintf("%v routes should not write a response object", r.method),
						})
						return false
					}
				}

			case "Decode":
				if r.method == "GET" || r.method == "DELETE" {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Pos(),
						Message: fmt.Sprintf("%v routes should not read a request object", r.method),
					})
					return false
				}
				typ := typeof(call.Args[0])
				if !isPtr(typ) {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Args[0].Pos(),
						Message: "Decode called on non-pointer value",
					})
					return false
				}
				if r.request != nil && !types.Identical(typ, r.request) {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Args[0].Pos(),
						Message: fmt.Sprintf("Decode called on %v, but was previously called on %v", typ, r.request),
					})
					return false
				}
				r.request = typ

			case "Encode":
				if r.method == "PUT" || r.method == "DELETE" {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Pos(),
						Message: fmt.Sprintf("%v routes should not write a response object", r.method),
					})
					return false
				}
				typ := typeof(call.Args[0])
				if r.response != nil && !types.Identical(typ, r.response) {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Args[0].Pos(),
						Message: fmt.Sprintf("Encode called on %v, but was previously called on %v", typ, r.response),
					})
					return false
				}
				r.response = typ

			case "DecodeForm":
				name := evalConstString(call.Args[0], pass.TypesInfo)
				typ := typeof(call.Args[1])
				if prev, ok := r.queryParams[name]; ok && !types.Identical(prev, typ) {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Pos(),
						Message: fmt.Sprintf("form value %q decoded as %v, but was previously decoded as %v", name, typ, prev),
					})
					return false
				}
				r.queryParams[name] = typ

			case "DecodeParam":
				name := evalConstString(call.Args[0], pass.TypesInfo)
				typ := typeof(call.Args[1])
				var sp *serverParam
				for i := range r.pathParams {
					if r.pathParams[i].name == name {
						sp = &r.pathParams[i]
					}
				}
				if sp == nil {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Args[0].Pos(),
						Message: fmt.Sprintf("DecodeParam called on param (%q) not present in route definition", name),
					})
					return false
				} else if sp.typ != nil && !types.Identical(sp.typ, typ) {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Args[1].Pos(),
						Message: fmt.Sprintf("param %q decoded as %v, but was previously decoded as %v", name, typ, sp.typ),
					})
					return false
				} else if !isPtr(typ) {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Args[1].Pos(),
						Message: "DecodeParam called on non-pointer value",
					})
					return false
				}
				sp.typ = typ

			case "PathParam":
				name := evalConstString(call.Args[0], pass.TypesInfo)
				typ := types.NewPointer(types.Typ[types.String])
				var sp *serverParam
				for i := range r.pathParams {
					if r.pathParams[i].name == name {
						sp = &r.pathParams[i]
					}
				}
				if sp == nil {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Args[0].Pos(),
						Message: fmt.Sprintf("PathParam called on param (%q) not present in route definition", name),
					})
					return false
				} else if sp.typ != nil && !types.Identical(sp.typ, typ) {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Pos(),
						Message: fmt.Sprintf("param %q decoded as %v, but was previously decoded as %v", name, typ, sp.typ),
					})
					return false
				}
				sp.typ = typ
			}
			return true
		}
	})
	if r.request == nil {
		r.request = types.Typ[types.UntypedNil]
	}
	if r.response == nil {
		r.response = types.Typ[types.UntypedNil]
	}

	if r.method == "GET" && r.response == types.Typ[types.UntypedNil] {
		pass.Report(analysis.Diagnostic{
			Pos:     funcBody.Pos(),
			Message: fmt.Sprintf("%v routes should write a response object", r.method),
		})
		return nil, false
	} else if r.method == "PUT" && r.request == types.Typ[types.UntypedNil] {
		pass.Report(analysis.Diagnostic{
			Pos:     funcBody.Pos(),
			Message: fmt.Sprintf("%v routes should read a request object", r.method),
		})
		return nil, false
	}

	checkCfg := func(cfgg *cfg.CFG) {
		isCheckCall := func(pass *analysis.Pass, n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); !ok {
				return false
			} else if sel, ok := call.Fun.(*ast.SelectorExpr); !ok {
				return false
			} else if typ := typeof(sel.X); typ == nil || typ.String() != "go.sia.tech/jape.Context" {
				return false
			} else if sel.Sel.Name != "Check" {
				return false
			}
			return true
		}
		checkSuccessor := func(block *cfg.Block) bool {
			if len(block.Nodes) == 1 {
				if _, ok := block.Nodes[0].(*ast.ReturnStmt); ok {
					return true
				}
			}
			return false
		}
		for _, block := range cfgg.Blocks {
			for _, n := range block.Nodes {
				if e, ok := n.(*ast.BinaryExpr); ok {
					// check if jc.Check() != nil
					if isCheckCall(pass, e.X) && e.Op == token.NEQ {
						// check that successor is just a return statement
						if !checkSuccessor(cfgg.Blocks[block.Succs[0].Index]) {
							pass.Report(analysis.Diagnostic{
								Pos:     e.Pos(),
								Message: fmt.Sprintf("jc.Check() != nil doesn't immediately return"),
							})
						}
					}
				}
			}
		}
	}

	cfgs := pass.ResultOf[ctrlflow.Analyzer].(*ctrlflow.CFGs)
	if fd != nil {
		checkCfg(cfgs.FuncDecl(fd))
	} else if fl != nil {
		checkCfg(cfgs.FuncLit(fl))
	}

	return r, true
}

type clientRoute struct {
	method string
	path   string

	pathParams  []ast.Expr
	queryParams map[string]ast.Expr
	request     ast.Expr
	response    ast.Expr
}

func (r clientRoute) String() string { return r.method + " " + r.path }

func (r clientRoute) normalizedRoute() string {
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
	return r.method + " " + strings.Join(split, "/")
}

func parseClientRoute(call *ast.CallExpr, pass *analysis.Pass) *clientRoute {
	sprintfParse := func(r *clientRoute, expr ast.Expr) {
		r.queryParams = make(map[string]ast.Expr)
		if call, ok := expr.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && types.ExprString(sel) == "fmt.Sprintf" {
				nPath := strings.Count(r.path, "/%")
				nForm := strings.Count(r.path, "=%")
				if len(call.Args[1:]) != nPath+nForm {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Pos(),
						Message: fmt.Sprintf("route contains (%v path + %v form) = %v parameters, but only %v arguments are supplied", nPath, nForm, nPath+nForm, len(call.Args[1:])),
					})
					return
				}
				var queryParams []string
				if i := strings.Index(r.path, "?"); i != -1 {
					for _, part := range strings.Split(r.path[i+1:], "&") {
						if i := strings.Index(part, "=%"); i == -1 {
							continue // hard-coded form value
						} else {
							queryParams = append(queryParams, part[:i])
						}
					}
				}
				for i, arg := range call.Args[1:] {
					if i < nPath {
						r.pathParams = append(r.pathParams, arg)
					} else {
						r.queryParams[queryParams[i-nPath]] = arg
					}
				}
			}
		}
	}

	if call.Fun.(*ast.SelectorExpr).Sel.Name == "Custom" {
		r := &clientRoute{
			method:   evalConstString(call.Args[0], pass.TypesInfo),
			path:     strings.TrimPrefix(evalConstString(call.Args[1], pass.TypesInfo), clientPrefix),
			request:  call.Args[2],
			response: call.Args[3],
		}
		sprintfParse(r, call.Args[1])
		return r
	}

	r := &clientRoute{
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
	sprintfParse(r, call.Args[0])
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
		return pass.TypesInfo.TypeOf(e)
	}

	// parse server routes
	routes := make(map[string]*serverRoute)
	done := false
	ast.Inspect(serverFile, func(n ast.Node) bool {
		if done {
			return false
		} else if typ := typeof(n); typ == nil || typ.String() != "map[string]go.sia.tech/jape.Handler" {
			return true
		}
		for _, elt := range n.(*ast.CompositeLit).Elts {
			r, ok := parseServerRoute(elt.(*ast.KeyValueExpr), pass)
			if !ok {
				continue
			}
			routes[r.normalizedRoute()] = r
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
		cr := parseClientRoute(call, pass)
		if cr == nil {
			return true
		}

		// compare against server
		sr, ok := routes[cr.normalizedRoute()]
		if !ok {
			pass.Report(analysis.Diagnostic{
				Pos:     n.Pos(),
				Message: fmt.Sprintf("Client references route not defined by server: %v", cr),
			})
			return true
		} else if sr.seen {
			// TODO: maybe allow this?
			pass.Report(analysis.Diagnostic{
				Pos:     n.Pos(),
				Message: fmt.Sprintf("Client references %v multiple times", cr),
			})
			return true
		}
		sr.seen = true

		ptrTo := func(t types.Type) types.Type {
			if t == types.Typ[types.UntypedNil] {
				return t
			}
			return types.NewPointer(t)
		}
		elem := func(t types.Type) types.Type {
			if t, ok := t.(*types.Pointer); ok {
				return t.Elem()
			}
			return t
		}

		if cr.request != nil {
			got := typeof(cr.request)
			want := elem(sr.request)
			if !types.Identical(got, want) {
				pass.Report(analysis.Diagnostic{
					Pos:     cr.request.Pos(),
					Message: fmt.Sprintf("Client has wrong request type for %v (got %v, should be %v)", sr, got, want),
				})
			}
		}
		if cr.response != nil {
			got := typeof(cr.response)
			want := ptrTo(sr.response)
			if !types.Identical(got, want) {
				pass.Report(analysis.Diagnostic{
					Pos:     cr.response.Pos(),
					Message: fmt.Sprintf("Client has wrong response type for %v (got %v, should be %v)", sr, got, want),
				})
			}
		}
		for i := range sr.pathParams {
			if i >= len(cr.pathParams) {
				// TODO: this should be unreachable (routes[] lookup should fail)
				pass.Report(analysis.Diagnostic{
					Pos:     call.Pos(),
					Message: fmt.Sprintf("Client has too few path parameters for %v", sr),
				})
				continue
			}
			cp := cr.pathParams[i]
			sp := sr.pathParams[i]
			got := typeof(cp)
			want := elem(sp.typ)
			if !types.Identical(got, want) {
				pass.Report(analysis.Diagnostic{
					Pos:     cp.Pos(),
					Message: fmt.Sprintf("Client has wrong type for path parameter %q (got %v, should be %v)", sp.name, got, want),
				})
			}
		}
		for name, arg := range cr.queryParams {
			sq, ok := sr.queryParams[name]
			if !ok {
				pass.Report(analysis.Diagnostic{
					Pos:     arg.Pos(),
					Message: fmt.Sprintf("Client references undefined query parameter %q", name),
				})
				continue
			}
			got := typeof(arg)
			want := elem(sq)
			if !types.Identical(got, want) {
				pass.Report(analysis.Diagnostic{
					Pos:     arg.Pos(),
					Message: fmt.Sprintf("Client has wrong type for query parameter %q (got %v, should be %v)", name, got, want),
				})
			}
		}

		return true
	})

	for _, sr := range routes {
		if !sr.seen {
			pass.Report(analysis.Diagnostic{
				Pos:     clientFile.Pos(),
				Message: fmt.Sprintf("Client missing method for %v", sr),
			})
		}
	}

	return nil, nil
}
