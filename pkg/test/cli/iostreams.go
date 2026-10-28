package cli

import (
	"bytes"
	"io"

	"github.com/tektoncd/pipelines-as-code/pkg/cli"
)

// NewIOStream return a fake iostreams.
func NewIOStream() (*cli.IOStreams, *bytes.Buffer) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &cli.IOStreams{
		In:     io.NopCloser(in),
		Out:    out,
		ErrOut: errOut,
	}, out
}
