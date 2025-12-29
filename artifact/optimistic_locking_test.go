package artifact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"go.viam.com/test"
)

// TestOptimisticLocking_VersionIncrement tests that version numbers increment correctly.
func TestOptimisticLocking_VersionIncrement(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, ConfigName)
	test.That(t, os.WriteFile(confPath, []byte(confRaw), 0o644), test.ShouldBeNil)

	// Load config (no tree exists yet, version should be 0)
	config, err := LoadConfigFromFile(confPath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, config.treeVersion, test.ShouldEqual, 0)

	// First commit should create version 1
	config.StoreHash("hash1", 5, []string{"file1"})
	test.That(t, config.commitFn(), test.ShouldBeNil)
	test.That(t, config.treeVersion, test.ShouldEqual, 1)

	// Reload and verify version is persisted
	config, err = LoadConfigFromFile(confPath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, config.treeVersion, test.ShouldEqual, 1)

	// Second commit should increment to version 2
	config.StoreHash("hash2", 6, []string{"file2"})
	test.That(t, config.commitFn(), test.ShouldBeNil)
	test.That(t, config.treeVersion, test.ShouldEqual, 2)

	// Reload and verify
	config, err = LoadConfigFromFile(confPath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, config.treeVersion, test.ShouldEqual, 2)
}

// TestOptimisticLocking_ConflictDetection tests that concurrent modifications are detected.
func TestOptimisticLocking_ConflictDetection(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, ConfigName)
	test.That(t, os.WriteFile(confPath, []byte(confRaw), 0o644), test.ShouldBeNil)

	// Load config and commit initial state
	config1, err := LoadConfigFromFile(confPath)
	test.That(t, err, test.ShouldBeNil)
	config1.StoreHash("hash1", 5, []string{"file1"})
	test.That(t, config1.commitFn(), test.ShouldBeNil)

	// Load two instances of the same config (simulating two processes)
	configA, err := LoadConfigFromFile(confPath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, configA.treeVersion, test.ShouldEqual, 1)

	configB, err := LoadConfigFromFile(confPath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, configB.treeVersion, test.ShouldEqual, 1)

	// ConfigA makes a change and commits successfully
	configA.StoreHash("hashA", 10, []string{"fileA"})
	test.That(t, configA.commitFn(), test.ShouldBeNil)
	test.That(t, configA.treeVersion, test.ShouldEqual, 2)

	// ConfigB tries to commit - should fail with conflict error
	configB.StoreHash("hashB", 20, []string{"fileB"})
	err = configB.commitFn()
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, IsConflictError(err), test.ShouldBeTrue)
	test.That(t, err.Error(), test.ShouldContainSubstring, "conflict detected")
	test.That(t, err.Error(), test.ShouldContainSubstring, "expected version 1")
	test.That(t, err.Error(), test.ShouldContainSubstring, "found version 2")

	// ConfigB's in-memory version should not have changed
	test.That(t, configB.treeVersion, test.ShouldEqual, 1)

	// Verify only configA's changes are persisted
	finalConfig, err := LoadConfigFromFile(confPath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, finalConfig.treeVersion, test.ShouldEqual, 2)

	// Should have file1 (initial) and fileA (from configA)
	node, err := finalConfig.Lookup("fileA")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, node.external.Hash, test.ShouldEqual, "hashA")

	// Should not have fileB (from configB that failed)
	_, err = finalConfig.Lookup("fileB")
	test.That(t, IsNotFoundError(err), test.ShouldBeTrue)
}

// TestOptimisticLocking_BackwardCompatibility tests loading old tree files without version.
func TestOptimisticLocking_BackwardCompatibility(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, ConfigName)
	treePath := filepath.Join(dir, TreeName)

	test.That(t, os.WriteFile(confPath, []byte(confRaw), 0o644), test.ShouldBeNil)
	// Write old-format tree without version field
	test.That(t, os.WriteFile(treePath, []byte(treeRaw), 0o644), test.ShouldBeNil)

	// Load should succeed and treat as version 0
	config, err := LoadConfigFromFile(confPath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, config.treeVersion, test.ShouldEqual, 0)

	// Verify tree was loaded correctly
	// The old tree structure has "one" as a parent with "two" and "three" as children
	node, err := config.Lookup("one")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, node.IsInternal(), test.ShouldBeTrue)

	node, err = config.Lookup("one/two")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, node.external.Hash, test.ShouldEqual, "hash1")

	// Commit should add version and increment to 1
	config.StoreHash("newHash", 100, []string{"newFile"})
	test.That(t, config.commitFn(), test.ShouldBeNil)
	test.That(t, config.treeVersion, test.ShouldEqual, 1)

	// Reload and verify new format with version
	config, err = LoadConfigFromFile(confPath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, config.treeVersion, test.ShouldEqual, 1)

	// Verify tree still intact
	node, err = config.Lookup("one/two")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, node.external.Hash, test.ShouldEqual, "hash1")

	node, err = config.Lookup("newFile")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, node.external.Hash, test.ShouldEqual, "newHash")
}

