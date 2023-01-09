package main

import (
	"go.sia.tech/jape"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(jape.Analyzer)
}
