package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/edaniels/golog"
	"go.uber.org/zap/zaptest/observer"
	"go.viam.com/test"

	"go.viam.com/utils/artifact"
	"go.viam.com/utils/artifact/tools"
	"go.viam.com/utils/testutils"
)

//nolint:dupl
func TestMainMain(t *testing.T) {
	// all other setups do not need to undo; just this one.
	_, mainUndo := artifact.TestSetupGlobalCache(t)
	defer mainUndo()

	before := func(t *testing.T, _ golog.Logger, _ *testutils.ContextualMainExecution) {
		t.Helper()
		dir, _ := artifact.TestSetupGlobalCache(t)
		test.That(t, os.MkdirAll(filepath.Join(dir, artifact.DotDir), 0o755), test.ShouldBeNil)
		confPath := filepath.Join(dir, artifact.DotDir, artifact.ConfigName)
		test.That(t, os.WriteFile(confPath, []byte(`{}`), 0o644), test.ShouldBeNil)
	}

	pullBeforeWithLimit := func(t *testing.T, limit bool) {
		t.Helper()
		dir, _ := artifact.TestSetupGlobalCache(t)
		test.That(t, os.MkdirAll(filepath.Join(dir, artifact.DotDir), 0o755), test.ShouldBeNil)
		confPath := filepath.Join(dir, artifact.DotDir, artifact.ConfigName)
		sourcePath := filepath.Join(dir, "source")
		test.That(t, os.MkdirAll(sourcePath, 0o755), test.ShouldBeNil)
		if limit {
			test.That(t, os.WriteFile(confPath, []byte(fmt.Sprintf(`{
			"source_store": {
				"type": "fs",
				"path": "%s"
			},
			"source_pull_size_limit": 3
		}`, sourcePath)), 0o644), test.ShouldBeNil)
		} else {
			test.That(t, os.WriteFile(confPath, []byte(fmt.Sprintf(`{
			"source_store": {
				"type": "fs",
				"path": "%s"
			}
		}`, sourcePath)), 0o644), test.ShouldBeNil)
		}
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
	}
	pullBefore := func(t *testing.T, _ golog.Logger, _ *testutils.ContextualMainExecution) {
		t.Helper()
		pullBeforeWithLimit(t, false)
	}

	pullLimitBefore := func(t *testing.T, _ golog.Logger, _ *testutils.ContextualMainExecution) {
		t.Helper()
		pullBeforeWithLimit(t, true)
	}

	pushBefore := func(t *testing.T, _ golog.Logger, _ *testutils.ContextualMainExecution) {
		t.Helper()
		dir, _ := artifact.TestSetupGlobalCache(t)
		test.That(t, os.MkdirAll(filepath.Join(dir, artifact.DotDir), 0o755), test.ShouldBeNil)
		confPath := filepath.Join(dir, artifact.DotDir, artifact.ConfigName)
		test.That(t, os.WriteFile(confPath, []byte(`{}`), 0o644), test.ShouldBeNil)

		filePath := artifact.MustNewPath("some/file")
		test.That(t, os.MkdirAll(filepath.Dir(filePath), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(filePath, []byte("hello"), 0o644), test.ShouldBeNil)
		otherFilePath := artifact.MustNewPath("some/other_file")
		test.That(t, os.MkdirAll(filepath.Dir(otherFilePath), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(otherFilePath, []byte("world"), 0o644), test.ShouldBeNil)
	}

	removeBefore := func(t *testing.T, _ golog.Logger, _ *testutils.ContextualMainExecution) {
		t.Helper()
		dir, _ := artifact.TestSetupGlobalCache(t)
		test.That(t, os.MkdirAll(filepath.Join(dir, artifact.DotDir), 0o755), test.ShouldBeNil)
		confPath := filepath.Join(dir, artifact.DotDir, artifact.ConfigName)
		test.That(t, os.WriteFile(confPath, []byte(`{}`), 0o644), test.ShouldBeNil)

		filePath := artifact.MustNewPath("some/file")
		test.That(t, os.MkdirAll(filepath.Dir(filePath), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(filePath, []byte("hello"), 0o644), test.ShouldBeNil)
		otherFilePath := artifact.MustNewPath("some/other_file")
		test.That(t, os.MkdirAll(filepath.Dir(otherFilePath), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(otherFilePath, []byte("world"), 0o644), test.ShouldBeNil)
		test.That(t, tools.Push(), test.ShouldBeNil)
	}

	statusBefore := func(t *testing.T, _ golog.Logger, _ *testutils.ContextualMainExecution) {
		t.Helper()
		dir, _ := artifact.TestSetupGlobalCache(t)
		test.That(t, os.MkdirAll(filepath.Join(dir, artifact.DotDir), 0o755), test.ShouldBeNil)
		confPath := filepath.Join(dir, artifact.DotDir, artifact.ConfigName)
		test.That(t, os.WriteFile(confPath, []byte(`{}`), 0o644), test.ShouldBeNil)

		filePath := artifact.MustNewPath("some/file")
		test.That(t, os.MkdirAll(filepath.Dir(filePath), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(filePath, []byte("hello"), 0o644), test.ShouldBeNil)
		otherFilePath := artifact.MustNewPath("some/other_file")
		test.That(t, os.MkdirAll(filepath.Dir(otherFilePath), 0o755), test.ShouldBeNil)
		test.That(t, os.WriteFile(otherFilePath, []byte("world"), 0o644), test.ShouldBeNil)
	}

	testutils.TestMain(t, mainWithArgs, []testutils.MainTestCase{
		{"no args", nil, "clean|pull|push|rm|status", nil, nil, nil},
		{"unknown", []string{"unknown"}, "clean|pull|push|rm|status", nil, nil, nil},
		{"clean nothing", []string{"clean"}, "", before, nil, nil},
		{
			"clean something",
			[]string{"clean"},
			"",
			func(t *testing.T, logger golog.Logger, exec *testutils.ContextualMainExecution) {
				t.Helper()
				before(t, logger, exec)

				filePath := artifact.MustNewPath("some/file")
				test.That(t, os.MkdirAll(filepath.Dir(filePath), 0o755), test.ShouldBeNil)
				test.That(t, os.WriteFile(filePath, []byte("hello"), 0o644), test.ShouldBeNil)
				_, err := os.Stat(filePath)
				test.That(t, err, test.ShouldBeNil)
			}, nil, func(t *testing.T, _ *observer.ObservedLogs) {
				t.Helper()
				filePath := artifact.MustNewPath("some/file")
				_, err := os.Stat(filePath)
				test.That(t, err, test.ShouldNotBeNil)
			},
		},
		{"pull bad args", []string{"pull", "--all=hello"}, "boolean", nil, nil, nil},
		{
			"pull",
			[]string{"pull"},
			"",
			func(t *testing.T, logger golog.Logger, exec *testutils.ContextualMainExecution) {
				t.Helper()
				pullBefore(t, logger, exec)
			}, nil, func(t *testing.T, _ *observer.ObservedLogs) {
				t.Helper()
				_, err := os.Stat(artifact.MustNewPath("one/two"))
				test.That(t, err, test.ShouldBeNil)
				_, err = os.Stat(artifact.MustNewPath("one/three"))
				test.That(t, err, test.ShouldBeNil)
				_, err = os.Stat(artifact.MustNewPath("two"))
				test.That(t, err, test.ShouldBeNil)
			},
		},
		{
			"pull specific",
			[]string{"pull", "one/two"},
			"",
			func(t *testing.T, logger golog.Logger, exec *testutils.ContextualMainExecution) {
				t.Helper()
				pullBefore(t, logger, exec)
			}, nil, func(t *testing.T, _ *observer.ObservedLogs) {
				t.Helper()
				_, err := os.Stat(artifact.MustNewPath("one/two"))
				test.That(t, err, test.ShouldBeNil)
				_, err = os.Stat(artifact.MustNewPath("one/three"))
				test.That(t, err, test.ShouldNotBeNil)
				_, err = os.Stat(artifact.MustNewPath("two"))
				test.That(t, err, test.ShouldNotBeNil)
			},
		},
		{
			"pull limit",
			[]string{"pull"},
			"",
			func(t *testing.T, logger golog.Logger, exec *testutils.ContextualMainExecution) {
				t.Helper()
				pullLimitBefore(t, logger, exec)
			}, nil, func(t *testing.T, _ *observer.ObservedLogs) {
				t.Helper()
				_, err := os.Stat(artifact.MustNewPath("one/two"))
				test.That(t, err, test.ShouldNotBeNil)
				_, err = os.Stat(artifact.MustNewPath("one/three"))
				test.That(t, err, test.ShouldNotBeNil)
				_, err = os.Stat(artifact.MustNewPath("two"))
				test.That(t, err, test.ShouldNotBeNil)
			},
		},
		{
			"pull limit specific",
			[]string{"pull", "one/two"},
			"",
			func(t *testing.T, logger golog.Logger, exec *testutils.ContextualMainExecution) {
				t.Helper()
				pullLimitBefore(t, logger, exec)
			}, nil, func(t *testing.T, _ *observer.ObservedLogs) {
				t.Helper()
				_, err := os.Stat(artifact.MustNewPath("one/two"))
				test.That(t, err, test.ShouldNotBeNil)
				_, err = os.Stat(artifact.MustNewPath("one/three"))
				test.That(t, err, test.ShouldNotBeNil)
				_, err = os.Stat(artifact.MustNewPath("two"))
				test.That(t, err, test.ShouldNotBeNil)
			},
		},
		{
			"pull limit all",
			[]string{"pull", "--all"},
			"",
			func(t *testing.T, logger golog.Logger, exec *testutils.ContextualMainExecution) {
				t.Helper()
				pullLimitBefore(t, logger, exec)
			}, nil, func(t *testing.T, _ *observer.ObservedLogs) {
				t.Helper()
				_, err := os.Stat(artifact.MustNewPath("one/two"))
				test.That(t, err, test.ShouldBeNil)
				_, err = os.Stat(artifact.MustNewPath("one/three"))
				test.That(t, err, test.ShouldBeNil)
				_, err = os.Stat(artifact.MustNewPath("two"))
				test.That(t, err, test.ShouldBeNil)
			},
		},
		{
			"push",
			[]string{"push"},
			"",
			func(t *testing.T, logger golog.Logger, exec *testutils.ContextualMainExecution) {
				t.Helper()
				pushBefore(t, logger, exec)
			}, nil, func(t *testing.T, _ *observer.ObservedLogs) {
				t.Helper()
				filePath := artifact.MustNewPath("some/file")
				otherFilePath := artifact.MustNewPath("some/other_file")

				test.That(t, os.RemoveAll(artifact.MustNewPath("/")), test.ShouldBeNil)
				_, err := os.Stat(filePath)
				test.That(t, err, test.ShouldNotBeNil)
				_, err = os.Stat(otherFilePath)
				test.That(t, err, test.ShouldNotBeNil)

				test.That(t, tools.Pull("/", true), test.ShouldBeNil)
				_, err = os.Stat(filePath)
				test.That(t, err, test.ShouldBeNil)
				_, err = os.Stat(otherFilePath)
				test.That(t, err, test.ShouldBeNil)
			},
		},
		{"remove bad args", []string{"rm"}, "required", nil, nil, nil},
		{
			"remove specific",
			[]string{"rm", "some/file"},
			"",
			func(t *testing.T, logger golog.Logger, exec *testutils.ContextualMainExecution) {
				t.Helper()
				removeBefore(t, logger, exec)
				test.That(t, os.Chdir(artifact.MustNewPath("/")), test.ShouldBeNil)
			}, nil, func(t *testing.T, _ *observer.ObservedLogs) {
				t.Helper()
				filePath := artifact.MustNewPath("some/file")
				otherFilePath := artifact.MustNewPath("some/other_file")

				test.That(t, os.RemoveAll(artifact.MustNewPath("/")), test.ShouldBeNil)
				_, err := os.Stat(filePath)
				test.That(t, err, test.ShouldNotBeNil)
				_, err = os.Stat(otherFilePath)
				test.That(t, err, test.ShouldNotBeNil)

				test.That(t, tools.Pull("/", true), test.ShouldBeNil)
				_, err = os.Stat(filePath)
				test.That(t, err, test.ShouldNotBeNil)
				_, err = os.Stat(otherFilePath)
				test.That(t, err, test.ShouldBeNil)
			},
		},
		{
			"remove specific unknown",
			[]string{"rm", "some/unknown_file"},
			"",
			func(t *testing.T, logger golog.Logger, exec *testutils.ContextualMainExecution) {
				t.Helper()
				removeBefore(t, logger, exec)
				test.That(t, os.Chdir(artifact.MustNewPath("/")), test.ShouldBeNil)
			}, nil, func(t *testing.T, _ *observer.ObservedLogs) {
				t.Helper()
				filePath := artifact.MustNewPath("some/file")
				otherFilePath := artifact.MustNewPath("some/other_file")

				test.That(t, os.RemoveAll(artifact.MustNewPath("/")), test.ShouldBeNil)
				_, err := os.Stat(filePath)
				test.That(t, err, test.ShouldNotBeNil)
				_, err = os.Stat(otherFilePath)
				test.That(t, err, test.ShouldNotBeNil)

				test.That(t, tools.Pull("/", true), test.ShouldBeNil)
				_, err = os.Stat(filePath)
				test.That(t, err, test.ShouldBeNil)
				_, err = os.Stat(otherFilePath)
				test.That(t, err, test.ShouldBeNil)
			},
		},
		{"remove root does nothing", []string{"rm", "/"}, "", func(t *testing.T, logger golog.Logger, exec *testutils.ContextualMainExecution) {
			t.Helper()
			removeBefore(t, logger, exec)
			test.That(t, os.Chdir(artifact.MustNewPath("/")), test.ShouldBeNil)
		}, nil, func(t *testing.T, _ *observer.ObservedLogs) {
			t.Helper()
			filePath := artifact.MustNewPath("some/file")
			otherFilePath := artifact.MustNewPath("some/other_file")

			test.That(t, os.RemoveAll(artifact.MustNewPath("/")), test.ShouldBeNil)
			_, err := os.Stat(filePath)
			test.That(t, err, test.ShouldNotBeNil)
			_, err = os.Stat(otherFilePath)
			test.That(t, err, test.ShouldNotBeNil)

			test.That(t, tools.Pull("/", true), test.ShouldBeNil)
			_, err = os.Stat(filePath)
			test.That(t, err, test.ShouldBeNil)
			_, err = os.Stat(otherFilePath)
			test.That(t, err, test.ShouldBeNil)
		}},
		{"status", []string{"status"}, "", before, nil, func(t *testing.T, logs *observer.ObservedLogs) {
			t.Helper()
			test.That(t, len(logs.FilterMessageSnippet("").All()), test.ShouldEqual, 0)
		}},
		{"status unstored", []string{"status"}, "", statusBefore, nil, func(t *testing.T, logs *observer.ObservedLogs) {
			t.Helper()
			filePath := artifact.MustNewPath("some/file")
			otherFilePath := artifact.MustNewPath("some/other_file")

			messages := logs.FilterMessageSnippet("").All()
			test.That(t, messages, test.ShouldHaveLength, 1)
			test.That(t, messages[0].Message, test.ShouldContainSubstring, "Unstored")
			test.That(t, messages[0].Message, test.ShouldNotContainSubstring, "Modified")
			test.That(t, messages[0].Message, test.ShouldContainSubstring, filePath)
			test.That(t, messages[0].Message, test.ShouldContainSubstring, otherFilePath)
		}},
		{"status modified", []string{"status"}, "", func(t *testing.T, logger golog.Logger, exec *testutils.ContextualMainExecution) {
			t.Helper()
			statusBefore(t, logger, exec)
			test.That(t, tools.Push(), test.ShouldBeNil)
			otherFilePath := artifact.MustNewPath("some/other_file")
			test.That(t, os.WriteFile(otherFilePath, []byte("changes"), 0o644), test.ShouldBeNil)
		}, nil, func(t *testing.T, logs *observer.ObservedLogs) {
			t.Helper()
			filePath := artifact.MustNewPath("some/file")
			otherFilePath := artifact.MustNewPath("some/other_file")

			messages := logs.FilterMessageSnippet("").All()
			test.That(t, messages, test.ShouldHaveLength, 1)
			test.That(t, messages[0].Message, test.ShouldNotContainSubstring, "Unstored")
			test.That(t, messages[0].Message, test.ShouldContainSubstring, "Modified")
			test.That(t, messages[0].Message, test.ShouldNotContainSubstring, filePath)
			test.That(t, messages[0].Message, test.ShouldContainSubstring, otherFilePath)
		}},
		{
			"status unstored and modified",
			[]string{"status"},
			"",
			func(t *testing.T, logger golog.Logger, exec *testutils.ContextualMainExecution) {
				t.Helper()
				statusBefore(t, logger, exec)
				test.That(t, tools.Push(), test.ShouldBeNil)
				otherFilePath := artifact.MustNewPath("some/other_file")
				test.That(t, os.WriteFile(otherFilePath, []byte("changes"), 0o644), test.ShouldBeNil)
				newFilePath := artifact.MustNewPath("some/new_file")
				test.That(t, os.MkdirAll(filepath.Dir(newFilePath), 0o755), test.ShouldBeNil)
				test.That(t, os.WriteFile(newFilePath, []byte("newwwww"), 0o644), test.ShouldBeNil)
			}, nil, func(t *testing.T, logs *observer.ObservedLogs) {
				t.Helper()
				filePath := artifact.MustNewPath("some/file")
				otherFilePath := artifact.MustNewPath("some/other_file")
				newFilePath := artifact.MustNewPath("some/new_file")

				messages := logs.FilterMessageSnippet("").All()
				test.That(t, messages, test.ShouldHaveLength, 1)
				test.That(t, messages[0].Message, test.ShouldContainSubstring, "Unstored")
				test.That(t, messages[0].Message, test.ShouldContainSubstring, "Modified")
				test.That(t, messages[0].Message, test.ShouldNotContainSubstring, filePath)
				test.That(t, messages[0].Message, test.ShouldContainSubstring, otherFilePath)
				test.That(t, messages[0].Message, test.ShouldContainSubstring, newFilePath)
			},
		},
	})
}
