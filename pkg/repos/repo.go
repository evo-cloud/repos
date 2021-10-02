package repos

import (
	"container/list"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/karrick/godirwalk"
	"github.com/zabawaba99/go-gitignore"

	"repos/pkg/repos/meta"
)

const (
	outFolderName   = "out"
	logFolderName   = "log"
	cacheFolderName = "cache"
)

// Repo represents the monolithic repository.
type Repo struct {
	// RootDir is the absolute path to the root of the repository.
	RootDir string
	// WorkDir is the absolute path of current working directory (may be different from PWD).
	WorkDir string

	root           *meta.Root
	dataDir        string
	metaFolder     string
	projects       map[string]*Project
	currentProject *Project
}

// NewRepo creates a Repo from the specified directory as working directory.
// If wd is empty, the current working directory is used.
func NewRepo(workDir string) (*Repo, error) {
	var err error
	if workDir == "" {
		workDir, err = os.Getwd()
	} else {
		workDir, err = filepath.Abs(workDir)
	}
	if err != nil {
		return nil, err
	}
	r := &Repo{WorkDir: workDir}
	if err := r.LocateRoot(); err != nil {
		return nil, err
	}
	return r, nil
}

// LocateRoot find the root of the repository from working directory.
func (r *Repo) LocateRoot() error {
	wd, err := filepath.Abs(r.WorkDir)
	if err != nil {
		return fmt.Errorf("unknown absolute path of working dir %q: %w", r.WorkDir, err)
	}
	var root *meta.Root
	for root == nil || !root.AbsoluteRoot {
		m, err := meta.LoadRootFromDir(wd)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("check %s error: %w", filepath.Join(wd, meta.RootFile), err)
			}
		}
		if err == nil {
			root, r.RootDir = m, wd
		}
		if wd == "/" {
			break
		}
		wd = filepath.Dir(wd)
	}
	if root == nil {
		return fmt.Errorf("find %s from %q failed: %w", meta.RootFile, r.WorkDir, os.ErrNotExist)
	}
	return r.updateMeta(root)
}

// LoadProjects scans the repository to populate all projects.
// It fails if names of projects conflict.
// This must be called after LocateRoot.
func (r *Repo) LoadProjects() error {
	relWorkDir := strings.Trim(r.WorkDir[len(r.RootDir):], string(filepath.Separator)) + string(filepath.Separator)
	var current *Project

	projects := make(map[string]*Project)
	suffix := string(filepath.Separator) + r.metaFolder
	err := walkDirs(r.RootDir, func(relPath string, isDir bool) error {
		if !isDir {
			return nil
		}
		if !strings.HasSuffix(relPath, suffix) {
			return nil
		}
		var dir string
		if left := len(relPath) - len(suffix); left > 0 {
			dir = relPath[1:left]
		}
		// Match gitignore pattern is expensive.
		for _, pattern := range r.root.ProjectPathExclude {
			if gitignore.Match(pattern, relPath) || gitignore.Match(pattern, dir) {
				return filepath.SkipDir
			}
		}
		project, err := loadProject(r, dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load project from %q error: %w", dir, err)
		}
		if err == nil && project != nil {
			if p, ok := projects[project.Name]; ok {
				return fmt.Errorf("conflict project name %q in %q and %q", project.Name, project.Dir, p.Dir)
			}
			projects[project.Name] = project
			prefix := project.Dir + string(filepath.Separator)
			if strings.HasPrefix(relWorkDir, prefix) && (current == nil || len(project.Dir) > len(current.Dir)) {
				current = project
			}
		}
		return filepath.SkipDir
	})
	if err != nil {
		return err
	}
	r.projects, r.currentProject = projects, current
	return nil
}

// FindProject finds the project by name.
func (r *Repo) FindProject(name string) *Project {
	return r.projects[name]
}

// FindTarget find a target by global name.
func (r *Repo) FindTarget(name TargetName) *Target {
	if p := r.FindProject(name.Project); p != nil {
		return p.FindTarget(name.LocalName)
	}
	return nil
}

// Projects returns loaded projects in a copied slice.
func (r *Repo) Projects() []*Project {
	projects := make([]*Project, 0, len(r.projects))
	for _, project := range r.projects {
		projects = append(projects, project)
	}
	return projects
}

// CurrentProject returns the project whose folder is the closest parent folder
// of the working directory. It can be nil if no such folder exists.
func (r *Repo) CurrentProject() *Project {
	return r.currentProject
}

// OutDir returns the base output directory.
func (r *Repo) OutDir() string {
	return filepath.Join(r.dataDir, outFolderName)
}

// LogDir returns the directory for log files.
func (r *Repo) LogDir() string {
	return filepath.Join(r.dataDir, logFolderName)
}

// Plan builds a TaskGraph and prepares it for execution.
func (r *Repo) Plan(requiredTargets ...string) (*TaskGraph, error) {
	g, err := BuildTaskGraph(r, requiredTargets...)
	if err != nil {
		return nil, err
	}
	cyclicTasks := g.Prepare()
	if len(cyclicTasks) > 0 {
		names := make([]string, 0, len(cyclicTasks))
		for task := range cyclicTasks {
			names = append(names, task.Name())
		}
		return nil, fmt.Errorf("cyclic dependencies in %s", strings.Join(names, ","))
	}
	return g, nil
}

