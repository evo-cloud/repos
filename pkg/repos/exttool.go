package repos

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExtTool registers tool using external programs from output of a target.
type ExtTool struct {
	Task        *Task
	Executable  string
	ShellScript bool
	Envs        []string
	Args        []string
}

// ExtToolExecutor implements ToolExecutor.
type ExtToolExecutor struct {
	tool         *ExtTool
	envTemplates []*ToolParamTemplate
}

// CreateToolExecutor implements Tool.
func (t *ExtTool) CreateToolExecutor(target *Target) (ToolExecutor, error) {
	var params map[string]interface{}
	if err := target.ToolParamsAs(&params); err != nil {
		return nil, fmt.Errorf("decode params error: %w", err)
	}
	x := &ExtToolExecutor{
		tool:         t,
		envTemplates: make([]*ToolParamTemplate, 0, len(params)),
	}
	for key, val := range params {
		var valStr string
		if s, ok := val.(string); ok {
			valStr = s
		} else {
			valStr = fmt.Sprintf("%v", val)
		}
		tpl, err := NewToolParamTemplate("REPOS_TOOL_PARAM_" + key + "=" + valStr)
		if err != nil {
			return nil, fmt.Errorf("invalid parameter %s: %w", key, err)
		}
		x.envTemplates = append(x.envTemplates, tpl)
	}
	return x, nil
}

// Execute implements ToolExecutor.
func (x *ExtToolExecutor) Execute(ctx context.Context, xctx *ToolExecContext) error {
	envs := make([]string, len(x.tool.Envs)+len(x.envTemplates))
	copy(envs[:len(x.tool.Envs)], x.tool.Envs)
	ruleEnvs, err := xctx.RenderEnvs(x.envTemplates)
	if err != nil {
		return fmt.Errorf("envs: %w", err)
	}
	copy(envs[len(x.tool.Envs):], ruleEnvs)
	var cmd *exec.Cmd
	if x.tool.ShellScript {
		cmd = xctx.ShellScript(ctx, x.tool.Executable, x.tool.Args...)
	} else {
		cmd = xctx.Command(ctx, x.tool.Executable, x.tool.Args...)
	}
	return ExecuteExtToolCmd(ctx, xctx, cmd, envs...)
}

// ExecuteExtToolCmd executes the external program as a tool.
func ExecuteExtToolCmd(ctx context.Context, xctx *ToolExecContext, cmd *exec.Cmd, envs ...string) error {
	xctx.AddBinToPathFromDeps(cmd)
	xctx.ExtendEnv(cmd, envs...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	in, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe error: %w", err)
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe error: %w", err)
	}
	defer out.Close()

	xctx.Logger.Printf("CMD START %v", cmd.Args)
	if err := cmd.Start(); err != nil {
		xctx.Logger.Printf("CMD ERROR %v: %v", cmd.Args, err)
		return fmt.Errorf("start command %v error: %w", cmd.Args, err)
	}

	cr := &CacheReporter{Cache: NewFilesCache(xctx)}
	cr.AddOpaque(cmd.Args...)
	cr.AddOpaque(envs...)
	err = controlCmd(xctx, cr, in, out)
	execErr := cmd.Wait()
	if err != nil {
		if err == ErrSkipped {
			xctx.Output(*cr.SavedTaskOutputs())
		}
		return err
	}
	if execErr != nil {
		return execErr
	}
	cache := xctx.ReplayAndPersistCacheOrLog(cr, NewFilesCache(xctx))
	xctx.Output(*cache.TaskOutputs())
	return nil
}

func controlCmd(xctx *ToolExecContext, cache *CacheReporter, in io.WriteCloser, out io.Reader) error {
	defer in.Close()
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		cmd, val := line[0], line[1:]
		switch cmd {
		case 'S':
			var err error
			if strings.HasSuffix(val, string(filepath.Separator)) {
				err = cache.AddSourceRecursively(val[:len(val)-1])
			} else {
				err = cache.AddSource(val)
			}
			if err != nil {
				return err
			}
		case 'I':
			var err error
			if strings.HasSuffix(val, string(filepath.Separator)) {
				err = cache.AddInputRecursively(val[:len(val)-1])
			} else {
				err = cache.AddInput(val)
			}
			if err != nil {
				return err
			}
		case 'O':
			var key, relPath string
			items := strings.SplitN(val, ":", 2)
			if len(items) == 2 {
				key, relPath = items[0], items[1]
			} else {
				relPath = items[0]
			}
			cache.AddOutput(key, relPath)
		case 'G':
			cache.AddGenerated(val)
		case 'P':
			cache.AddOpaque(val)
		case 'V':
			if xctx.Skippable && cache.Verify() {
				fmt.Fprintln(in, "1")
			} else {
				fmt.Fprintln(in, "0")
			}
		case 'C':
			cache.ClearSaved()
		case 'X':
			return ErrSkipped
		}
	}
	return nil
}
