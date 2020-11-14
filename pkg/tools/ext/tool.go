// Package ext provides an "ext" tool for invoking external tools.
package ext

import (
	"context"
	"fmt"

	"repos/pkg/repos"
)

// Params defines the parameters.
type Params struct {
	Command string   `json:"command"`
	Env     []string `json:"env"`
}

// Tool defines the tool to be registered.
type Tool struct {
}

// Executor implements repos.ToolExecutor.
type Executor struct {
	CommandTemplate *repos.ToolParamTemplate
	EnvTemplates    []*repos.ToolParamTemplate
}

// CreateToolExecutor implements repos.Tool.
func (t *Tool) CreateToolExecutor(target *repos.Target) (repos.ToolExecutor, error) {
	var params Params
	err := target.ToolParamsAs(&params)
	if err != nil {
		return nil, fmt.Errorf("decode params error: %w", err)
	}
	if params.Command == "" {
		return nil, fmt.Errorf("missing parameter command")
	}

	x := &Executor{EnvTemplates: make([]*repos.ToolParamTemplate, len(params.Env))}
	x.CommandTemplate, err = repos.NewToolParamTemplate(params.Command)
	if err != nil {
		return nil, fmt.Errorf("invalid parameter command: %w", err)
	}

	for n, val := range params.Env {
		if x.EnvTemplates[n], err = repos.NewToolParamTemplate(val); err != nil {
			return nil, fmt.Errorf("invalid parameter env[%d]: %w", n, err)
		}
	}

	return x, nil
}

// Execute implements repos.ToolExecutor.
func (x *Executor) Execute(ctx context.Context, xctx *repos.ToolExecContext) error {
	command, err := x.CommandTemplate.ExecWith(xctx, nil)
	if err != nil {
		return fmt.Errorf("rendering parameter cli error: %w", err)
	}
	envs, err := xctx.RenderEnvs(x.EnvTemplates)
	if err != nil {
		return fmt.Errorf("envs: %w", err)
	}
	return repos.ExecuteExtToolCmd(ctx, xctx, xctx.ShellCommand(ctx, command), envs...)
}

func init() {
	repos.RegisterTool("ext", &Tool{})
}