// LoadTaskResult loads task result.
func (r *Repo) LoadTaskResult(taskName string) (*TaskResult, error) {
	fn := filepath.Join(r.dataDir, cacheFolderName, taskName+".result")
	return loadTaskResultFrom(fn)
}

// LoadTaskOutputs loads task outputs from saved state.
func (r *Repo) LoadTaskOutputs(taskName string) (*OutputFiles, error) {
	stateFile := filepath.Join(r.dataDir, cacheFolderName, taskName+".state")
	state, err := loadStateFrom(stateFile)
	if err != nil {
		return nil, err
	}
	return &state.TaskOutputs, nil
}

// ResolveTargets resolves a pattern for a list of matched targets.
// The pattern is matched using filepath.Match, with special rules:
// If colon ':' is present, the pattern is separated into a pattern for matching
// project name and the other for matching target name. E.g. "public.*:gen-*".
// For matching project names, the following rules apply:
// - With wildcard, project names are matched using filepath.Match;
// - Empty string, the current project (the closest project folder in the parents
//     of current working directory) is matched. It fails if no current project
//     is available;
// - Without wildcard, the exact project name is matched, or empty result is
//     returned (not an error).
// For matching target names, the above rules apply except empty string will result
// an error of filepath.ErrBadPattern.
// If colon is not present, the pattern is used to match target names. If wildcard
// is present, the target names are matched from all projects. If no wildcard is
// present, exactly one target name is matched. If there are more than one targets
// match the name from multiple projects, ErrAmbiguousMatch is returned together with
// all matched targets.
func (r *Repo) ResolveTargets(pattern string) ([]*Target, error) {
	items := strings.Split(pattern, ":")
	if len(items) > 2 {
		return nil, fmt.Errorf("%w: %q contains more than one colon", filepath.ErrBadPattern, pattern)
	}
	onlyMatchTargets := len(items) == 1

	projects := make([]*Project, 0, len(r.projects))
	var targetPattern string

	if onlyMatchTargets {
		targetPattern = strings.TrimSpace(items[0])
		for _, project := range r.projects {
			projects = append(projects, project)
		}
	} else {
		projectPattern := strings.TrimSpace(items[0])
		targetPattern = strings.TrimSpace(items[1])
		if projectPattern == "" {
			project := r.CurrentProject()
			if project == nil {
				return nil, ErrNoCurrentProject
			}
			projects = append(projects, project)
		} else {
			for name, project := range r.projects {
				matched, err := filepath.Match(projectPattern, name)
				if err != nil {
					return nil, fmt.Errorf("%w: %q for projects", err, projectPattern)
				}
				if matched {
					projects = append(projects, project)
				}
			}
		}
	}

	if targetPattern == "" {
		return nil, fmt.Errorf("%w: empty target pattern", filepath.ErrBadPattern)
	}

	wildcardMatch := false
	var targetList list.List
	for _, project := range projects {
		for name, target := range project.targets {
			matched, err := filepath.Match(targetPattern, name)
			if err != nil {
				return nil, fmt.Errorf("%w: %q for targets", err, targetPattern)
			}
			if !matched {
				continue
			}
			targetList.PushBack(target)
			if target.Name.LocalName != targetPattern {
				wildcardMatch = true
			}
		}
	}

	if targetList.Len() == 0 {
		return nil, nil
	}

	targets := make([]*Target, 0, targetList.Len())
	for elm := targetList.Front(); elm != nil; elm = elm.Next() {
		targets = append(targets, elm.Value.(*Target))
	}

	if onlyMatchTargets && !wildcardMatch && targetList.Len() > 1 {
		return targets, fmt.Errorf(`%w: use "*:%s" for matching multiple targets`, ErrAmbiguousMatch, targetPattern)
	}

	return targets, nil
}

// ResolveTargetNames resolves multiple patterns into a list of target names.
func (r *Repo) ResolveTargetNames(patterns ...string) ([]string, error) {
	targetSet := make(map[*Target]struct{})
	for _, pattern := range patterns {
		targets, err := r.ResolveTargets(pattern)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", pattern, err)
		}
		for _, target := range targets {
			targetSet[target] = struct{}{}
		}
	}
	names := make([]string, 0, len(targetSet))
	for target := range targetSet {
		names = append(names, target.Name.GlobalName())
	}
	return names, nil
}

func (r *Repo) updateMeta(root *meta.Root) error {
	r.root = root
	dataDir := root.DataDir
	if dataDir == "" {
		dataDir = meta.DefaultDataDir
	}
	r.dataDir = filepath.Join(r.RootDir, dataDir)
	if r.metaFolder = root.MetaFolder; r.metaFolder == "" {
		r.metaFolder = meta.DefaultMetaFolder
	}
	return nil
}

func walkDirs(baseDir string, callback func(string, bool) error) error {
	baseDir = filepath.Clean(baseDir)
	return godirwalk.Walk(baseDir, &godirwalk.Options{
		Callback: func(path string, entry *godirwalk.Dirent) error {
			relPath := path[len(baseDir):]
			if relPath == "" {
				relPath = "/"
			}
			return callback(relPath, entry.IsDir())
		},
		Unsorted: true,
	})
}
