package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"repos/pkg/repos"
)

// LogCmd prints output of a task.
type LogCmd struct {
}

// Execute executes the command.
func (c *LogCmd) Execute(ctx context.Context, cctx *Context, args ...string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) > 1 {
		return fmt.Errorf("too many targets, please specify only one")
	}
	names, err := cctx.Repo.ResolveTargetNames(args[0])
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("%q: no target found", args[0])
	}
	if len(names) > 1 {
		return fmt.Errorf("%q: matches multiple targets", args[0])
	}
	logFn := filepath.Join(cctx.Repo.LogDir(), names[0]+".out")
	f, err := os.Open(logFn)
	if err != nil {
		return fmt.Errorf("open %q error: %w", logFn, err)
	}
	defer f.Close()
	cctx.UI.PrintLog(f)
	return nil
}

// OpenTaskLog opens the task output file.
func OpenTaskLog(task *repos.Task) (io.ReadCloser, error) {
	return os.Open(filepath.Join(task.Graph.Repo.LogDir(), task.Name()+".out"))
}
