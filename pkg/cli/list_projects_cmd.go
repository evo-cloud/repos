package cli

import (
	"context"
	"sort"
)

// ListProjectsCmd provides a command to list projects.
type ListProjectsCmd struct {
}

// Execute executes the command.
func (c *ListProjectsCmd) Execute(ctx context.Context, cctx *Context, args ...string) error {
	projects := cctx.Repo.Projects()
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Name < projects[j].Name
	})
	cctx.UI.PrintProjectList(projects)
	return nil
}
