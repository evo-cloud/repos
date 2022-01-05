package golang

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"repos/pkg/repos"
)

// Params defines the parameters for the tool.
type Params struct {
	// BuildMode specifies build mode.
	BuildMode string `json:"buildmode,omitempty"`
	// CGo specifies whether CGo should be enabled (disabled by default).
	CGo bool `json:"cgo,omitempty"`
	// GoOS specifies GOOS environment variable if present.
	GoOS string `json:"goos,omitempty"`
	// GoArch specifies GOARCH environment variable if present.
	GoArch string `json:"goarch,omitempty"`
	// Packages specifies the packages to build.
	Packages []string `json:"packages,omitempty"`
	// Env specifies extra environment variables.
	Env []string `json:"env,omitempty"`
	// GoArgs specifies extra arguments to the go command.
	GoArgs []string `json:"args,omitempty"`
	// Output specifies output filename.
	Output string `json:"output,omitempty"`
}

// Tool defines a Go Tool.
type Tool struct {
}

// Executor defines a Go ToolExecutor.
type Executor struct {
	ExtraEnv     []string
	BuildOptions []string
	Packages     []string
	ExtraArgs    []*repos.ToolParamTemplate
	Output       string
	CLib         bool

	stateOpaque []string
}

type listPackage struct {
	Dir               string   // directory containing package sources
	GoFiles           []string // .go source files (excluding CgoFiles, TestGoFiles, XTestGoFiles)
	CgoFiles          []string // .go source files that import "C"
	CompiledGoFiles   []string // .go files presented to compiler (when using -compiled)
	IgnoredGoFiles    []string // .go source files ignored due to build constraints
	IgnoredOtherFiles []string // non-.go source files ignored due to build constraints
	CFiles            []string // .c source files
	CXXFiles          []string // .cc, .cxx and .cpp source files
	MFiles            []string // .m source files
	HFiles            []string // .h, .hh, .hpp and .hxx source files
	FFiles            []string // .f, .F, .for and .f90 Fortran source files
	SFiles            []string // .s source files
	SwigFiles         []string // .swig files
	SwigCXXFiles      []string // .swigcxx files
	SysoFiles         []string // .syso object files to add to archive
	TestGoFiles       []string // _test.go files in package
	XTestGoFiles      []string // _test.go files outside package
	EmbedFiles        []string // files matched by EmbedPatterns
	TestEmbedFiles    []string // files matched by TestEmbedPatterns
	XTestEmbedFiles   []string // files matched by XTestEmbedPatterns
}

// CreateToolExecutor implements repos.Tool.
func (t *Tool) CreateToolExecutor(target *repos.Target) (repos.ToolExecutor, error) {
	var params Params
	if err := target.ToolParamsAs(&params); err != nil {
		return nil, fmt.Errorf("decode params error: %w", err)
	}
	x := &Executor{Packages: params.Packages}
	switch params.BuildMode {
	case "c-archive", "c-shared", "shared", "plugin":
		x.Output = filepath.Join("lib", params.Output)
		x.ExtraEnv = append(x.ExtraEnv, "CGO_ENABLED=1")
		x.CLib = true
	case "", "exe", "pie":
		x.Output = filepath.Join("bin", params.Output)
		if params.CGo {
			x.ExtraEnv = append(x.ExtraEnv, "CGO_ENABLED=1")
		} else {
			x.ExtraEnv = append(x.ExtraEnv, "CGO_ENABLED=0")
		}
	default:
		return nil, fmt.Errorf("unsupported buildmode %q", params.BuildMode)
	}
	if params.BuildMode != "" {
		x.BuildOptions = append(x.BuildOptions, "-buildmode", params.BuildMode)
	}
	if params.GoOS != "" {
		x.ExtraEnv = append(x.ExtraEnv, "GOOS="+params.GoOS)
	}
	if params.GoArch != "" {
		x.ExtraEnv = append(x.ExtraEnv, "GOARCH="+params.GoArch)
	}
	if len(params.Packages) == 0 {
		return nil, fmt.Errorf("at least one package should be specified in param packages")
	}
	x.ExtraEnv = append(x.ExtraEnv, params.Env...)
	for n, arg := range params.GoArgs {
		tpl, err := repos.NewToolParamTemplate(arg)
		if err != nil {
			return nil, fmt.Errorf("invalid parameter args[%d]: %w", n, err)
		}
		x.ExtraArgs = append(x.ExtraArgs, tpl)
	}
	if x.Output == "" {
		x.Output = target.Name.LocalName
	}
	x.stateOpaque = append([]string{strings.Join(x.BuildOptions, " ")}, x.ExtraEnv...)
	return x, nil
}

