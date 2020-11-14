// Package cc provides C/C++ tools.
package cc

import (
	"container/list"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"repos/pkg/repos"
)

var (
	makefileTemplate = template.Must(template.New("").Parse(`
VPATH := {{.SourceDir}}
TARGET := {{.Target}}
OBJECTS := \{{range .Objects}}
	{{.}} \
{{- end}}
{{with .CFlags}}
CFLAGS += \{{range .}}
	{{.}} \
{{- end}}{{- end}}
{{with .IncDirs}}
CFLAGS += \{{range .}}
	-I{{.}} \
{{- end}}{{- end}}
{{with .CXXFlags}}
CXXFLAGS += \{{range .}}
	{{.}} \
{{- end}}{{- end}}
{{with .LibDirs}}
LDFLAGS += \{{range .}}
	-L{{.}} \
{{- end}}{{- end}}
{{with .Libs}}
LIBS += \{{range .}}
	{{.}} \
{{- end}}{{- end}}

.SUFFIXES:
.SUFFIXES: .c .cc .cpp .cxx .h .hpp .o

%.o: %.c
	-mkdir -p $(dir $@)
	$(CROSS_COMPILE)$(CC) $(CFLAGS) -MD -c -o $@ $<

%.o: %.cc
	-mkdir -p $(dir $@)
	$(CROSS_COMPILE)$(CXX) $(CFLAGS) $(CXXFLAGS) -MD -c -o $@ $<

%.o: %.cpp
	-mkdir -p $(dir $@)
	$(CROSS_COMPILE)$(CXX) $(CFLAGS) $(CXXFLAGS) -MD -c -o $@ $<

%.o: %.cxx
	-mkdir -p $(dir $@)
	$(CROSS_COMPILE)$(CXX) $(CFLAGS) $(CXXFLAGS) -MD -c -o $@ $<

.PHONY: all
all: $(TARGET)

{{.Target}}: $(OBJECTS) {{.Makefile}}
	-mkdir -p $(dir $@)
	{{.BinRule}}

{{range .HdrDepFiles}}
-include {{.}}
{{- end}}
`))
)

// Params defines tool parameters.
type Params struct {
	Output      string   `json:"output"`
	SourceList  []string `json:"srcs"`
	HeaderList  []string `json:"hdrs"`
	LinkLibs    []string `json:"libs"`
	StaticLink  bool     `json:"static"`
	IncludeDirs []string `json:"include-dirs"`
	CXXStd      string   `json:"std"`
}

// Tool registers cc tool.
type Tool struct {
}

// Executor implements repos.ToolExecutor.
type Executor struct {
	SourceList  []string
	HeaderList  []string
	IncludeDirs []string

	data makefileData
}

type makefileData struct {
	SourceDir   string
	Target      string
	Objects     []string
	HdrDepFiles []string
	BinRule     string
	Makefile    string
	CFlags      []string
	CXXFlags    []string
	IncDirs     []string
	LibDirs     []string
	Libs        []string
}

// CreateToolExecutor implements repos.Tool.
func (t *Tool) CreateToolExecutor(target *repos.Target) (repos.ToolExecutor, error) {
	var params Params
	if err := target.ToolParamsAs(&params); err != nil {
		return nil, fmt.Errorf("decode params error: %w", err)
	}
	if params.Output == "" {
		return nil, fmt.Errorf("missing parameter output")
	}
	if len(params.SourceList) == 0 {
		return nil, fmt.Errorf("missing or empty parameter srcs")
	}
	x := &Executor{
		SourceList:  params.SourceList,
		HeaderList:  params.HeaderList,
		IncludeDirs: params.IncludeDirs,
	}
	if len(x.IncludeDirs) == 0 {
		x.IncludeDirs = []string{"inc"}
	}
	x.data.SourceDir = target.SourceDir()
	x.data.Objects = make([]string, len(x.SourceList))
	x.data.HdrDepFiles = make([]string, 0, len(x.SourceList))
	for n, src := range x.SourceList {
		pos := strings.LastIndex(src, ".")
		if pos <= 0 {
			return nil, fmt.Errorf("invalid srcs[%d]: %q", n, src)
		}
		x.data.Objects[n] = src[:pos] + ".o"
		ext := src[pos:]
		switch ext {
		case ".c", ".cc", ".cpp", ".cxx":
			x.data.HdrDepFiles = append(x.data.HdrDepFiles, src[:pos]+".d")
		}
	}
	if strings.HasPrefix(params.Output, "lib") {
		switch {
		case strings.HasSuffix(params.Output, ".a"):
			x.data.Target = filepath.Join("lib", params.Output)
			x.data.BinRule = `$(CROSS_COMPLE)$(AR) $(ARFLAGS) $@ $(OBJECTS)`
		case strings.HasSuffix(params.Output, ".so"):
			x.data.Target = filepath.Join("lib", params.Output)
			if params.StaticLink {
				return nil, fmt.Errorf("parameter static should be false for shared object")
			}
			x.data.BinRule = `$(CROSS_COMPILE)$(CXX) $(CFLAGS) $(CXXFLAGS) $(LDFLAGS) -shared -o $@ $(OBJECTS) $(LIBS)`
		}
	}
	if x.data.Target == "" {
		x.data.Target = filepath.Join("bin", params.Output)
		var static string
		if params.StaticLink {
			static = "-static "
		}
		x.data.BinRule = `$(CROSS_COMPILE)$(CXX) $(CFLAGS) $(CXXFLAGS) $(LDFLAGS) ` + static + `-o $@ $(OBJECTS) $(LIBS)`
	}
	x.data.CFlags = append(x.data.CFlags, "-g")
	cxxStd := params.CXXStd
	if cxxStd == "" {
		cxxStd = "c++17"
	}
	x.data.CXXFlags = append(x.data.CXXFlags, "-std="+cxxStd)
	x.data.Libs = make([]string, len(params.LinkLibs))
	for n, val := range params.LinkLibs {
		if strings.HasPrefix(val, "-") || strings.HasSuffix(val, ".a") || strings.HasSuffix(val, ".so") {
			x.data.Libs[n] = val
			continue
		}
		x.data.Libs[n] = "-l" + val
	}
	return x, nil
}

