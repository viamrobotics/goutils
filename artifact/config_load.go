package artifact

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/pkg/errors"

	"go.viam.com/utils"
)

// The artifact file names.
const (
	ConfigName = "config.json"
	TreeName   = "tree.json"
)

// LoadConfig attempts to automatically load an artifact config
// by searching for the default configuration file upwards in
// the file system.
func LoadConfig() (*Config, error) {
	configPath, err := searchConfig()
	if err != nil {
		return nil, err
	}
	return LoadConfigFromFile(configPath)
}

// ErrConfigNotFound is used when the configuration file cannot be found anywhere.
var ErrConfigNotFound = errors.Errorf("%q not found on system", ConfigName)

// searchConfig searches for the default configuration file by
// traversing the filesystem upwards from the current working
// directory.
func searchConfig() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	wdAbs, err := filepath.Abs(wd)
	if err != nil {
		return "", err
	}
	var helper func(path string) (string, error)
	helper = func(path string) (string, error) {
		candidate := filepath.Join(path, DotDir, ConfigName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}

		next := filepath.Join(path, "..")
		if next == path {
			return "", nil
		}
		return helper(next)
	}
	location, err := helper(wdAbs)
	if err != nil {
		return "", err
	}
	if location == "" {
		return "", ErrConfigNotFound
	}
	return location, nil
}

// LoadConfigFromFile loads a Config from the given path. It also
// searches for an adjacent tree file (not required to exist).
func LoadConfigFromFile(path string) (*Config, error) {
	pathDir := filepath.Dir(path)
	//nolint:gosec
	configFile, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer utils.UncheckedErrorFunc(configFile.Close)

	configDec := json.NewDecoder(configFile)

	var config Config
	if err := configDec.Decode(&config); err != nil {
		return nil, err
	}

	treePath := filepath.Join(pathDir, TreeName)
	config.configDir = pathDir
	config.commitFn = func() error {
		// Read current version from disk to detect concurrent modifications
		var currentVersion int64
		var fileExists bool
		//nolint:gosec
		if existingFile, err := os.Open(treePath); err == nil {
			defer utils.UncheckedErrorFunc(existingFile.Close)

			fileExists = true
			var existingTreeFile TreeFile
			if err := json.NewDecoder(existingFile).Decode(&existingTreeFile); err != nil {
				// If we get EOF or decode error, the file might be being written by another process
				// Treat this as a concurrent modification
				if err.Error() == "EOF" || errors.Is(err, os.ErrClosed) {
					return NewConflictError(treePath, config.treeVersion, -1)
				}

				// Try backward compatible read
				if _, seekErr := existingFile.Seek(0, 0); seekErr == nil {
					var tree TreeNodeTree
					if decodeErr := json.NewDecoder(existingFile).Decode(&tree); decodeErr == nil {
						currentVersion = 0 // Old format
					} else {
						return errors.Wrap(err, "failed to read existing tree version")
					}
				} else {
					return errors.Wrap(err, "failed to read existing tree version")
				}
			} else {
				currentVersion = existingTreeFile.Version
			}
		}

		// Check for concurrent modification (optimistic locking)
		// Only check if file existed when we tried to read it
		if fileExists && currentVersion != config.treeVersion {
			return NewConflictError(treePath, config.treeVersion, currentVersion)
		}

		// Increment version for this write
		newVersion := config.treeVersion + 1

		// Write new tree with incremented version
		//nolint:gosec
		newTreeFile, err := os.OpenFile(treePath, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}
		defer utils.UncheckedErrorFunc(newTreeFile.Close)
		if err := newTreeFile.Truncate(0); err != nil {
			return err
		}

		treeFile := TreeFile{
			Version: newVersion,
			Tree:    config.tree,
		}

		enc := json.NewEncoder(newTreeFile)
		enc.SetIndent("", "  ")
		if err := enc.Encode(treeFile); err != nil {
			return err
		}

		// Update in-memory version on successful write
		config.treeVersion = newVersion
		return nil
	}

	//nolint:gosec
	treeFileHandle, err := os.Open(treePath)
	if err == nil {
		defer utils.UncheckedErrorFunc(treeFileHandle.Close)

		// Read file contents for potential backward compatibility
		var fileData []byte
		fileData, err = io.ReadAll(treeFileHandle)
		if err != nil {
			if err.Error() == "EOF" {
				return nil, errors.New("tree file is being modified by another process, please retry")
			}
			return nil, errors.Wrap(err, "failed to read tree file")
		}

		// Try to decode as new format (TreeFile with version)
		var treeFile TreeFile
		if err := json.Unmarshal(fileData, &treeFile); err == nil && treeFile.Tree != nil && len(treeFile.Tree) > 0 {
			// Successfully decoded as new format with content
			config.tree = treeFile.Tree
			config.treeVersion = treeFile.Version
		} else {
			// Try backward compatibility: decode as raw tree without version wrapper
			var tree TreeNodeTree
			if err := json.Unmarshal(fileData, &tree); err != nil {
				return nil, errors.Wrap(err, "failed to decode tree file in old or new format")
			}
			config.tree = tree
			config.treeVersion = 0 // Old format, start at version 0
		}
	} else {
		config.tree = TreeNodeTree{}
		config.treeVersion = 0
	}

	return &config, nil
}
