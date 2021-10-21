package cli

import (
	"context"
	"errors"
	"fmt"

	"repos/pkg/repos"
)

// BuildCmd provides a build command.
type BuildCmd struct {
	Quiet bool
	Force bool
}

// Execute executes the command.
func (c *BuildCmd) Execute(ctx context.Context, cctx *Context, args ...string) error {
	names, err := cctx.Repo.ResolveTargetNames(args...)
	if err != nil {
		return err
	}
	_, err = c.Build(ctx, cctx, names...)
	return err
}

// Build builds the specified targets.
func (c *BuildCmd) Build(ctx context.Context, cctx *Context, targets ...string) (*repos.TaskGraph, error) {
	g, err := cctx.Repo.Plan(targets...)
	if err != nil {
		return nil, err
	}
	if c.Force {
		for _, name := range targets {
			if task := g.Tasks[name]; task != nil {
				task.NoSkip = true
			}
		}
	}
	disp := repos.NewDispatcher(g)
	var options EventHandlingOptions
	if !c.Quiet {
		options.LogReader = OpenTaskLog
	}
	disp.EventHandler = cctx.UI.TaskEventHandler(options)
	err = disp.Run(ctx)
	if err != nil {
		switch {
		case errors.Is(err, repos.ErrSomeTaskFailed) || errors.Is(err, repos.ErrIncomplete):
			err = fmt.Errorf(`some tasks failed, use "status|log TARGET" to inspect the details`)
		case errors.Is(err, context.DeadlineExceeded):
			err = fmt.Errorf("timeout")
		case errors.Is(err, context.Canceled):
			err = fmt.Errorf("canceled")
		}
	}
	return g, err
}