// Execute implements ToolExecutor.
func (x *Executor) Execute(ctx context.Context, xctx *repos.ToolExecContext) error {
	extraArgs, err := xctx.RenderTemplates(x.ExtraArgs)
	if err != nil {
		return fmt.Errorf("args: %w", err)
	}
	cache := repos.NewFilesCache(xctx)
	if x.validateCache(ctx, xctx, cache, extraArgs) {
		xctx.Output(cache.SavedTaskOutputs())
		return repos.ErrSkipped
	}
	cache.ClearSaved()
	os.MkdirAll(filepath.Join(xctx.OutDir, filepath.Dir(x.Output)), 0755)
	args := append([]string{"build", "-v", "-o", filepath.Join(xctx.OutDir, x.Output)}, extraArgs...)
	if err := xctx.RunAndLog(x.goCmd(ctx, xctx, args...)); err != nil {
		return err
	}
	xctx.PersistCacheOrLog(cache)
	xctx.Output(cache.TaskOutputs())
	return nil
}

func (x *Executor) validateCache(ctx context.Context, xctx *repos.ToolExecContext, cache *repos.FilesCache, extraArgs []string) bool {
	cmd := x.goCmd(ctx, xctx, "list", "-json", "-deps")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.MultiWriter(&out, xctx.LogWriter), xctx.LogWriter
	if err := xctx.RunAndLog(cmd); err != nil {
		return false
	}

	prefix := strings.TrimRight(filepath.Clean(xctx.SourceDir()), string(filepath.Separator)) + string(filepath.Separator)
	decoder := json.NewDecoder(&out)
	for {
		var pkg listPackage
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			xctx.Logger.Printf("parse output of go list error: %v", err)
			return false
		}
		xctx.Logger.Printf("Package dir=%q prefix=%q", pkg.Dir, prefix)
		if !strings.HasPrefix(pkg.Dir, prefix) {
			continue
		}
		err = reportInputFiles(cache, pkg.Dir[len(prefix):],
			pkg.GoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles, pkg.EmbedFiles)
		if err != nil {
			xctx.Logger.Print(err)
			return false
		}
	}
	cache.AddOutput("", x.Output)
	if x.CLib {
		cache.AddOutput("CC_INC_DIR", "lib/")
		cache.AddOutput("CC_LIB_DIR", "lib/")
	}
	cache.AddOpaque(x.stateOpaque...)
	cache.AddOpaque(extraArgs...)
	return xctx.Skippable && cache.Verify()
}

func (x *Executor) goCmd(ctx context.Context, xctx *repos.ToolExecContext, args ...string) *exec.Cmd {
	cmd := xctx.Command(ctx, "go", args...)
	if args[0] == "build" {
		cmd.Args = append(cmd.Args, x.BuildOptions...)
	}
	cmd.Args = append(cmd.Args, x.Packages...)
	cmd.Env = append(cmd.Env, x.ExtraEnv...)
	return cmd
}

func reportInputFiles(cache *repos.FilesCache, subDir string, fileGroups ...[]string) error {
	for _, group := range fileGroups {
		for _, name := range group {
			if err := cache.AddInput(filepath.Join(subDir, name), false); err != nil {
				return fmt.Errorf("add input %q to state failed: %v", name, err)
			}
		}
	}
	return nil
}

func init() {
	repos.RegisterTool("go", &Tool{})
}
