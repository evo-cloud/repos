package repos

import (
	"path/filepath"
	"strings"
)

const (
	pathSep = string(filepath.Separator)
)

// Cache defines the abstraction for tracking changes.
type Cache interface {
	// AddInput adds input file/directory used in task.
	// relPath is relative to project dir (not source dir, use AddSource for path relative to source dir).
	// If relPath is suffixed with /, it's a directory, otherwise it is a file.
	// If relPath is a directory, only mtime is tracked, not the entries inside.
	AddInput(relPath string, recursive bool) error

	// AddSource is similar to AddInput but relPath is relative to source dir.
	AddSource(relPath string, recursive bool) error

	// AddOutput adds output file/directory generated by the task.
	// If it's a directory, relPath must be suffixed by "/".
	// If key is empty, it's primary output.
	AddOutput(key, relPath string)

	// AddGenerated adds generated file/directory by the task.
	// If it's a directory, relPath must be suffixed by "/".
	AddGenerated(relPath string)

	// AddOpaque add opaque data.
	AddOpaque(opaque ...string)

	// Load loads previously saved state.
	Load() error

	// Persist saves the state.
	Persist() error

	// ClearSaved removes the saved state (remove the state file).
	ClearSaved() error

	// Verify compares the current state with saved state and check
	// if the saved state is up-to-date.
	// It returns nil to indicate the state is up-to-date.
	// It returns ErrOutDated if state is out-of-date or any other error
	// occurred when accessing/parsing the saved state.
	Verify() bool

	// TaskOutputs returns the output files from the current state.
	TaskOutputs() OutputFiles

	// SavedTaskOutputs returns the output files from saved state.
	SavedTaskOutputs() OutputFiles
}

// CacheReporter wraps a Cache with some helper funcs.
type CacheReporter struct {
	Cache

	records []func(Cache) error
}

func (r *CacheReporter) AddInput(relPath string) error {
	return r.addInput(relPath, false)
}

// AddInputRecursively adds input directory used in task.
// The directory is traversed recursively and all entries are added.
func (r *CacheReporter) AddInputRecursively(relPath string) error {
	return r.addInput(relPath, true)
}

func (r *CacheReporter) addInput(relPath string, recursive bool) error {
	if err := r.Cache.AddInput(relPath, recursive); err != nil {
		return err
	}
	r.records = append(r.records, func(c Cache) error { return c.AddInput(relPath, recursive) })
	return nil
}

func (r *CacheReporter) AddSource(relPath string) error {
	return r.addSource(relPath, false)
}

func (r *CacheReporter) addSource(relPath string, recursive bool) error {
	if err := r.Cache.AddSource(relPath, recursive); err != nil {
		return err
	}
	r.records = append(r.records, func(c Cache) error { return c.AddSource(relPath, recursive) })
	return nil
}

// AddSourceRecursively is similar to AddInputRecursively but relPath is relative to source dir.
func (r *CacheReporter) AddSourceRecursively(relPath string) error {
	return r.addSource(relPath, true)
}

func (r *CacheReporter) AddOutput(key, relPath string) {
	r.Cache.AddOutput(key, relPath)
	r.records = append(r.records, func(c Cache) error {
		c.AddOutput(key, relPath)
		return nil
	})
}

// AddOutputDir explicitly adds an output directory without requiring
// relPath suffixed by "/".
func (r *CacheReporter) AddOutputDir(key, relPath string) {
	r.AddOutput(key, strings.TrimRight(relPath, pathSep)+pathSep)
}

func (r *CacheReporter) AddGenerated(relPath string) {
	r.Cache.AddGenerated(relPath)
	r.records = append(r.records, func(c Cache) error {
		c.AddGenerated(relPath)
		return nil
	})
}

// AddGeneratedDir explicitly adds a generated directory without requiring
// relPath suffixed by "/".
func (r *CacheReporter) AddGeneratedDir(relPath string) {
	r.AddGenerated(strings.TrimRight(relPath, pathSep) + pathSep)
}

// AddOpaque add opaque data.
func (r *CacheReporter) AddOpaque(opaque ...string) {
	r.Cache.AddOpaque(opaque...)
	r.records = append(r.records, func(c Cache) error {
		c.AddOpaque(opaque...)
		return nil
	})
}

// Replay replays the recorded reports to the specified cache.
func (r *CacheReporter) Replay(c Cache) error {
	for _, rec := range r.records {
		if err := rec(c); err != nil {
			return err
		}
	}
	return nil
}
