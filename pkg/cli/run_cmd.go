package cli

import (
	"container/list"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"repos/pkg/repos"
)

// RunCmd executes the output executable from the specified target.
type RunCmd struct {
	Build BuildCmd
}

// Execute executes the command.
func (c *RunCmd) Execute(ctx context.Context, cctx *Context, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing TARGET or Executable")
	}
	target, err := cctx.MatchOneTarget(args[0])
	if err != nil {
		return err
	}
	g, err := c.Build.Build(ctx, cctx, target.Name.GlobalName())
	if err != nil {
		return err
	}
	task := g.Tasks[target.Name.GlobalName()]
	if task.Failed() {
		return task.Err
	}
	if task.Outputs == nil || task.Outputs.Primary == "" {
		return fmt.Errorf("no output")
	}

	visited := make(map[*repos.Task]struct{})
	var dirList list.List
	findSharedLibDirs(task, &dirList, visited)
	ldLibPath := os.Getenv("LD_LIBRARY_PATH")
	for elm := dirList.Front(); elm != nil; elm = elm.Next() {
		if ldLibPath != "" {
			ldLibPath = ":" + ldLibPath
		}
		ldLibPath = elm.Value.(string) + ldLibPath
	}

	execFn := filepath.Join(target.Project.OutDir(), task.Outputs.Primary)

	cmd := exec.Command(execFn, args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if ldLibPath != "" {
		for n := range cmd.Env {
			if strings.HasPrefix(cmd.Env[n], "LD_LIBRARY_PATH=") {
				cmd.Env = append(cmd.Env[:n], cmd.Env[n+1:]...)
			}
		}
		cmd.Env = append(cmd.Env, "LD_LIBRARY_PATH="+ldLibPath)
	}
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

func findSharedLibDirs(task *repos.Task, dirList *list.List, visited map[*repos.Task]struct{}) {
	visited[task] = struct{}{}
	for dep := range task.DepOn {
		if _, ok := visited[dep]; ok {
			continue
		}
		findSharedLibDirs(dep, dirList, visited)
	}
	if dir := task.Outputs.Extra["SHARED_LIB_DIR"]; dir != "" {
		dirList.PushBack(filepath.Join(task.Target.Project.OutDir(), dir))
	}
}
