package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// StatusCmd prints status of a target.
type StatusCmd struct {
}

// Execute executes the command.
func (c *StatusCmd) Execute(ctx context.Context, cctx *Context, args ...string) error {
	if len(args) == 0 {
		return nil
	}
	names, err := cctx.Repo.ResolveTargetNames(args...)
	if err != nil {
		return err
	}
	for _, taskName := range names {
		taskResult, err := cctx.Repo.LoadTaskResult(taskName)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load result of %q: %w", taskName, err)
		}
		outputs, err := cctx.Repo.LoadTaskOutputs(taskName)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load outputs of %q: %w", taskName, err)
		}
		cctx.UI.PrintTaskStatus(taskName, taskResult, outputs)
	}
	return nil
}
