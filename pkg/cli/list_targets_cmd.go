package cli

import (
	"context"
	"fmt"
	"sort"

	"repos/pkg/repos"
)

// ListTargetsCmd provides a command to list targets.
type ListTargetsCmd struct {
}

// Execute executes the command.
func (c *ListTargetsCmd) Execute(ctx context.Context, cctx *Context, args ...string) error {
	targetSet := make(map[*repos.Target]struct{})
	if len(args) == 0 {
		for _, project := range cctx.Repo.Projects() {
			for _, target := range project.Targets() {
				targetSet[target] = struct{}{}
			}
		}
	} else {
		for _, pattern := range args {
			targets, err := cctx.Repo.ResolveTargets(pattern)
			if err != nil {
				return fmt.Errorf("%q: %w", pattern, err)
			}
			for _, target := range targets {
				targetSet[target] = struct{}{}
			}
		}
	}

	targets := make([]*repos.Target, 0, len(targetSet))
	for target := range targetSet {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Name.GlobalName() < targets[j].Name.GlobalName()
	})
	cctx.UI.PrintTargetList(targets)
	return nil
}