// TestOptimisticLocking_ConcurrentRace simulates a realistic race condition.
func TestOptimisticLocking_ConcurrentRace(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, ConfigName)
	test.That(t, os.WriteFile(confPath, []byte(confRaw), 0o644), test.ShouldBeNil)

	// Create initial state
	config, err := LoadConfigFromFile(confPath)
	test.That(t, err, test.ShouldBeNil)
	config.StoreHash("initial", 1, []string{"initial"})
	test.That(t, config.commitFn(), test.ShouldBeNil)

	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	successCount := 0
	conflictCount := 0
	loadErrorCount := 0
	var mu sync.Mutex

	// Launch multiple goroutines trying to commit simultaneously
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			// Each goroutine loads the config
			// Retry load if we hit a concurrent modification during load
			var cfg *Config
			var err error
			for attempts := 0; attempts < 5; attempts++ {
				cfg, err = LoadConfigFromFile(confPath)
				switch {
				case err == nil:
					// Successfully loaded, break the for loop
					break
				case err.Error() == "tree file is being modified by another process, please retry":
					// Retry loading
					continue
				default:
					// Some other error, break and handle
					break
				}
			}

			if err != nil {
				mu.Lock()
				loadErrorCount++
				mu.Unlock()
				return
			}

			// Make a change
			cfg.StoreHash("hash", id, []string{"file", string(rune('a' + id))})

			// Try to commit
			err = cfg.commitFn()

			mu.Lock()
			if err == nil {
				successCount++
			} else if IsConflictError(err) {
				conflictCount++
			} else {
				t.Errorf("goroutine %d: unexpected error: %v", id, err)
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// At least one goroutine should have succeeded
	// The rest should have gotten conflict errors or load errors
	test.That(t, successCount, test.ShouldBeGreaterThanOrEqualTo, 1)
	test.That(t, successCount+conflictCount+loadErrorCount, test.ShouldEqual, numGoroutines)

	// Final version should be at least 2 (initial commit + successful commits)
	// Retry loading in case a write is in progress
	var finalConfig *Config
	for attempts := 0; attempts < 10; attempts++ {
		finalConfig, err = LoadConfigFromFile(confPath)
		if err == nil {
			break
		}
	}
	test.That(t, err, test.ShouldBeNil)
	test.That(t, finalConfig.treeVersion, test.ShouldBeGreaterThanOrEqualTo, 2)
}

// TestOptimisticLocking_ManualFileModification tests detection of external file changes.
func TestOptimisticLocking_ManualFileModification(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, ConfigName)
	treePath := filepath.Join(dir, TreeName)
	test.That(t, os.WriteFile(confPath, []byte(confRaw), 0o644), test.ShouldBeNil)

	// Load and commit initial state
	config, err := LoadConfigFromFile(confPath)
	test.That(t, err, test.ShouldBeNil)
	config.StoreHash("hash1", 5, []string{"file1"})
	test.That(t, config.commitFn(), test.ShouldBeNil)

	// Load again to work with it
	config, err = LoadConfigFromFile(confPath)
	test.That(t, err, test.ShouldBeNil)
	originalVersion := config.treeVersion
	test.That(t, originalVersion, test.ShouldEqual, 1)

	// Manually modify the tree file (simulating another process or manual edit)
	manualTreeFile := TreeFile{
		Version: 99,
		Tree: TreeNodeTree{
			"manually": &TreeNode{
				external: &TreeNodeExternal{Hash: "modified", Size: 123},
			},
		},
	}
	data, err := json.MarshalIndent(manualTreeFile, "", "  ")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, os.WriteFile(treePath, data, 0o644), test.ShouldBeNil)

	// Try to commit - should detect the external modification
	config.StoreHash("hash2", 10, []string{"file2"})
	err = config.commitFn()
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, IsConflictError(err), test.ShouldBeTrue)
	test.That(t, err.Error(), test.ShouldContainSubstring, "expected version 1")
	test.That(t, err.Error(), test.ShouldContainSubstring, "found version 99")
}

// TestConflictError tests the ConflictError type directly.
func TestConflictError(t *testing.T) {
	err := NewConflictError("/path/to/tree.json", 5, 10)

	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "conflict detected")
	test.That(t, err.Error(), test.ShouldContainSubstring, "/path/to/tree.json")
	test.That(t, err.Error(), test.ShouldContainSubstring, "expected version 5")
	test.That(t, err.Error(), test.ShouldContainSubstring, "found version 10")
	test.That(t, err.Error(), test.ShouldContainSubstring, "reload and retry")

	test.That(t, IsConflictError(err), test.ShouldBeTrue)
	test.That(t, IsConflictError(nil), test.ShouldBeFalse)
	test.That(t, IsConflictError(NewArtifactNotFoundHashError("test")), test.ShouldBeFalse)
}
