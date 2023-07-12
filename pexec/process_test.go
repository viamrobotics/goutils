package pexec

import (
	"bytes"
	"encoding/json"
	"runtime"
	"syscall"
	"testing"
	"time"

	"go.viam.com/test"
)

func TestProcessConfigRoundTripJSON(t *testing.T) {
	config := ProcessConfig{
		ID:          "test",
		Name:        "hello",
		Args:        []string{"1", "2", "3"},
		CWD:         "dir",
		OneShot:     true,
		Log:         true,
		StopSignal:  syscall.SIGTERM,
		StopTimeout: 250 * time.Millisecond,
	}
	md, err := json.Marshal(config)
	test.That(t, err, test.ShouldBeNil)

	var rt ProcessConfig
	err = json.Unmarshal(md, &rt)
	if runtime.GOOS == "windows" {
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "not supported")

		config.StopSignal = 0
		md, err = json.Marshal(config)
		test.That(t, err, test.ShouldBeNil)
	} else {
		test.That(t, err, test.ShouldBeNil)
	}
	test.That(t, rt, test.ShouldResemble, config)

	var rtLower ProcessConfig
	test.That(t, json.Unmarshal(bytes.ToLower(md), &rtLower), test.ShouldBeNil)
	test.That(t, rtLower, test.ShouldResemble, config)
}

func TestProcessConfigValidate(t *testing.T) {
	var emptyConfig ProcessConfig
	err := emptyConfig.Validate("path")
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, `"id" is required`)

	invalidConfig := ProcessConfig{
		ID: "id1",
	}
	err = invalidConfig.Validate("path")
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, `"name" is required`)

	// assert that mutating the ProcessConfig to be invalid (StopTimeout < 100)
	// does not change cached validation error.
	invalidConfig.Name = "foo"
	invalidConfig.StopTimeout = 50
	err = invalidConfig.Validate("path")
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, `"name" is required`)

	invalidConfig = ProcessConfig{
		ID:          "id1",
		Name:        "foo",
		StopTimeout: 50,
	}
	err = invalidConfig.Validate("path")
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, `stop_timeout should not be less than 100ms`)

	validConfig := ProcessConfig{
		ID:          "id1",
		Name:        "foo",
		StopTimeout: time.Second,
	}
	test.That(t, validConfig.Validate("path"), test.ShouldBeNil)
}
