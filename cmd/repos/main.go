package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/subcommands"

	"repos/pkg/cli"
	_ "repos/pkg/tools/builtin"
)

const (
	projectsSynopsis = `List all projects`
	projectsUsage    = `projects
List all projects.
`
	targetsSynopsis = `List targets`
	targetsUsage    = `targets [PATTERN...]
List all targets or matched targets with specified patterns.

A pattern can be either
    PROJECT-PATTERN:TARGET-PATTERN
 or TARGET-PATTERN

A PROJECT-PATTERN/TARGET-PATTERN may contain:
    *   matches any sequence of characters
    ?   matches a single character
    [character-range]
        matches a single character in the specified range .e.g [a-z0-9]
        character-range can be
            c     a single character (c is not one of *, ?, \, ])
            \c    a single character
            lo-hi a single character c for lo <= c <= hi
    [^character-range]
        matches a single character NOT in the specified range.
    c   matches a single character as specified (c is not one of *, ? \, [)
    \c  matches a single character as c.

If a pattern is in the form of PROJECT-PATTERN:TARGET-PATTERN, projects are
matched using PROJECT-PATTERN, especially if PROJECT-PATTERN is empty, it 
matches the "current project" which is the project whose folder is the closest
ancestor of the current working directory. It fails if no such folder exists.
For TARGET-PATTERN, it's similar to PROJECT-PATTERN except empty string is
invalid.

If a pattern is in the form of TARGET-PATTERN, it's matches from all projects,
with an exception if TARGET-PATTERN is a plain name (without wildcards like
*, ?, or character-range, or escapes like \c), it matches a single target.
If more than one targets (from multiple projects) are matched, it's an error.
`
	checkSynopsis = `Check consistency of projects and targets`
	checkUsage    = `check
Check consistency of projects and targets.
`
	statusSynopsis = `Print task status`
	statusUsage    = `status TARGET
Print status of TARGET.
TARGET following the same matching rule as command "targets". except it should
match exact one target. Please checkout using "help targets".
Otherwise the command will fail.
`
	logSynopsis = `Print task logs`
	logUsage    = `log TARGET
Print log of TARGET.
TARGET following the same matching rule as command "targets" except it should
match exact one target. Please checkout using "help targets".
Otherwise the command will fail.
`
	buildSynopsis = `build targets`
	buildUsage    = `build TARGETS...
build targets.
TARGET following the same matching rule as command "targets". Please checkout
using "help targets".
`
	runSynopsis = `Execute the output executable from the specified target`
	runUsage    = `run TARGET ARGUMENTS...
Execute a target.
TARGET following the same matching rule as command "targets" except it should
match exact one target. Please checkout using "help targets".
`
)

var (
	contextBuilder cli.ContextBuilder
)

type flagBinder interface {
	SetFlags(*flag.FlagSet)
}

type cmdWrapper struct {
	name     string
	synopsis string
	usage    string
	command  cli.Command
}

func (w *cmdWrapper) Name() string     { return w.name }
func (w *cmdWrapper) Synopsis() string { return w.synopsis }
func (w *cmdWrapper) Usage() string    { return w.usage }
func (w *cmdWrapper) SetFlags(fs *flag.FlagSet) {
	if b, ok := w.command.(flagBinder); ok {
		b.SetFlags(fs)
	}
}
func (w *cmdWrapper) Execute(ctx context.Context, f *flag.FlagSet, args ...interface{}) subcommands.ExitStatus {
	return runCmd(ctx, w.command, f.Args()...)
}

func wrapCmd(cmd cli.Command, name, synopsis, usage string) *cmdWrapper {
	return &cmdWrapper{name: name, synopsis: synopsis, usage: usage, command: cmd}
}

func runCmd(ctx context.Context, cmd cli.Command, args ...string) subcommands.ExitStatus {
	if err := contextBuilder.BuildAndRun(ctx, cmd, args...); err != nil {
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

func registerCmd(cmd subcommands.Command, aliases ...string) {
	subcommands.Register(cmd, "")
	for _, alias := range aliases {
		subcommands.Register(subcommands.Alias(alias, cmd), "")
	}
}

func init() {
	flag.StringVar(&contextBuilder.WorkDir, "C", "", "Working directory.")
	flag.BoolVar(&contextBuilder.TextUI, "no-color", contextBuilder.TextUI, "Disable color terminal support.")

	subcommands.Register(subcommands.HelpCommand(), "")
	subcommands.Register(subcommands.FlagsCommand(), "")
	subcommands.Register(subcommands.CommandsCommand(), "")
	registerCmd(wrapCmd(&cli.ListProjectsCmd{}, "projects", projectsSynopsis, projectsUsage), "p")
	registerCmd(wrapCmd(&cli.ListTargetsCmd{}, "targets", targetsSynopsis, targetsUsage), "t")
	registerCmd(wrapCmd(&cli.CheckCmd{}, "check", checkSynopsis, checkUsage))
	registerCmd(wrapCmd(&cli.StatusCmd{}, "status", statusSynopsis, statusUsage), "st")
	registerCmd(wrapCmd(&cli.LogCmd{}, "log", logSynopsis, logUsage))
	registerCmd(wrapCmd(&cli.BuildCmd{}, "build", buildSynopsis, buildUsage), "b")
	registerCmd(wrapCmd(&cli.RunCmd{}, "run", runSynopsis, runUsage), "r")
}

func main() {
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
		<-sigCh
		os.Exit(1)
	}()
	os.Exit(int(subcommands.Execute(ctx)))
}
