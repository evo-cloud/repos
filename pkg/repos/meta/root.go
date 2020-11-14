package meta

const (
	// RootFile defines the Root metadata file name.
	RootFile = "REPOS.yaml"

	// DefaultDataDir is the default directory name for data.
	DefaultDataDir = ".repos_data"

	// DefaultMetaFolder is the default value for meta folder.
	DefaultMetaFolder = ".repos"
)

// Root defines the metadata of information at the root of a monolithic repository.
// This is the schema of RootFile.
type Root struct {
	// DataDir specifies the relative path to store outputs, cached data, internal states, etc.
	DataDir string `json:"data-dir,omitempty"`
	// MetaFolder specifies the folder name containing metadata of a workspace/project.
	MetaFolder string `json:"meta-folder,omitempty"`
	// ProjectPathExclude specifies the pattern to skip certain paths when looking for projects.
	ProjectPathExclude []string `json:"project-path-exclude,omitempty"`
}
