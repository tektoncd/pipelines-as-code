package main

import (
	"os"

	"github.com/tektoncd/pipelines-as-code/pkg/cmd/tknpac"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
)

func main() {
	clients := params.New()
	pac := tknpac.Root(clients)

	if err := pac.Execute(); err != nil {
		os.Exit(1)
	}
}
