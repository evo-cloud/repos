package golang

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/build"
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
	// Packages specifies the packages to build.
	Packages []string `json:"packages,omitempty"`
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
	Output       string
	CLib         bool

	stateOpaque []string
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
	if len(params.Packages) == 0 {
		return nil, fmt.Errorf("at least one package should be specified in param packages")
	}
	if x.Output == "" {
		x.Output = target.Name.LocalName
	}
	x.stateOpaque = append([]string{strings.Join(x.BuildOptions, " ")}, x.ExtraEnv...)
	return x, nil
}

// Execute implements ToolExecutor.
func (x *Executor) Execute(ctx context.Context, xctx *repos.ToolExecContext) error {
	cache := repos.NewFilesCache(xctx)
	if x.validateCache(ctx, xctx, cache) {
		xctx.Output(*cache.SavedTaskOutputs())
		return repos.ErrSkipped
	}
	cache.ClearSaved()
	os.MkdirAll(filepath.Join(xctx.OutDir, filepath.Dir(x.Output)), 0755)
	if err := xctx.RunAndLog(x.goCmd(ctx, xctx, "build", "-v", "-o", filepath.Join(xctx.OutDir, x.Output))); err != nil {
		return err
	}
	cache.PersistOrLog()
	xctx.Output(*cache.TaskOutputs())
	return nil
}

func (x *Executor) validateCache(ctx context.Context, xctx *repos.ToolExecContext, cache *repos.FilesCache) bool {
	cmd := x.goCmd(ctx, xctx, "list", "-json", "-deps")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.MultiWriter(&out, xctx.LogWriter), xctx.LogWriter
	if err := xctx.RunAndLog(cmd); err != nil {
		return false
	}

	prefix := xctx.ProjectDir() + string(filepath.Separator)
	decoder := json.NewDecoder(&out)
	for {
		var pkg build.Package
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			xctx.Logger.Printf("parse output of go list error: %v", err)
			return false
		}
		if !strings.HasPrefix(pkg.Dir, prefix) {
			continue
		}
		err = reportInputFiles(cache, pkg.Dir[len(prefix):],
			pkg.GoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles)
		if err != nil {
			xctx.Logger.Print(err)
			return false
		}
	}
	cache.AddOutput("", x.Output)
	if x.CLib {
		cache.AddOutputDir("CC_INC_DIR", "lib")
		cache.AddOutputDir("CC_LIB_DIR", "lib")
	}
	cache.AddOpaque(x.stateOpaque...)
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
			if err := cache.AddInput(filepath.Join(subDir, name)); err != nil {
				return fmt.Errorf("add input %q to state failed: %v", name, err)
			}
		}
	}
	return nil
}

func init() {
	repos.RegisterTool("go", &Tool{})
}
