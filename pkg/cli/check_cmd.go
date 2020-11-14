package cli

import (
	"context"
)

// CheckCmd checks the integrity of all projects.
type CheckCmd struct {
}

// Execute executes the command.
func (c *CheckCmd) Execute(ctx context.Context, cctx *Context, args ...string) error {
	var names []string
	for _, project := range cctx.Repo.Projects() {
		for _, target := range project.Targets() {
			names = append(names, target.Name.GlobalName())
		}
	}
	_, err := cctx.Repo.Plan(names...)
	return err
}
