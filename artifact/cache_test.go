package artifact

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/errors"
	"go.viam.com/test"

	"go.viam.com/utils"
)

func TestGlobalCache(t *testing.T) {
	dir, undo := TestSetupGlobalCache(t)
	defer undo()

	test.That(t, os.MkdirAll(filepath.Join(dir, DotDir), 0o755), test.ShouldBeNil)
	confPath := filepath.Join(dir, DotDir, ConfigName)

	// bad config
	test.That(t, os.WriteFile(confPath, []byte(`{
	"cache": "somedir",
	"root": "someotherdir",
	"source_pull_size_limit": false,
	"ignore": ["one", "two"]
}`), 0o644), test.ShouldBeNil)

	_, err := GlobalCache()
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "bool")

	test.That(t, os.WriteFile(confPath, []byte(`{
	"cache": "somedir",
	"root": "someotherdir",
	"source_pull_size_limit": 5,
	"ignore": ["one", "two"]
}`), 0o644), test.ShouldBeNil)

	_, err = GlobalCache()
	test.That(t, err, test.ShouldBeNil)
}

func TestCache(t *testing.T) {
	t.Run("starting empty", func(t *testing.T) {
		newDir := t.TempDir()
		oldDir := DefaultCachePath
		defer func() {
			DefaultCachePath = oldDir
		}()
		DefaultCachePath = newDir

		conf := &Config{
			commitFn: func() error {
				return nil
			},
			tree: TreeNodeTree{},
		}
		cache, err := NewCache(conf)
		test.That(t, err, test.ShouldBeNil)

		test.That(t, cache.Contains("hash1"), test.ShouldResemble, NewArtifactNotFoundHashError("hash1"))
		_, err = cache.Load("hash1")
		test.That(t, err, test.ShouldResemble, NewArtifactNotFoundHashError("hash1"))
		test.That(t, cache.Store("hash1", strings.NewReader("hello")), test.ShouldBeNil)
		test.That(t, cache.Contains("hash1"), test.ShouldBeNil)
		reader, err := cache.Load("hash1")
		test.That(t, err, test.ShouldBeNil)
		rd, err := io.ReadAll(reader)
		test.That(t, reader.Close(), test.ShouldBeNil)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, string(rd), test.ShouldEqual, "hello")

		barPath := cache.NewPath("foo/bar")
		test.That(t, barPath, test.ShouldEqual, filepath.Join(newDir, "data/foo/bar"))
		_, err = cache.Ensure("foo/bar", true)
		test.That(t, err, test.ShouldResemble, NewArtifactNotFoundPathError("foo/bar"))
		test.That(t, cache.Remove("foo/bar"), test.ShouldBeNil)
		conf.commitFn = func() error {
			return errors.New("whoops")
		}
		err = cache.Remove("foo/bar")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "whoops")

		test.That(t, cache.Clean(), test.ShouldBeNil)

		status, err := cache.Status()
		test.That(t, err, test.ShouldBeNil)
		test.That(t, status, test.ShouldResemble, &Status{})

		barContent := "world"
		barHash, err := computeHash([]byte(barContent))
		test.That(t, err, test.ShouldBeNil)
		test.That(t, os.MkdirAll(filepath.Dir(barPath), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(barPath, []byte(barContent), 0o644), test.ShouldBeNil)

		status, err = cache.Status()
		test.That(t, err, test.ShouldBeNil)
		test.That(t, status, test.ShouldResemble, &Status{
			Unstored: []string{barPath},
		})

		bapContent := "bappin"
		bapHash, err := computeHash([]byte(bapContent))
		test.That(t, err, test.ShouldBeNil)
		bapPath := cache.NewPath("baz/bap")
		test.That(t, os.MkdirAll(filepath.Dir(bapPath), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(bapPath, []byte(bapContent), 0o644), test.ShouldBeNil)

		status, err = cache.Status()
		test.That(t, err, test.ShouldBeNil)
		test.That(t, status, test.ShouldResemble, &Status{
			Unstored: []string{bapPath, barPath},
		})

		conf.commitFn = func() error {
			return nil
		}
		test.That(t, cache.WriteThroughUser(), test.ShouldBeNil)

		reader, err = cache.Load(barHash)
		test.That(t, err, test.ShouldBeNil)
		rd, err = io.ReadAll(reader)
		test.That(t, reader.Close(), test.ShouldBeNil)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, string(rd), test.ShouldResemble, barContent)

		reader, err = cache.Load(bapHash)
		test.That(t, err, test.ShouldBeNil)
		rd, err = io.ReadAll(reader)
		test.That(t, reader.Close(), test.ShouldBeNil)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, string(rd), test.ShouldResemble, bapContent)

		newBapContent := "bappin_again"
		newBapHash, err := computeHash([]byte(newBapContent))
		test.That(t, err, test.ShouldBeNil)
		test.That(t, os.MkdirAll(filepath.Dir(bapPath), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(bapPath, []byte(newBapContent), 0o644), test.ShouldBeNil)

		reader, err = cache.Load(bapHash)
		test.That(t, err, test.ShouldBeNil)
		rd, err = io.ReadAll(reader)
		test.That(t, reader.Close(), test.ShouldBeNil)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, string(rd), test.ShouldResemble, bapContent)

		_, err = cache.Load(newBapHash)
		test.That(t, err, test.ShouldResemble, NewArtifactNotFoundHashError(newBapHash))

		status, err = cache.Status()
		test.That(t, err, test.ShouldBeNil)
		test.That(t, status, test.ShouldResemble, &Status{
			Modified: []string{bapPath},
		})
		test.That(t, cache.WriteThroughUser(), test.ShouldBeNil)
		status, err = cache.Status()
		test.That(t, err, test.ShouldBeNil)
		test.That(t, status, test.ShouldResemble, &Status{})

		deletePath := cache.NewPath("to/be/deleted")
		test.That(t, os.MkdirAll(filepath.Dir(deletePath), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(deletePath, []byte("delete me"), 0o644), test.ShouldBeNil)
		_, err = os.Stat(deletePath)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, cache.Clean(), test.ShouldBeNil)
		_, err = os.Stat(deletePath)
		test.That(t, os.IsNotExist(err), test.ShouldBeTrue)

		test.That(t, os.RemoveAll(cache.NewPath("/")), test.ShouldBeNil)
		_, err = os.Stat(bapPath)
		test.That(t, os.IsNotExist(err), test.ShouldBeTrue)

		_, err = cache.Ensure("baz/bap", true)
		test.That(t, err, test.ShouldBeNil)
		rd, err = os.ReadFile(bapPath)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, string(rd), test.ShouldEqual, newBapContent)

		_, err = os.Stat(barPath)
		test.That(t, os.IsNotExist(err), test.ShouldBeTrue)

		_, err = cache.Ensure("/", true)
		test.That(t, err, test.ShouldBeNil)
		rd, err = os.ReadFile(barPath)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, string(rd), test.ShouldEqual, barContent)

		test.That(t, cache.Close(), test.ShouldBeNil)
	})

	t.Run("nested files", func(t *testing.T) {
		newDir := t.TempDir()
		oldDir := DefaultCachePath
		defer func() {
			DefaultCachePath = oldDir
		}()
		DefaultCachePath = newDir

		conf := &Config{
			commitFn: func() error {
				return nil
			},
			tree: TreeNodeTree{},
		}
		cache, err := NewCache(conf)
		test.That(t, err, test.ShouldBeNil)

		path1 := cache.NewPath("one/two/three")
		path2 := cache.NewPath("one/two/four")
		path3 := cache.NewPath("two/three/four/five")
		content1 := "content1"
		content2 := "content2"
		content3 := "content3"

		test.That(t, os.MkdirAll(filepath.Dir(path1), 0o755), test.ShouldBeNil)
		test.That(t, os.MkdirAll(filepath.Dir(path2), 0o755), test.ShouldBeNil)
		test.That(t, os.MkdirAll(filepath.Dir(path3), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(path1, []byte(content1), 0o644), test.ShouldBeNil)
		test.That(t, os.WriteFile(path2, []byte(content2), 0o644), test.ShouldBeNil)
		test.That(t, os.WriteFile(path3, []byte(content3), 0o644), test.ShouldBeNil)

		test.That(t, cache.WriteThroughUser(), test.ShouldBeNil)
		test.That(t, os.RemoveAll(cache.NewPath("/")), test.ShouldBeNil)

		_, err = cache.Ensure("one", true)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path1)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path2)
		test.That(t, err, test.ShouldBeNil)

		test.That(t, os.RemoveAll(cache.NewPath("/")), test.ShouldBeNil)
		_, err = cache.Ensure("/", true)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path1)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path2)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path3)
		test.That(t, err, test.ShouldBeNil)
	})

	t.Run("cache and root set", func(t *testing.T) {
		artDir := t.TempDir()
		rootDir := filepath.Join(artDir, "root")
		cacheDir := filepath.Join(artDir, "cache")

		conf := &Config{
			Root:  rootDir,
			Cache: cacheDir,
			commitFn: func() error {
				return nil
			},
			tree: TreeNodeTree{},
		}
		cache, err := NewCache(conf)
		test.That(t, err, test.ShouldBeNil)

		path1 := cache.NewPath("one/two/three")
		test.That(t, path1, test.ShouldContainSubstring, rootDir)
		content1 := "content1"
		content1Hash, err := computeHash([]byte(content1))
		test.That(t, err, test.ShouldBeNil)

		test.That(t, os.MkdirAll(filepath.Dir(path1), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(path1, []byte(content1), 0o644), test.ShouldBeNil)

		test.That(t, cache.WriteThroughUser(), test.ShouldBeNil)
		test.That(t, os.RemoveAll(cache.NewPath("/")), test.ShouldBeNil)

		_, err = cache.Ensure("one", true)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path1)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(filepath.Join(cacheDir, content1Hash))
		test.That(t, err, test.ShouldBeNil)
	})

	t.Run("source set", func(t *testing.T) {
		artDir := t.TempDir()
		rootDir := filepath.Join(artDir, "root")
		cacheDir := filepath.Join(artDir, "cache")
		sourceDir := filepath.Join(artDir, "source")

		conf := &Config{
			Root:  rootDir,
			Cache: cacheDir,
			SourceStore: &FileSystemStoreConfig{
				Path: sourceDir,
			},
			commitFn: func() error {
				return nil
			},
			tree: TreeNodeTree{},
		}
		cache, err := NewCache(conf)
		test.That(t, err, test.ShouldBeNil)

		path1 := cache.NewPath("one/two/three")
		test.That(t, path1, test.ShouldContainSubstring, rootDir)
		content1 := "content1"
		content1Hash, err := computeHash([]byte(content1))
		test.That(t, err, test.ShouldBeNil)

		test.That(t, os.MkdirAll(filepath.Dir(path1), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(path1, []byte(content1), 0o644), test.ShouldBeNil)

		test.That(t, cache.WriteThroughUser(), test.ShouldBeNil)
		test.That(t, os.RemoveAll(cache.NewPath("/")), test.ShouldBeNil)

		_, err = cache.Ensure("one", true)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path1)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(filepath.Join(cacheDir, content1Hash))
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(filepath.Join(sourceDir, content1Hash))
		test.That(t, err, test.ShouldBeNil)

		test.That(t, os.RemoveAll(cache.NewPath("/")), test.ShouldBeNil)
		test.That(t, os.RemoveAll(cacheDir), test.ShouldBeNil)
		test.That(t, os.MkdirAll(cacheDir, 0o755), test.ShouldBeNil)
		_, err = cache.Ensure("/", true)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path1)
		test.That(t, err, test.ShouldBeNil)
	})

	t.Run("ensure with size limit", func(t *testing.T) {
		artDir := t.TempDir()
		rootDir := filepath.Join(artDir, "root")
		cacheDir := filepath.Join(artDir, "cache")
		sourceDir := filepath.Join(artDir, "source")

		conf := &Config{
			Root:  rootDir,
			Cache: cacheDir,
			SourceStore: &FileSystemStoreConfig{
				Path: sourceDir,
			},
			SourcePullSizeLimit: 3,
			commitFn: func() error {
				return nil
			},
			tree: TreeNodeTree{},
		}
		cache, err := NewCache(conf)
		test.That(t, err, test.ShouldBeNil)

		path1 := cache.NewPath("one/two/three")
		path2 := cache.NewPath("one/two/four")
		path3 := cache.NewPath("two/three/four/five")
		content1 := "con"
		content2 := "content2"
		content3 := "content3"

		test.That(t, os.MkdirAll(filepath.Dir(path1), 0o755), test.ShouldBeNil)
		test.That(t, os.MkdirAll(filepath.Dir(path2), 0o755), test.ShouldBeNil)
		test.That(t, os.MkdirAll(filepath.Dir(path3), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(path1, []byte(content1), 0o644), test.ShouldBeNil)
		test.That(t, os.WriteFile(path2, []byte(content2), 0o644), test.ShouldBeNil)
		test.That(t, os.WriteFile(path3, []byte(content3), 0o644), test.ShouldBeNil)

		test.That(t, cache.WriteThroughUser(), test.ShouldBeNil)
		test.That(t, os.RemoveAll(cache.NewPath("/")), test.ShouldBeNil)

		_, err = cache.Ensure("one", true)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path1)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path2)
		test.That(t, err, test.ShouldBeNil)

		test.That(t, os.RemoveAll(cache.NewPath("/")), test.ShouldBeNil)
		test.That(t, os.RemoveAll(cacheDir), test.ShouldBeNil)
		test.That(t, os.MkdirAll(cacheDir, 0o755), test.ShouldBeNil)
		_, err = cache.Ensure("/", false)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path1)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path2)
		test.That(t, err, test.ShouldNotBeNil)
		_, err = os.Stat(path3)
		test.That(t, err, test.ShouldNotBeNil)

		_, err = cache.Ensure("/", true)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path1)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path2)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(path3)
		test.That(t, err, test.ShouldBeNil)
	})

	t.Run("ignore", func(t *testing.T) {
		artDir := t.TempDir()
		rootDir := filepath.Join(artDir, "root")
		cacheDir := filepath.Join(artDir, "cache")

		conf := &Config{
			Root:  rootDir,
			Cache: cacheDir,
			commitFn: func() error {
				return nil
			},
			tree:      TreeNodeTree{},
			ignoreSet: utils.NewStringSet("one", "five"),
		}
		cache, err := NewCache(conf)
		test.That(t, err, test.ShouldBeNil)

		path1 := cache.NewPath("one/two/three")
		path2 := cache.NewPath("one/two/four")
		path3 := cache.NewPath("two/three/four/five")
		content1 := "content1"
		content2 := "content2"
		content3 := "content3"
		content1Hash, err := computeHash([]byte(content1))
		test.That(t, err, test.ShouldBeNil)
		content2Hash, err := computeHash([]byte(content2))
		test.That(t, err, test.ShouldBeNil)
		content3Hash, err := computeHash([]byte(content3))
		test.That(t, err, test.ShouldBeNil)

		test.That(t, os.MkdirAll(filepath.Dir(path1), 0o755), test.ShouldBeNil)
		test.That(t, os.MkdirAll(filepath.Dir(path2), 0o755), test.ShouldBeNil)
		test.That(t, os.MkdirAll(filepath.Dir(path3), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(path1, []byte(content1), 0o644), test.ShouldBeNil)
		test.That(t, os.WriteFile(path2, []byte(content2), 0o644), test.ShouldBeNil)
		test.That(t, os.WriteFile(path3, []byte(content3), 0o644), test.ShouldBeNil)

		test.That(t, cache.WriteThroughUser(), test.ShouldBeNil)
		test.That(t, cache.Contains(content1Hash), test.ShouldBeNil)
		test.That(t, cache.Contains(content2Hash), test.ShouldBeNil)
		test.That(t, cache.Contains(content3Hash), test.ShouldNotBeNil)
	})
}

func TestComputeHash(t *testing.T) {
	content1 := "one"
	content2 := "two"
	hash1, err := computeHash([]byte(content1))
	test.That(t, err, test.ShouldBeNil)
	test.That(t, hash1, test.ShouldNotBeEmpty)
	hash2, err := computeHash([]byte(content2))
	test.That(t, err, test.ShouldBeNil)
	test.That(t, hash2, test.ShouldNotBeEmpty)
	test.That(t, hash2, test.ShouldNotEqual, hash1)
}