// Execute implements repos.ToolExecutor.
func (x *Executor) Execute(ctx context.Context, xctx *repos.ToolExecContext) error {
	cache := repos.NewFilesCache(xctx)
	for _, src := range x.SourceList {
		if err := cache.AddSource(src); err != nil {
			return fmt.Errorf("add source %q to cache failed: %w", src, err)
		}
	}
	for _, hdr := range x.HeaderList {
		if err := cache.AddSource(hdr); err != nil {
			return fmt.Errorf("add header %q to cache failed: %w", hdr, err)
		}
	}
	cache.AddOutput("", x.data.Target)
	if strings.HasPrefix(x.data.Target, "lib/") {
		cache.AddOutputDir("CC_LIB_DIR", "lib")
	}
	cache.AddOpaque(strings.Join(x.data.CFlags, " "))
	cache.AddOpaque(strings.Join(x.data.CXXFlags, " "))
	cache.AddOpaque(strings.Join(x.data.Libs, " "))
	if xctx.Skippable && cache.Verify() {
		xctx.Output(*cache.SavedTaskOutputs())
		return repos.ErrSkipped
	}

	visited := make(map[*repos.Task]struct{})
	var incList, libList list.List
	findCCDeps(xctx.Task, &incList, &libList, visited)
	for _, dir := range x.IncludeDirs {
		incList.PushBack(filepath.Join(xctx.SourceDir(), dir))
	}
	x.data.IncDirs = listToSlice(&incList)
	x.data.LibDirs = listToSlice(&libList)

	x.data.Makefile = xctx.Task.Target.Name.LocalName + ".mak"
	makefile := filepath.Join(xctx.OutDir, x.data.Makefile)
	f, err := os.Create(makefile)
	if err != nil {
		return fmt.Errorf("create %q error: %w", makefile, err)
	}
	defer f.Close()
	if err := makefileTemplate.Execute(f, &x.data); err != nil {
		return fmt.Errorf("write %q error: %w", makefile, err)
	}
	// Close makefile early to flush all data and allow make to access.
	f.Close()

	if err := xctx.RunAndLog(xctx.Command(ctx, "make", "-f", x.data.Makefile, "-C", xctx.OutDir)); err != nil {
		return fmt.Errorf("run make error: %w", err)
	}

	cache.PersistOrLog()
	xctx.Output(*cache.TaskOutputs())
	return nil
}

func findCCDeps(task *repos.Task, incList, libList *list.List, visited map[*repos.Task]struct{}) {
	visited[task] = struct{}{}
	for dep := range task.DepOn {
		if _, ok := visited[dep]; ok {
			continue
		}
		findCCDeps(dep, incList, libList, visited)
		if cc, ok := dep.Executor.(*Executor); ok && cc != nil {
			for _, dir := range cc.IncludeDirs {
				incList.PushBack(filepath.Join(dep.Target.SourceDir(), dir))
			}
		}
		if dep.Outputs == nil {
			continue
		}
		if dir := dep.Outputs.Extra["CC_INC_DIR"]; dir != "" {
			incList.PushBack(filepath.Join(dep.Target.Project.OutDir(), dir))
		}
		if dir := dep.Outputs.Extra["CC_LIB_DIR"]; dir != "" {
			libList.PushBack(filepath.Join(dep.Target.Project.OutDir(), dir))
		}
	}
}

func listToSlice(l *list.List) []string {
	strs := make([]string, 0, l.Len())
	for elm := l.Front(); elm != nil; elm = elm.Next() {
		strs = append(strs, elm.Value.(string))
	}
	return strs
}

func init() {
	repos.RegisterTool("cc", &Tool{})
}
