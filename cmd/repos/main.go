package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"repos/pkg/cli"
	_ "repos/pkg/tools/builtin"
)

const (
	targetsUsage = `targets [PATTERN...]
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

	statusUsage = `status TARGET
Print status of TARGET.
TARGET following the same matching rule as command "targets".
Except it should match exact one target.
Please checkout using "targets --help".
Otherwise the command will fail.
`

	logUsage = `log TARGET
Print log of TARGET.
TARGET following the same matching rule as command "targets".
Except it should match exact one target.
Please checkout using "targets --help".
Otherwise the command will fail.
`

	buildUsage = `build TARGETS...
Build targets.
TARGET following the same matching rule as command "targets".
Please checkout using "targets --help".
`

	runUsage = `run TARGET ARGUMENTS...
Execute a target.
TARGET following the same matching rule as command "targets".
Except it should match exact one target.
Please checkout using "targets --help".
`
)

var (
	Version string

	contextBuilder cli.ContextBuilder
)

func cmdRunner(cmd cli.Command) func(c *cobra.Command, args []string) {
	return func(c *cobra.Command, args []string) {
		if err := contextBuilder.BuildAndRun(c.Context(), cmd, args...); err != nil {
			os.Exit(1)
		}
	}
}

func init() {
	cobra.EnableCommandSorting = false
}

func main() {
	name, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unknown current executable: %v\n", err)
		os.Exit(1)
	}
	bin := filepath.Base(name)

	cmd := &cobra.Command{
		Use:     bin,
		Version: Version,
		Short:   "Monorepo Build Tool",
	}
	cmd.PersistentFlags().StringVarP(
		&contextBuilder.WorkDir,
		"chdir", "C",
		"",
		"Working directory.",
	)
	cmd.PersistentFlags().BoolVar(
		&contextBuilder.TextUI,
		"script",
		contextBuilder.TextUI,
		"Disable color terminal support.",
	)

	listProjectsCmd := &cobra.Command{
		Use:     "projects",
		Aliases: []string{"p"},
		Short:   "List all projects.",
		Run:     cmdRunner(&cli.ListProjectsCmd{}),
	}
	cmd.AddCommand(listProjectsCmd)

	listTargetsCmd := &cobra.Command{
		Use:     targetsUsage,
		Aliases: []string{"t"},
		Short:   "List all targets or matched targets with specified patterns.",
		Run:     cmdRunner(&cli.ListTargetsCmd{}),
	}
	cmd.AddCommand(listTargetsCmd)

	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "Check consistency of projects and targets.",
		Run:   cmdRunner(&cli.CheckCmd{}),
	}
	cmd.AddCommand(checkCmd)

	statusCmd := &cobra.Command{
		Use:     statusUsage,
		Aliases: []string{"st"},
		Short:   "Print task status.",
		Run:     cmdRunner(&cli.StatusCmd{}),
	}
	cmd.AddCommand(statusCmd)

	logCmd := &cobra.Command{
		Use:     logUsage,
		Aliases: []string{"logs"},
		Short:   "Print task logs.",
		Run:     cmdRunner(&cli.LogCmd{}),
	}
	cmd.AddCommand(logCmd)

	buildCmd := &cobra.Command{
		Use:     buildUsage,
		Aliases: []string{"b"},
		Short:   "Build targets.",
		Run:     cmdRunner(&cli.BuildCmd{}),
	}
	cmd.AddCommand(buildCmd)

	runCmd := &cobra.Command{
		Use:     runUsage,
		Aliases: []string{"r"},
		Short:   "Execute the output executable from the specified target.",
		Run:     cmdRunner(&cli.RunCmd{}),
	}
	cmd.AddCommand(runCmd)

	cmd.Execute()
}
