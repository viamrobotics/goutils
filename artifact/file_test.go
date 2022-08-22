package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.viam.com/test"

	"go.viam.com/utils/testutils"
)

func TestPath(t *testing.T) {
	dir, undo := TestSetupGlobalCache(t)
	defer undo()

	_, err := Path("to/somewhere")
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "not found")

	test.That(t, func() {
		MustPath("to/somewhere")
	}, test.ShouldPanic)

	test.That(t, os.MkdirAll(filepath.Join(dir, DotDir), 0o755), test.ShouldBeNil)
	confPath := filepath.Join(dir, DotDir, ConfigName)
	test.That(t, os.WriteFile(confPath, []byte(`{
	"cache": "somedir",
	"root": "someotherdir",
	"source_pull_size_limit": 5,
	"ignore": ["one", "two"]
}`), 0o644), test.ShouldBeNil)
	found, err := searchConfig()
	test.That(t, err, test.ShouldBeNil)

	rootDir := filepath.Join(filepath.Dir(found), "someotherdir")

	_, err = Path("to/somewhere")
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "not found")
	test.That(t, err.Error(), test.ShouldContainSubstring, "to/somewhere")

	test.That(t, func() {
		MustPath("to/somewhere")
	}, test.ShouldPanic)

	cache, err := GlobalCache()
	test.That(t, err, test.ShouldBeNil)

	toSomePath := filepath.Join(rootDir, "to/somewhere")
	test.That(t, os.MkdirAll(filepath.Dir(toSomePath), 0o755), test.ShouldBeNil)
	test.That(t, os.WriteFile(toSomePath, []byte("hello world"), 0o644), test.ShouldBeNil)
	test.That(t, cache.WriteThroughUser(), test.ShouldBeNil)

	resolved, err := Path("to/somewhere")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, resolved, test.ShouldEqual, filepath.Join(filepath.Dir(found), "someotherdir/to/somewhere"))

	resolved = MustPath("to/somewhere")
	test.That(t, resolved, test.ShouldEqual, filepath.Join(filepath.Dir(found), "someotherdir/to/somewhere"))
}

func TestNewPath(t *testing.T) {
	dir, undo := TestSetupGlobalCache(t)
	defer undo()

	test.That(t, os.MkdirAll(filepath.Join(dir, DotDir), 0o755), test.ShouldBeNil)
	confPath := filepath.Join(dir, DotDir, ConfigName)
	test.That(t, os.WriteFile(confPath, []byte(confRaw), 0o644), test.ShouldBeNil)
	found, err := searchConfig()
	test.That(t, err, test.ShouldBeNil)

	resolved, err := NewPath("to/somewhere")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, resolved, test.ShouldEqual, filepath.Join(filepath.Dir(found), "someotherdir/to/somewhere"))

	resolved = MustNewPath("to/somewhere")
	test.That(t, resolved, test.ShouldEqual, filepath.Join(filepath.Dir(found), "someotherdir/to/somewhere"))
}

func TestEmplaceFile(t *testing.T) {
	storeDir := testutils.TempDirT(t, "", "file_test")
	defer os.RemoveAll(storeDir)
	rootDir := testutils.TempDirT(t, "", "file_test")
	defer os.RemoveAll(rootDir)

	store, err := newFileSystemStore(&FileSystemStoreConfig{Path: storeDir})
	test.That(t, err, test.ShouldBeNil)

	unknownHash := "foo"
	file1Path := filepath.Join(storeDir, "file1")
	err = emplaceFile(store, unknownHash, file1Path)
	test.That(t, IsNotFoundError(err), test.ShouldBeTrue)
	test.That(t, err, test.ShouldResemble, &NotFoundError{hash: &unknownHash})
	_, err = os.Stat(file1Path)
	test.That(t, err, test.ShouldNotBeNil)

	content1 := "mycoolcontent"
	content2 := "myothercoolcontent"

	hashVal1, err := computeHash([]byte(content1))
	test.That(t, err, test.ShouldBeNil)
	hashVal2, err := computeHash([]byte(content2))
	test.That(t, err, test.ShouldBeNil)

	test.That(t, store.Store(hashVal1, strings.NewReader(content1)), test.ShouldBeNil)
	test.That(t, store.Store(hashVal2, strings.NewReader(content2)), test.ShouldBeNil)

	test.That(t, emplaceFile(store, hashVal1, file1Path), test.ShouldBeNil)
	rd, err := os.ReadFile(file1Path)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, string(rd), test.ShouldEqual, content1)

	test.That(t, emplaceFile(store, hashVal2, file1Path), test.ShouldBeNil)
	rd, err = os.ReadFile(file1Path)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, string(rd), test.ShouldEqual, content2)

	file2Path := filepath.Join(storeDir, "file2")
	test.That(t, emplaceFile(store, hashVal1, file2Path), test.ShouldBeNil)
	rd, err = os.ReadFile(file2Path)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, string(rd), test.ShouldEqual, content1)
	rd, err = os.ReadFile(file1Path)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, string(rd), test.ShouldEqual, content2)

	file3Path := filepath.Join(storeDir, "one", "two", "three", "file")
	test.That(t, emplaceFile(store, hashVal1, file3Path), test.ShouldBeNil)
	rd, err = os.ReadFile(file3Path)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, string(rd), test.ShouldEqual, content1)
}
