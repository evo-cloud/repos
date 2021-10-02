package repos

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	errInvalidFileEntryValue = errors.New("invalid value")
)

// FilesCache tracks files for detecting changes.
type FilesCache struct {
	xctx      *ToolExecContext
	stateFile string
	current   fileCacheContent
	saved     *fileCacheContent
}

type fileEntry struct {
	Dir   bool
	MTime time.Time
}

type fileCacheContent struct {
	Inputs      map[string]*fileEntry
	Outputs     map[string]*fileEntry
	Generates   map[string]*fileEntry
	Opaque      []string
	TaskOutputs OutputFiles
}

// NewFilesCache creates FilesCache from ToolExecContext.
func NewFilesCache(xctx *ToolExecContext) *FilesCache {
	return &FilesCache{
		xctx:      xctx,
		stateFile: filepath.Join(xctx.CacheDir, xctx.Task.Name()+".state"),
		current: fileCacheContent{
			Inputs:    make(map[string]*fileEntry),
			Outputs:   make(map[string]*fileEntry),
			Generates: make(map[string]*fileEntry),
			TaskOutputs: OutputFiles{
				Extra: make(map[string]string),
			},
		},
	}
}

// AddInput implements Cache.
func (s *FilesCache) AddInput(relPath string, recursive bool) error {
	if recursive {
		return filepath.Walk(filepath.Join(s.xctx.ProjectDir(), relPath), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			s.addInputEntry(path, &fileEntry{Dir: info.IsDir(), MTime: info.ModTime()})
			return nil
		})
	}
	fn := filepath.Join(s.xctx.ProjectDir(), relPath)
	fi, err := os.Stat(fn)
	if err != nil {
		return err
	}
	s.addInputEntry(fn, &fileEntry{Dir: fi.IsDir(), MTime: fi.ModTime()})
	return nil
}

// AddSource implements Cache.
func (s *FilesCache) AddSource(relPath string, recursive bool) error {
	if srcDir := s.xctx.SourceSubDir(); srcDir != "" {
		return s.AddInput(filepath.Join(srcDir, relPath), recursive)
	}
	return s.AddInput(relPath, recursive)
}

func (s *FilesCache) addInputEntry(fn string, entry *fileEntry) {
	s.current.Inputs[fn] = entry
	s.xctx.Logger.Printf("Input %q %s", fn[len(s.xctx.ProjectDir())+1:], entry.String())
}

// AddOutput implements Cache.
func (s *FilesCache) AddOutput(key, relPath string) {
	dir := strings.HasSuffix(relPath, string(filepath.Separator))
	cleanPath := strings.TrimRight(relPath, string(filepath.Separator))
	fn := filepath.Join(s.xctx.OutDir, cleanPath)
	s.current.Outputs[fn] = &fileEntry{Dir: dir}
	if key == "" {
		s.current.TaskOutputs.Primary = relPath
		s.xctx.Logger.Printf("Output PRIMARY %q", relPath)
	} else {
		s.current.TaskOutputs.Extra[key] = relPath
		s.xctx.Logger.Printf("Output [%s] %q", key, relPath)
	}
}

// AddGenerated implements Cache.
func (s *FilesCache) AddGenerated(relPath string) {
	dir := strings.HasSuffix(relPath, string(filepath.Separator))
	cleanPath := strings.TrimRight(relPath, string(filepath.Separator))
	fn := filepath.Join(s.xctx.SourceDir(), cleanPath)
	s.current.Generates[fn] = &fileEntry{Dir: dir}
	s.current.TaskOutputs.GeneratedFiles = append(s.current.TaskOutputs.GeneratedFiles, relPath)
	s.xctx.Logger.Printf("Generate %q", relPath)
}

// AddOpaque implements Cache.
func (s *FilesCache) AddOpaque(opaque ...string) {
	for _, val := range opaque {
		s.current.Opaque = append(s.current.Opaque, val)
		s.xctx.Logger.Printf("Opaque %s", val)
	}
}

// Load implements Cache.
func (s *FilesCache) Load() error {
	saved, err := loadStateFrom(s.stateFile)
	if err != nil {
		return err
	}
	s.saved = saved
	return nil
}

// Persist implements Cache.
func (s *FilesCache) Persist() error {
	if err := refreshFileEntries(s.current.Outputs); err != nil {
		return fmt.Errorf("output: %w", err)
	}
	if err := refreshFileEntries(s.current.Generates); err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	data, err := json.Marshal(&s.current)
	if err != nil {
		return fmt.Errorf("encoding state error: %w", err)
	}
	if err := os.WriteFile(s.stateFile, data, 0644); err != nil {
		return fmt.Errorf("write state %q error: %w", s.stateFile, err)
	}
	return nil
}

// ClearSaved implements Cache.
func (s *FilesCache) ClearSaved() error {
	return os.Remove(s.stateFile)
}

