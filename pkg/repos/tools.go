package repos

import (
	"bytes"
	"container/list"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

var (
	registeredTools = make(map[string]Tool)
)

// ToolExecContext is the context for executing a tool.
type ToolExecContext struct {
	Task      *Task
	Worker    int
	CacheDir  string
	OutDir    string
	LogWriter io.Writer
	Skippable bool
	ExtraEnv  []string
	Stdout    io.Writer
	Stderr    io.Writer
	Logger    *log.Logger
}

// ToolParamTemplate wraps text/template.Template with specific funcs.
type ToolParamTemplate struct {
	ExecCtx  *ToolExecContext
	Template *template.Template
}

// ToolExecutor is the abstraction of an executable instance of a tool.
type ToolExecutor interface {
	Execute(ctx context.Context, execCtx *ToolExecContext) error
}

// Tool is the abstraction of a tool.
type Tool interface {
	CreateToolExecutor(target *Target) (ToolExecutor, error)
}

// Project returns the related project.
func (c ToolExecContext) Project() *Project {
	return c.Task.Target.Project
}

// Target returns the related target.
func (c ToolExecContext) Target() *Target {
	return c.Task.Target
}

// Repo returns the related repo.
func (c ToolExecContext) Repo() *Repo {
	return c.Task.Graph.Repo
}

// ProjectDir returns the absolute path of project directory.
func (c ToolExecContext) ProjectDir() string {
	return c.Target().ProjectDir()
}

// SourceSubDir returns the source path relative to project directory.
func (c ToolExecContext) SourceSubDir() string {
	return c.Target().Meta().SubDir
}

// SourceDir returns the absolute path of source directory.
func (c ToolExecContext) SourceDir() string {
	return c.Target().SourceDir()
}

// MetaFolder returns the name of project metadata folder.
func (c ToolExecContext) MetaFolder() string {
	return c.Repo().metaFolder
}

// MetaDir returns the absolute path of project metadata folder.
func (c ToolExecContext) MetaDir() string {
	return filepath.Join(c.ProjectDir(), c.MetaFolder())
}

// Output specifies output files of the task.
func (c ToolExecContext) Output(outputs OutputFiles) {
	c.Task.Outputs = &outputs
}

// PersistCacheOrLog persists cache or logs on error.
func (c ToolExecContext) PersistCacheOrLog(cache Cache) {
	if err := cache.Persist(); err != nil {
		c.Logger.Printf("Persist state error: %v", err)
	}
}

// ReplayAndPersistCacheOrLog replays cache or logs on error.
func (c ToolExecContext) ReplayAndPersistCacheOrLog(reporter *CacheReporter, cache Cache) Cache {
	if err := reporter.Replay(cache); err != nil {
		c.Logger.Printf("Refresh cache error: %v", err)
		return cache
	}
	c.PersistCacheOrLog(cache)
	return cache
}

// RenderTemplates renders string list from templates.
func (c ToolExecContext) RenderTemplates(templates []*ToolParamTemplate) ([]string, error) {
	vals := make([]string, 0, len(templates))
	for n, tpl := range templates {
		val, err := tpl.ExecWith(&c, nil)
		if err != nil {
			return nil, fmt.Errorf("rendering [%d] error: %w", n, err)
		}
		vals = append(vals, val)
	}
	return vals, nil
}

// RenderEnvs renders environment variables from templates.
func (c ToolExecContext) RenderEnvs(templates []*ToolParamTemplate) ([]string, error) {
	vals, err := c.RenderTemplates(templates)
	if err != nil {
		return nil, err
	}
	sort.Strings(vals)
	return vals, nil
}

// Command creates an exec.Cmd.
func (c ToolExecContext) Command(ctx context.Context, program string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Env = append(os.Environ(), c.ExtraEnv...)
	cmd.Stdout = c.Stdout
	cmd.Stderr = c.Stderr
	cmd.Dir = c.SourceDir()
	return cmd
}

// ShellCommand creates an exec.Cmd to invoke a shell commandline.
func (c ToolExecContext) ShellCommand(ctx context.Context, commandLine string) *exec.Cmd {
	return c.Command(ctx, shellProgram(), "-c", commandLine)
}

// ShellScript creates an exec.Cmd to invoke a shell script.
func (c ToolExecContext) ShellScript(ctx context.Context, script string, args ...string) *exec.Cmd {
	cmd := c.Command(ctx, shellProgram(), script)
	cmd.Args = append(cmd.Args, args...)
	return cmd
}

// ExtendEnv extends environment variables in existing command env.
func (c ToolExecContext) ExtendEnv(cmd *exec.Cmd, envs ...string) {
	keys := make(map[string]int)
	for n, env := range cmd.Env {
		items := strings.SplitN(env, "=", 2)
		if len(items) == 2 {
			keys[items[0]] = n
		}
	}
	for _, env := range envs {
		// Value like "ENV_VAR" with "=VALUE" is allowed to represent inheritance of
		// current environment variable explicitly.
		// As all environment variables are inherited already, only handle those containing
		// "=" for assigning new values.
		pos := strings.Index(env, "=")
		if pos <= 0 {
			continue
		}
		key := env[:pos]
		if index, ok := keys[key]; ok {
			cmd.Env[index] = env
			continue
		}
		keys[key] = len(cmd.Env)
		cmd.Env = append(cmd.Env, env)
	}
}

// AddBinToPathFromDeps adds bin output folder to path from direct and indirect dependencies.
func (c ToolExecContext) AddBinToPathFromDeps(cmd *exec.Cmd) {
	var binList list.List
	visited := make(map[*Task]struct{})
	findBinDir(c.Task, &binList, visited)
	var pathPrefix string
	for elm := binList.Back(); elm != nil; elm = elm.Prev() {
		pathPrefix += elm.Value.(string) + ":"
	}
	for n, val := range cmd.Env {
		if strings.HasPrefix(val, "PATH=") {
			cmd.Env[n] = "PATH=" + pathPrefix + val[5:]
			return
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+pathPrefix[:len(pathPrefix)-1])
}

// RunAndLog logs command execution and result (no output).
func (c ToolExecContext) RunAndLog(cmd *exec.Cmd) error {
	c.Logger.Printf("CMD START %v", cmd.Args)
	err := cmd.Run()
	if err != nil {
		c.Logger.Printf("CMD FAILED %v: %v", cmd.Args, err)
		return err
	}
	c.Logger.Printf("CMD DONE %v", cmd.Args)
	return nil
}

// NewToolParamTemplate creates a template by parsing content.
func NewToolParamTemplate(content string) (*ToolParamTemplate, error) {
	t := &ToolParamTemplate{}
	tpl, err := template.New("").Funcs(t.TemplateFuncs()).Parse(content)
	if err != nil {
		return nil, err
	}
	t.Template = tpl
	return t, nil
}

// TemplateFuncs returns FuncMap to inject funcs into template.
func (t *ToolParamTemplate) TemplateFuncs() template.FuncMap {
	return template.FuncMap(map[string]interface{}{
		"env":    t.fnEnv,
		"depout": t.fnDepOut,
		"depsrc": t.fnDepSrc,
		"sh":     t.fnShell,
	})
}

// Exec executes template to render a string.
func (t *ToolParamTemplate) Exec(data interface{}) (string, error) {
	var out bytes.Buffer
	if err := t.Template.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

// ExecWith executes template to render a string with provided context.
func (t *ToolParamTemplate) ExecWith(xctx *ToolExecContext, data interface{}) (string, error) {
	t.ExecCtx = xctx
	return t.Exec(data)
}

func (t *ToolParamTemplate) findDep(depName string) (*Task, error) {
	tn := SplitTargetName(depName)
	if tn.Project == "" {
		tn.Project = t.ExecCtx.Project().Name
	}
	task := t.ExecCtx.Task.Graph.Tasks[tn.GlobalName()]
	if _, ok := t.ExecCtx.Task.DepDone[task]; !ok {
		return nil, fmt.Errorf("invalid dependency %q", depName)
	}
	return task, nil
}

func (t *ToolParamTemplate) fnEnv(name string) string {
	return os.Getenv(name)
}

func (t *ToolParamTemplate) fnDepOut(depName, outKey string) (string, error) {
	task, err := t.findDep(depName)
	if err != nil {
		return "", err
	}
	if task.Outputs == nil {
		return "", fmt.Errorf("no outputs from %q", depName)
	}
	var val string
	if outKey == "" {
		if val = task.Outputs.Primary; val == "" {
			return "", fmt.Errorf("no primary output from %q", depName)
		}
	} else {
		if val = task.Outputs.Extra[outKey]; val == "" {
			return "", fmt.Errorf("no extra output %q from %q", depName, outKey)
		}
	}
	return filepath.Join(task.Graph.Repo.OutDir(), task.Target.Project.Dir, val), nil
}

func (t *ToolParamTemplate) fnDepSrc(depName string) (string, error) {
	task, err := t.findDep(depName)
	if err != nil {
		return "", err
	}
	return filepath.Join(task.Graph.Repo.RootDir, task.Target.Project.Dir), nil
}

func (t *ToolParamTemplate) fnShell(commandline string) (string, error) {
	cmd := t.ExecCtx.ShellCommand(context.Background(), commandline)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, errOut.String())
	}
	return out.String(), nil
}

// CreateToolExecutor creates the ToolExecutor according to the tool.
func CreateToolExecutor(t *Target) error {
	if len(t.meta.Rule) > 1 {
		return ErrTooManyTools
	}
	for t.toolName, t.toolParams = range t.meta.Rule {
		break
	}

	if t.toolName == "" {
		// Target without tool is a dummy target.
		return nil
	}
	factory, ok := registeredTools[t.toolName]
	if !ok {
		return nil
	}
	tool, err := factory.CreateToolExecutor(t)
	if err != nil {
		return err
	}
	t.builtinTool = tool
	return nil
}

// RegisterTool registers a built-in tool.
func RegisterTool(name string, tool Tool) {
	registeredTools[name] = tool
}

func findBinDir(task *Task, binList *list.List, visited map[*Task]struct{}) {
	visited[task] = struct{}{}
	for dep := range task.DepOn {
		if _, ok := visited[dep]; ok {
			continue
		}
		findBinDir(dep, binList, visited)
		if dep.Outputs == nil {
			continue
		}
		addBinDir(dep, binList, "bin")
		if installDir := dep.Outputs.Extra["INSTALL_DIR"]; installDir != "" {
			addBinDir(dep, binList, filepath.Join(installDir, "bin"))
		}
	}
}

func addBinDir(dep *Task, binList *list.List, prefix string) {
	if dir := extractPathPrefix(dep.Outputs.Primary, prefix); dir != "" {
		binList.PushBack(filepath.Join(dep.Target.Project.OutDir(), dir))
	}
	for _, val := range dep.Outputs.Extra {
		if dir := extractPathPrefix(val, prefix); dir != "" {
			binList.PushBack(filepath.Join(dep.Target.Project.OutDir(), dir))
		}
	}
}

func extractPathPrefix(path, prefix string) string {
	if path == prefix {
		return prefix
	}
	if strings.HasPrefix(path, prefix+string(filepath.Separator)) {
		return prefix
	}
	return ""
}

func shellProgram() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}
