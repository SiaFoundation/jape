package main

import (
	"go.sia.tech/checkapi"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(checkapi.ApiAnalyzer)
}
