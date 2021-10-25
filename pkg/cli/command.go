package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"repos/pkg/repos"
)

// Command defines an abstract command.
type Command interface {
	Execute(ctx context.Context, cctx *Context, args ...string) error
}

// CommandFunc is the func form of Command.
type CommandFunc func(context.Context, *Context, ...string) error

// Execute implements Command.
func (f CommandFunc) Execute(ctx context.Context, cctx *Context, args ...string) error {
	return f(ctx, cctx, args...)
}

// TaskLogReader opens task log for reading.
type TaskLogReader func(task *repos.Task) (io.ReadCloser, error)

// EventHandlingOptions specifies options for how to handle task events.
type EventHandlingOptions struct {
	LogReader TaskLogReader
}

// UserInterface defines the abstraction for interacting with the user.
type UserInterface interface {
	TaskEventHandler(options EventHandlingOptions) repos.EventHandler
	PrintProjectList([]*repos.Project)
	PrintTargetList([]*repos.Target)
	PrintLog(io.Reader)
	PrintTaskStatus(name string, result *repos.TaskResult, outputs *repos.OutputFiles)
	PrintError(err error)
}

// Context provides information about the environment for commands.
type Context struct {
	Repo *repos.Repo
	UI   UserInterface
}

// ContextBuilder is used to build Context.
type ContextBuilder struct {
	WorkDir    string
	TextUI     bool
	LocalScope bool
}

// BuildContext creates a context.
func (b *ContextBuilder) BuildContext() (*Context, error) {
	c := &Context{
		UI: &TextPrinter{},
	}
	if !b.TextUI {
		if term := os.Getenv("TERM"); term != "" && term != "dumb" {
			c.UI = &TermPrinter{}
		}
	}
	scope := repos.RepoScopeGlobal
	if b.LocalScope {
		scope = repos.RepoScopeLocal
	}
	repo, err := repos.NewRepo(b.WorkDir, scope)
	if err != nil {
		c.UI.PrintError(err)
		return nil, err
	}
	if err := repo.LoadProjects(); err != nil {
		c.UI.PrintError(err)
		return nil, err
	}
	c.Repo = repo
	return c, nil
}

// BuildAndRun builds the context and runs the command.
func (b *ContextBuilder) BuildAndRun(ctx context.Context, cmd Command, args ...string) error {
	cctx, err := b.BuildContext()
	if err != nil {
		return err
	}
	return cctx.RunCmd(ctx, cmd, args...)
}

// RunCmd runs a command.
func (c *Context) RunCmd(ctx context.Context, cmd Command, args ...string) error {
	if err := cmd.Execute(ctx, c, args...); err != nil {
		c.UI.PrintError(err)
		return err
	}
	return nil
}

// MatchOneTarget matches exactly one target.
func (c *Context) MatchOneTarget(pattern string) (*repos.Target, error) {
	targets, err := c.Repo.ResolveTargets(pattern)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no target matches %q", pattern)
	}
	if len(targets) > 1 {
		return nil, fmt.Errorf("more than one targets match %q", pattern)
	}
	return targets[0], nil
}
