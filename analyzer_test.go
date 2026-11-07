package jape

import (
	"go/ast"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/ctrlflow"
)

func TestSingleResponseSelect(t *testing.T) {
	analyzer := &analysis.Analyzer{
		Name:     "singleresponse",
		Doc:      "check responses in select handlers",
		Requires: []*analysis.Analyzer{ctrlflow.Analyzer},
		Run: func(pass *analysis.Pass) (interface{}, error) {
			for _, file := range pass.Files {
				ast.Inspect(file, func(n ast.Node) bool {
					if kv, ok := n.(*ast.KeyValueExpr); ok {
						checkSingleResponse(kv, pass)
					}
					return true
				})
			}
			return nil, nil
		},
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "selectresponse")
}