// Verify implements Cache.
func (s *FilesCache) Verify() bool {
	if s.saved == nil {
		if err := s.Load(); err != nil {
			s.xctx.Logger.Printf("Cache %v", err)
			return false
		}
	}
	if !compareFileEntryKeys(s.saved.Outputs, s.current.Outputs, s.xctx.Logger, "outputs") ||
		!compareFileEntryKeys(s.saved.Generates, s.current.Generates, s.xctx.Logger, "generates") ||
		!compareFileEntryMaps(s.saved.Inputs, s.current.Inputs, s.xctx.Logger, "inputs") {
		return false
	}
	if saved, curr := s.saved.TaskOutputs.Primary, s.current.TaskOutputs.Primary; saved != curr {
		s.xctx.Logger.Printf("Cache primary output %q vs %q", saved, curr)
	}
	if !compareExtraTaskOutputs(s.saved.TaskOutputs.Extra, s.current.TaskOutputs.Extra, s.xctx.Logger) {
		return false
	}
	if len(s.saved.Opaque) != len(s.current.Opaque) {
		s.xctx.Logger.Println("Cache opaque size differs")
		return false
	}
	for n, val := range s.saved.Opaque {
		if newVal := s.current.Opaque[n]; newVal != val {
			s.xctx.Logger.Printf("Cache opaque[%d] %q vs %q (saved)", n, newVal, val)
			return false
		}
	}
	if err := checkUpToDate(s.current.Outputs, s.saved.Outputs); err != nil {
		s.xctx.Logger.Printf("Cache output: %v", err)
		return false
	}
	if err := checkUpToDate(s.current.Generates, s.saved.Generates); err != nil {
		s.xctx.Logger.Printf("Cache generate: %v", err)
		return false
	}
	return true
}

// TaskOutputs implements Cache.
func (s *FilesCache) TaskOutputs() *OutputFiles {
	return &s.current.TaskOutputs
}

// SavedTaskOutputs implements Cache.
func (s *FilesCache) SavedTaskOutputs() *OutputFiles {
	if s.saved != nil {
		return &s.saved.TaskOutputs
	}
	return nil
}

func (f *fileEntry) String() string {
	fileType := "F"
	if f.Dir {
		fileType = "D"
	}
	return fmt.Sprintf(`%s%v`, fileType, f.MTime.UnixNano())
}

func (f *fileEntry) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	fmt.Fprintf(&out, `"%s"`, f.String())
	return out.Bytes(), nil
}

func (f *fileEntry) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	if str == "" {
		return errInvalidFileEntryValue
	}
	fileType := str[0]
	if fileType != 'D' && fileType != 'F' {
		return errInvalidFileEntryValue
	}
	timeVal, err := strconv.ParseInt(str[1:], 10, 64)
	if err != nil {
		return errInvalidFileEntryValue
	}
	f.Dir, f.MTime = fileType == 'D', time.Unix(0, timeVal)
	return nil
}

func compareFileEntryMaps(m1, m2 map[string]*fileEntry, logger *log.Logger, title string) bool {
	if l1, l2 := len(m1), len(m2); l1 != l2 {
		logger.Printf("Cache %s length %d vs %d", title, l1, l2)
		return false
	}
	for fn, entry1 := range m1 {
		entry2 := m2[fn]
		if entry2 == nil {
			logger.Printf("Cache %s[%q] not found", title, fn)
			return false
		}
		if dir1, dir2 := entry1.Dir, entry2.Dir; dir1 != dir2 {
			logger.Printf("Cache %s[%q] IsDir %v vs %v", title, fn, dir1, dir2)
			return false
		}
		if mtime1, mtime2 := entry1.MTime, entry2.MTime; mtime1 != mtime2 {
			logger.Printf("Cache %s[%q] mtime %s vs %s", title, fn, mtime1, mtime2)
			return false
		}
	}
	return true
}

func compareFileEntryKeys(m1, m2 map[string]*fileEntry, logger *log.Logger, title string) bool {
	if l1, l2 := len(m1), len(m2); l1 != l2 {
		logger.Printf("Cache %s length %d vs %d", title, l1, l2)
		return false
	}
	for fn := range m1 {
		if entry2 := m2[fn]; entry2 == nil {
			logger.Printf("Cache %s[%q] not found", title, fn)
			return false
		}
	}
	return true
}

func compareExtraTaskOutputs(m1, m2 map[string]string, logger *log.Logger) bool {
	if l1, l2 := len(m1), len(m2); l1 != l2 {
		logger.Printf("Cache extra outputs length %d vs %d", l1, l2)
		return false
	}
	for key := range m1 {
		if _, ok := m2[key]; !ok {
			logger.Printf("Cache extra outputs[%q] not found", key)
			return false
		}
	}
	return true
}

func refreshFileEntries(entries map[string]*fileEntry) error {
	for fn, entry := range entries {
		info, err := os.Stat(fn)
		if err != nil {
			return fmt.Errorf("stat %q error: %w", fn, err)
		}
		if entry.Dir != info.IsDir() {
			if entry.Dir {
				return fmt.Errorf("%q is not a directory", fn)
			}
			return fmt.Errorf("%q is not a file", fn)
		}
		entry.MTime = info.ModTime()
	}
	return nil
}

func checkUpToDate(current, saved map[string]*fileEntry) error {
	for fn := range current {
		info, err := os.Stat(fn)
		if err != nil {
			return fmt.Errorf("stat %q error: %w", fn, err)
		}
		if entry := saved[fn]; entry == nil || entry.Dir != info.IsDir() || entry.MTime != info.ModTime() {
			return fmt.Errorf("out-of-date: %q", fn)
		}
	}
	return nil
}

func loadStateFrom(stateFile string) (*fileCacheContent, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("load state %q error: %w", stateFile, err)
	}
	var saved fileCacheContent
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("parse state %q error: %w", stateFile, err)
	}
	return &saved, nil
}
