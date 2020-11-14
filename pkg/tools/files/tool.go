// Package files provides a tool for listing source files without
// performing actual work.
package files

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"repos/pkg/repos"
)

// Params defines the parameters.
type Params struct {
	Srcs   []string `json:"srcs"`
	Opaque []string `json:"opaque"`
}

// Tool defines the tool to be registered.
type Tool struct {
}

// Executor implements repos.ToolExecutor.
type Executor struct {
	Params Params
}

// CreateToolExecutor implements repos.Tool.
func (t *Tool) CreateToolExecutor(target *repos.Target) (repos.ToolExecutor, error) {
	x := &Executor{}
	err := target.ToolParamsAs(&x.Params)
	if err != nil {
		return nil, fmt.Errorf("decode params error: %w", err)
	}
	return x, nil
}

// Execute implements repos.ToolExecutor.
func (x *Executor) Execute(ctx context.Context, xctx *repos.ToolExecContext) error {
	cache := repos.NewFilesCache(xctx)
	for _, src := range x.Params.Srcs {
		var err error
		if strings.HasSuffix(src, string(filepath.Separator)) {
			err = cache.AddSourceRecursively(src)
		} else {
			err = cache.AddSource(src)
		}
		if err != nil {
			return err
		}
	}
	cache.AddOpaque(x.Params.Opaque...)
	if xctx.Skippable && cache.Verify() {
		return repos.ErrSkipped
	}
	cache.ClearSaved()
	cache.PersistOrLog()
	return nil
}

func init() {
	repos.RegisterTool("files", &Tool{})
}
