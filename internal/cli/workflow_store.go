package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func lookupWorkflow(name, startDir string) (workflowEntry, workflowFile, error) {
	cleaned, err := cleanWorkflowName(name)
	if err != nil {
		return workflowEntry{}, workflowFile{}, err
	}
	entries, err := workflowEntries(startDir)
	if err != nil {
		return workflowEntry{}, workflowFile{}, err
	}
	for _, entry := range entries {
		if entry.Name != cleaned {
			continue
		}
		workflow, err := readWorkflowFile(entry.Path)
		if err != nil {
			return workflowEntry{}, workflowFile{}, err
		}
		return entry, workflow, nil
	}
	return workflowEntry{}, workflowFile{}, fmt.Errorf("workflow %q not found", cleaned)
}

func workflowEntries(startDir string) ([]workflowEntry, error) {
	byName := map[string]workflowEntry{}
	globalDir := ""
	if dir, err := globalWorkflowDir(); err != nil {
		return nil, err
	} else if entries, err := workflowEntriesInDir(dir, "global"); err != nil {
		return nil, err
	} else {
		globalDir = dir
		for _, entry := range entries {
			byName[entry.Name] = entry
		}
	}
	if dir, ok, err := discoverWorkflowDir(startDir); err != nil {
		return nil, err
	} else if ok && !sameFilesystemPath(dir, globalDir) {
		entries, err := workflowEntriesInDir(dir, "project")
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			byName[entry.Name] = entry
		}
	}
	entries := make([]workflowEntry, 0, len(byName))
	for _, entry := range byName {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

func workflowEntriesInDir(dir, scope string) ([]workflowEntry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var entries []workflowEntry
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, item.Name())
		workflow, err := readWorkflowFile(path)
		if err != nil {
			return nil, err
		}
		entries = append(entries, workflowEntry{
			Name:        workflow.Name,
			Description: workflow.Description,
			Scope:       scope,
			Path:        path,
		})
	}
	return entries, nil
}

func readWorkflowFile(path string) (workflowFile, error) {
	// #nosec G304 -- workflow paths are explicit Toolmux configuration.
	data, err := os.ReadFile(path)
	if err != nil {
		return workflowFile{}, err
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		return workflowFile{}, err
	}
	if workflow.Version == 0 {
		workflow.Version = 1
	}
	if workflow.Name == "" {
		workflow.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return workflow, validateWorkflow(workflow)
}

func writeWorkflowFile(path string, workflow workflowFile) error {
	if workflow.Version == 0 {
		workflow.Version = 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := yaml.Marshal(workflow)
	if err != nil {
		return err
	}
	// #nosec G306 -- workflows are non-secret local tool configuration.
	return os.WriteFile(path, data, 0o644)
}

func workflowWritePath(scope mcpProfileScopeOptions, name, startDir string) (string, string, error) {
	if scope.Global && scope.Project {
		return "", "", fmt.Errorf("use only one of --global or --project")
	}
	if scope.Project {
		if startDir == "" {
			var err error
			startDir, err = os.Getwd()
			if err != nil {
				return "", "", err
			}
		}
		return filepath.Join(startDir, workflowProjectRelDir, name+".yaml"), "project", nil
	}
	dir, err := globalWorkflowDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, name+".yaml"), "global", nil
}

func globalWorkflowDir() (string, error) {
	configPath, err := globalToolmuxConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(configPath), "workflows"), nil
}

func discoverWorkflowDir(startDir string) (string, bool, error) {
	if startDir == "" {
		var err error
		startDir, err = os.Getwd()
		if err != nil {
			return "", false, err
		}
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", false, err
	}
	for {
		candidate := filepath.Join(dir, workflowProjectRelDir)
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			return candidate, true, nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false, nil
}
