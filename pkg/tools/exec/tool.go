// Package exec provides an "exec" tool for invoking command.
package exec

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"repos/pkg/repos"
)

// Params defines the parameters.
type Params struct {
	Command    string            `json:"command"`
	ScriptFile string            `json:"script-file"`
	Args       []string          `json:"args"`
	Env        []string          `json:"env"`
	Srcs       []string          `json:"srcs"`
	Out        string            `json:"out"`
	ExtraOut   map[string]string `json:"extra-out"`
	Generated  []string          `json:"generated"`
	Opaque     []string          `json:"opaque"`
}

// Tool defines the tool to be registered.
type Tool struct {
}

// Executor implements repos.ToolExecutor.
type Executor struct {
	Params          Params
	CommandTemplate *repos.ToolParamTemplate
	ArgTemplates    []*repos.ToolParamTemplate
	EnvTemplates    []*repos.ToolParamTemplate
	OpaqueTemplates []*repos.ToolParamTemplate
}

// CreateToolExecutor implements repos.Tool.
func (t *Tool) CreateToolExecutor(target *repos.Target) (repos.ToolExecutor, error) {
	var params Params
	err := target.ToolParamsAs(&params)
	if err != nil {
		return nil, fmt.Errorf("decode params error: %w", err)
	}
	if params.Command == "" && params.ScriptFile == "" {
		return nil, fmt.Errorf("either command or script-file must be specified")
	}
	if params.Command != "" && params.ScriptFile != "" {
		return nil, fmt.Errorf("either command or script-file must be specified, but not both")
	}
	if params.Command != "" && len(params.Args) > 0 {
		return nil, fmt.Errorf("args can only be used with script-file, not command")
	}

	x := &Executor{
		Params:          params,
		ArgTemplates:    make([]*repos.ToolParamTemplate, len(params.Args)),
		EnvTemplates:    make([]*repos.ToolParamTemplate, len(params.Env)),
		OpaqueTemplates: make([]*repos.ToolParamTemplate, len(params.Opaque)),
	}
	if params.Command != "" {
		x.CommandTemplate, err = repos.NewToolParamTemplate(params.Command)
		if err != nil {
			return nil, fmt.Errorf("invalid parameter cli: %w", err)
		}
	}
	for n, val := range params.Args {
		if x.ArgTemplates[n], err = repos.NewToolParamTemplate(val); err != nil {
			return nil, fmt.Errorf("invalid parameter args[%d]: %w", n, err)
		}
	}
	for n, val := range params.Env {
		if x.EnvTemplates[n], err = repos.NewToolParamTemplate(val); err != nil {
			return nil, fmt.Errorf("invalid parameter env[%d]: %w", n, err)
		}
	}
	for n, val := range params.Opaque {
		if x.OpaqueTemplates[n], err = repos.NewToolParamTemplate(val); err != nil {
			return nil, fmt.Errorf("invalid parameter opaque[%d]: %w", n, err)
		}
	}
	return x, nil
}

// Execute implements repos.ToolExecutor.
func (x *Executor) Execute(ctx context.Context, xctx *repos.ToolExecContext) error {
	envs, err := xctx.RenderEnvs(x.EnvTemplates)
	if err != nil {
		return fmt.Errorf("envs: %w", err)
	}
	args, err := xctx.RenderTemplates(x.ArgTemplates)
	if err != nil {
		return fmt.Errorf("args: %w", err)
	}
	cr := &repos.CacheReporter{Cache: repos.NewFilesCache(xctx)}
	if x.Params.ScriptFile != "" {
		if err := cr.AddSource(x.Params.ScriptFile); err != nil {
			return err
		}
	}
	for _, src := range x.Params.Srcs {
		var err error
		if strings.HasSuffix(src, string(filepath.Separator)) {
			err = cr.AddSourceRecursively(src)
		} else {
			err = cr.AddSource(src)
		}
		if err != nil {
			return err
		}
	}
	if x.Params.Out != "" {
		cr.AddOutput("", x.Params.Out)
	}
	for key, val := range x.Params.ExtraOut {
		cr.AddOutput(key, val)
	}
	for _, gen := range x.Params.Generated {
		cr.AddGenerated(gen)
	}
	var command string
	if x.CommandTemplate != nil {
		var err error
		command, err = x.CommandTemplate.ExecWith(xctx, nil)
		if err != nil {
			return fmt.Errorf("rendering parameter command error: %w", err)
		}
		cr.AddOpaque(command)
	} else {
		cr.AddOpaque(x.Params.ScriptFile)
		cr.AddOpaque(args...)
	}
	cr.AddOpaque(envs...)
	cr.AddOpaque(x.Params.Opaque...)
	if xctx.Skippable && cr.Verify() {
		xctx.Output(*cr.SavedTaskOutputs())
		return repos.ErrSkipped
	}
	cr.ClearSaved()
	var cmd *exec.Cmd
	if x.CommandTemplate != nil {
		cmd = xctx.ShellCommand(ctx, command)
	} else {
		cmd = xctx.ShellScript(ctx, x.Params.ScriptFile, args...)
	}
	xctx.AddBinToPathFromDeps(cmd)
	xctx.ExtendEnv(cmd, envs...)
	if err := xctx.RunAndLog(cmd); err != nil {
		return err
	}
	xctx.PersistCacheOrLog(cr.Cache)
	xctx.Output(*cr.Cache.TaskOutputs())
	return nil
}

func init() {
	repos.RegisterTool("exec", &Tool{})
}
