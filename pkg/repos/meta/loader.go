package meta

import (
	"fmt"
	"path/filepath"

	"github.com/easeway/langx.go/mapper"
)

// LoadRootFromDir loads RootFile from the specified directory.
func LoadRootFromDir(dir string) (*Root, error) {
	return LoadRootFile(filepath.Join(dir, RootFile))
}

// LoadRootFile loads RootFile from the specified file.
func LoadRootFile(fn string) (*Root, error) {
	var root Root
	if err := loadAs(fn, &root); err != nil {
		return nil, err
	}
	return &root, nil
}

// LoadProjectFile loads Project from the specified file.
func LoadProjectFile(fn string) (*Project, error) {
	var project Project
	if err := loadAs(fn, &project); err != nil {
		return nil, err
	}
	return &project, nil
}

func loadAs(fn string, out interface{}) error {
	var ld mapper.Loader
	if err := ld.LoadFile(fn); err != nil {
		return fmt.Errorf("load %s error: %w", fn, err)
	}
	m := mapper.Mapper{FieldTags: []string{"json", "map"}}
	if err := m.Map(out, ld.Map); err != nil {
		return fmt.Errorf("parse %s error: %w", fn, err)
	}
	return nil
}
