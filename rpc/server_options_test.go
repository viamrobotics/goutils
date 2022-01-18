package rpc

import (
	"testing"

	"go.uber.org/multierr"
	"go.viam.com/test"
)

func TestWithAuthHandler(t *testing.T) {
	opts := []ServerOption{WithAuthHandler("sometype", nil)}

	var sOpts serverOptions
	for _, opt := range opts {
		test.That(t, opt.apply(&sOpts), test.ShouldBeNil)
	}

	opts = append(opts, WithAuthHandler("sometype", nil))
	var err error
	for _, opt := range opts {
		err = multierr.Combine(err, opt.apply(&sOpts))
	}
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "already has")
	test.That(t, err.Error(), test.ShouldContainSubstring, "sometype")

	err = nil
	opts = []ServerOption{WithAuthHandler(credentialsTypeInternal, nil)}
	for _, opt := range opts {
		err = multierr.Combine(err, opt.apply(&sOpts))
	}
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "cannot")
	test.That(t, err.Error(), test.ShouldContainSubstring, "externally")
	test.That(t, err.Error(), test.ShouldContainSubstring, string(credentialsTypeInternal))

	err = nil
	opts = []ServerOption{WithAuthHandler("", nil)}
	for _, opt := range opts {
		err = multierr.Combine(err, opt.apply(&sOpts))
	}
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "empty")
}

func TestWithAuthToHandler(t *testing.T) {
	opts := []ServerOption{WithAuthenticateToHandler("sometype", nil)}

	var sOpts serverOptions
	for _, opt := range opts {
		test.That(t, opt.apply(&sOpts), test.ShouldBeNil)
	}

	var err error
	opts = []ServerOption{WithAuthenticateToHandler(credentialsTypeInternal, nil)}
	for _, opt := range opts {
		err = multierr.Combine(err, opt.apply(&sOpts))
	}
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "cannot")
	test.That(t, err.Error(), test.ShouldContainSubstring, "externally")
	test.That(t, err.Error(), test.ShouldContainSubstring, string(credentialsTypeInternal))

	err = nil
	opts = []ServerOption{WithAuthenticateToHandler("", nil)}
	for _, opt := range opts {
		err = multierr.Combine(err, opt.apply(&sOpts))
	}
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "empty")
}
