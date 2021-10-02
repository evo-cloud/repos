package repos

import (
	"container/list"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/easeway/langx.go/mapper"

	"repos/pkg/repos/meta"
)

// Project represents a single project.
type Project struct {
	Repo *Repo

	// Name of the project.
	Name string
	// Dir is the relative path from root of the repo.
	Dir string

	meta    *meta.Project
	targets map[string]*Target
}

// Target represents a target in a project.
type Target struct {
	Project *Project
	Name    TargetName

	toolName    string
	toolParams  interface{}
	builtinTool ToolExecutor
	toolReg     *toolRegInfo
	meta        *meta.Target
}

// TargetName is the extracted name from a global target name.
type TargetName struct {
	Project   string
	LocalName string
}

type toolRegInfo struct {
	meta         *meta.ToolRegistration
	envTemplates []*ToolParamTemplate
	argTemplates []*ToolParamTemplate
}

// SplitTargetName splits a global/local target name into TargetName.
func SplitTargetName(name string) TargetName {
	items := strings.SplitN(name, ":", 2)
	if len(items) < 2 {
		return TargetName{LocalName: items[0]}
	}
	return TargetName{Project: items[0], LocalName: items[1]}
}

// GlobalName returns the global name of the target.
// If Project is empty, the returned global name is invalid.
func (n TargetName) GlobalName() string {
	return n.Project + ":" + n.LocalName
}

func mergeMetaTargets(targets, from map[string]*meta.Target) {
	for name, target := range from {
		targets[name] = target
	}
}

func loadProject(r *Repo, relPath string) (*Project, error) {
	fn := filepath.Join(r.RootDir, relPath, r.metaFolder, meta.ProjectFile)
	project, err := meta.LoadProjectFile(fn)
	if err != nil {
		return nil, err
	}
	p := &Project{
		Repo:    r,
		Dir:     relPath,
		Name:    project.Name,
		meta:    project,
		targets: make(map[string]*Target),
	}
	if p.Name == "" {
		return nil, fmt.Errorf("missing project name: %q", fn)
	}

	targets := make(map[string]*meta.Target)

	// Processing includes.
	var incProjects list.List
	incProjectFiles := make(map[string]*meta.Project)
	incProjects.PushBack(project)
	incProjectFiles[meta.ProjectFile] = project
	for incProjects.Len() > 0 {
		elem := incProjects.Front()
		project := elem.Value.(*meta.Project)
		incProjects.Remove(elem)
		mergeMetaTargets(targets, project.Targets)
		for _, includeFile := range p.meta.Includes {
			if incProjectFiles[includeFile] != nil {
				continue
			}
			project, err = meta.LoadProjectFile(filepath.Join(r.RootDir, relPath, r.metaFolder, includeFile))
			if err != nil {
				return nil, err
			}
			incProjects.PushBack(project)
			incProjectFiles[includeFile] = project
		}
	}

	for name, targetMeta := range targets {
		target := &Target{
			Project: p,
			Name:    TargetName{Project: p.Name, LocalName: name},
			meta:    targetMeta,
		}
		if err := CreateToolExecutor(target); err != nil {
			return nil, fmt.Errorf("create tool for target %q error: %w", target.Name.GlobalName(), err)
		}
		if regInfo := targetMeta.RegisterTool; regInfo != nil {
			if regInfo.Name == "" {
				return nil, fmt.Errorf("target %q: register-tool.name is empty", target.Name.GlobalName())
			}
			if _, ok := registeredTools[regInfo.Name]; ok {
				return nil, fmt.Errorf("target %q: register-tool %q used a reserved name", target.Name.GlobalName(), regInfo.Name)
			}
			if regInfo.Out != "" && regInfo.Src != "" {
				return nil, fmt.Errorf("target %q: out and src can't be used at same time in register-tool", target.Name.GlobalName())
			}
			reg := &toolRegInfo{
				meta:         regInfo,
				envTemplates: make([]*ToolParamTemplate, len(regInfo.Env)),
				argTemplates: make([]*ToolParamTemplate, len(regInfo.Args)),
			}
			for n, env := range regInfo.Env {
				tpl, err := NewToolParamTemplate(env)
				if err != nil {
					return nil, fmt.Errorf("target %q: invalid register-tool.env[%d]: %w", target.Name.GlobalName(), n, err)
				}
				reg.envTemplates[n] = tpl
			}
			for n, arg := range regInfo.Args {
				tpl, err := NewToolParamTemplate(arg)
				if err != nil {
					return nil, fmt.Errorf("target %q: invalid register-tool.args[%d]: %w", target.Name.GlobalName(), n, err)
				}
				reg.argTemplates[n] = tpl
			}
			target.toolReg = reg
		}
		p.targets[name] = target

	}
	return p, nil
}

// FileName returns the project file name with relative path.
func (p *Project) FileName() string {
	return filepath.Join(p.Dir, meta.ProjectFile)
}

// Meta returns the metadata of the project.
func (p *Project) Meta() meta.Project {
	return *p.meta
}

// OutDir returns the output directory of this project.
func (p *Project) OutDir() string {
	return filepath.Join(p.Repo.OutDir(), p.Dir)
}

// FindTarget finds the target by local name.
func (p *Project) FindTarget(localName string) *Target {
	return p.targets[localName]
}

// Targets returns the targets defined by the project.
func (p *Project) Targets() []*Target {
	targets := make([]*Target, 0, len(p.targets))
	for _, target := range p.targets {
		targets = append(targets, target)
	}
	return targets
}

// Meta returns the metadata of the target.
func (t *Target) Meta() meta.Target {
	return *t.meta
}

// ProjectDir returns full path to project directory.
func (t *Target) ProjectDir() string {
	return filepath.Join(t.Project.Repo.RootDir, t.Project.Dir)
}

// SourceDir returns full path to source directory (project-dir/subdir)
func (t *Target) SourceDir() string {
	if t.meta.SubDir != "" {
		return filepath.Join(t.ProjectDir(), t.meta.SubDir)
	}
	return t.ProjectDir()
}

// ToolName returns the name of the tool.
func (t *Target) ToolName() string {
	return t.toolName
}

// ToolParams returns the parameters for the tool.
func (t *Target) ToolParams() interface{} {
	return t.toolParams
}

// ToolParamsAs converts tool parameters as specified type.
func (t *Target) ToolParamsAs(out interface{}) error {
	m := mapper.Mapper{FieldTags: []string{"json", "map"}}
	return m.Map(out, t.toolParams)
}

// Tool returns pre-created built-in tool.
// If a tool is created, true is returned with the tool.
// dummy tool (without rule) is returned as nil with true.
func (t *Target) Tool() (ToolExecutor, bool) {
	if t.builtinTool != nil {
		return t.builtinTool, true
	}
	return nil, t.toolName == ""
}
