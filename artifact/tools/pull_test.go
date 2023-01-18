package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.viam.com/test"

	"go.viam.com/utils/artifact"
)

func TestPull(t *testing.T) {
	dir, undo := artifact.TestSetupGlobalCache(t)
	defer undo()

	test.That(t, os.MkdirAll(filepath.Join(dir, artifact.DotDir), 0o755), test.ShouldBeNil)
	confPath := filepath.Join(dir, artifact.DotDir, artifact.ConfigName)
	sourcePath := filepath.Join(dir, "source")
	test.That(t, os.MkdirAll(sourcePath, 0o755), test.ShouldBeNil)
	test.That(t, os.WriteFile(confPath, []byte(fmt.Sprintf(`{
		"source_store": {
			"type": "fs",
			"path": "%s"
		}
	}`, strings.ReplaceAll(sourcePath, "\\", "\\\\"))), 0o644), test.ShouldBeNil)
	treePath := filepath.Join(dir, artifact.DotDir, artifact.TreeName)
	test.That(t, os.WriteFile(treePath, []byte(`{
		"one": {
			"two": {
				"size": 10,
				"hash": "foo"
			},
			"three": {
				"size": 10,
				"hash": "bar"
			}
		},
		"two": {
			"size": 10,
			"hash": "baz"
		}
	}`), 0o644), test.ShouldBeNil)

	store, err := artifact.NewStore(&artifact.FileSystemStoreConfig{Path: sourcePath})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, store.Store("foo", strings.NewReader("foocontent")), test.ShouldBeNil)
	test.That(t, store.Store("bar", strings.NewReader("barcontent")), test.ShouldBeNil)
	test.That(t, store.Store("baz", strings.NewReader("bazcontent")), test.ShouldBeNil)

	test.That(t, Pull("one/two", true), test.ShouldBeNil)

	_, err = os.Stat(artifact.MustNewPath("one/two"))
	test.That(t, err, test.ShouldBeNil)
	_, err = os.Stat(artifact.MustNewPath("one/three"))
	test.That(t, err, test.ShouldNotBeNil)
	_, err = os.Stat(artifact.MustNewPath("two"))
	test.That(t, err, test.ShouldNotBeNil)

	test.That(t, Pull("/", true), test.ShouldBeNil)

	_, err = os.Stat(artifact.MustNewPath("one/two"))
	test.That(t, err, test.ShouldBeNil)
	_, err = os.Stat(artifact.MustNewPath("one/three"))
	test.That(t, err, test.ShouldBeNil)
	_, err = os.Stat(artifact.MustNewPath("two"))
	test.That(t, err, test.ShouldBeNil)
}

func TestPullLimit(t *testing.T) {
	dir, undo := artifact.TestSetupGlobalCache(t)
	defer undo()

	test.That(t, os.MkdirAll(filepath.Join(dir, artifact.DotDir), 0o755), test.ShouldBeNil)
	confPath := filepath.Join(dir, artifact.DotDir, artifact.ConfigName)
	sourcePath := filepath.Join(dir, "source")
	test.That(t, os.MkdirAll(sourcePath, 0o755), test.ShouldBeNil)
	test.That(t, os.WriteFile(confPath, []byte(fmt.Sprintf(`{
		"source_store": {
			"type": "fs",
			"path": "%s"
		},
		"source_pull_size_limit": 3
	}`, strings.ReplaceAll(sourcePath, "\\", "\\\\"))), 0o644), test.ShouldBeNil)
	treePath := filepath.Join(dir, artifact.DotDir, artifact.TreeName)
	test.That(t, os.WriteFile(treePath, []byte(`{
		"one": {
			"two": {
				"size": 10,
				"hash": "foo"
			},
			"three": {
				"size": 10,
				"hash": "bar"
			}
		},
		"two": {
			"size": 10,
			"hash": "baz"
		}
	}`), 0o644), test.ShouldBeNil)

	store, err := artifact.NewStore(&artifact.FileSystemStoreConfig{Path: sourcePath})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, store.Store("foo", strings.NewReader("foocontent")), test.ShouldBeNil)
	test.That(t, store.Store("bar", strings.NewReader("barcontent")), test.ShouldBeNil)
	test.That(t, store.Store("baz", strings.NewReader("bazcontent")), test.ShouldBeNil)

	test.That(t, Pull("one/two", false), test.ShouldBeNil)
	_, err = os.Stat(artifact.MustNewPath("one/two"))
	test.That(t, err, test.ShouldNotBeNil)
	_, err = os.Stat(artifact.MustNewPath("one/three"))
	test.That(t, err, test.ShouldNotBeNil)
	_, err = os.Stat(artifact.MustNewPath("two"))
	test.That(t, err, test.ShouldNotBeNil)

	test.That(t, Pull("/", false), test.ShouldBeNil)

	_, err = os.Stat(artifact.MustNewPath("one/two"))
	test.That(t, err, test.ShouldNotBeNil)
	_, err = os.Stat(artifact.MustNewPath("one/three"))
	test.That(t, err, test.ShouldNotBeNil)
	_, err = os.Stat(artifact.MustNewPath("two"))
	test.That(t, err, test.ShouldNotBeNil)

	test.That(t, Pull("/", true), test.ShouldBeNil)
	_, err = os.Stat(artifact.MustNewPath("one/two"))
	test.That(t, err, test.ShouldBeNil)
	_, err = os.Stat(artifact.MustNewPath("one/three"))
	test.That(t, err, test.ShouldBeNil)
	_, err = os.Stat(artifact.MustNewPath("two"))
	test.That(t, err, test.ShouldBeNil)
}
