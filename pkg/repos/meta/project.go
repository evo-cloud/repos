package meta

const (
	// ProjectFile is filename under meta-folder.
	ProjectFile = "project.yaml"
)

// Project is the schema of meta-folder/ProjectFile.
type Project struct {
	// Name of the project.
	Name string `json:"name"`
	// Description is the details of the project.
	Description string `json:"description,omitempty"`
	// Targets specifies all the targets in this project.
	Targets map[string]*Target `json:"targets,omitempty"`
}

// Target defines the schema of a single target.
type Target struct {
	// Description is the details of the target.
	Description string `json:"description,omitempty"`
	// Deps specifies the dependencies.
	Deps []string `json:"deps,omitempty"`
	// Launch indicates if this target is for launching a process.
	Launch bool `json:"launch,omitempty"`
	// Always specifies this target can't be skipped.
	Always bool `json:"always,omitempty"`
	// SubDir indicates the tool should operate in the relative path under
	// the project directory.
	SubDir string `json:"subdir,omitempty"`
	// RegisterTool indicates an external tool is registered using the output of this target.
	RegisterTool *ToolRegistration `json:"register-tool,omitempty"`
	// Rule specifies the tool and parameters of the tool to execute this target.
	Rule map[string]interface{} `json:"rule"`
}

// ToolRegistration defines the schema for registering a tool.
type ToolRegistration struct {
	// Name is tool name.
	Name string `json:"name"`
	// Src specify the tool executable relative to source directory.
	// If not present, output is used.
	Src string `json:"src"`
	// Out specify the key of the executable from extra outputs.
	// If not present, the primary output is used.
	Out string `json:"out,omitempty"`
	// ShellScript specifies the tool executable should be launched using shell or directly.
	ShellScript bool `json:"shell-script,omitempty"`
	// Env specifies the additional environment variables.
	Env []string `json:"env"`
	// Args specifies the immediate command line arguments for the executable.
	Args []string `json:"args"`
}
