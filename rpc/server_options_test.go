package rpc

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v4"
	"go.uber.org/multierr"
	"go.viam.com/test"
)

func TestWithAuthHandler(t *testing.T) {
	opts := []ServerOption{WithAuthHandler("sometype", AuthHandlerFunc(
		func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{"a": "1"}, nil
		}),
	)}

	var sOpts serverOptions
	for _, opt := range opts {
		test.That(t, opt.apply(&sOpts), test.ShouldBeNil)
	}

	opts = append(opts, WithAuthHandler("sometype", AuthHandlerFunc(
		func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{"a": "2"}, nil
		})))
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

func TestWithEntityDataLoader(t *testing.T) {
	opts := []ServerOption{WithEntityDataLoader("sometype", EntityDataLoaderFunc(
		func(ctx context.Context, claims Claims) (interface{}, error) {
			return map[string]string{"a": "1"}, nil
		}),
	)}

	var sOpts serverOptions
	for _, opt := range opts {
		test.That(t, opt.apply(&sOpts), test.ShouldBeNil)
	}

	opts = append(opts, WithEntityDataLoader("sometype", EntityDataLoaderFunc(
		func(ctx context.Context, claims Claims) (interface{}, error) {
			return map[string]string{"a": "2"}, nil
		})))
	var err error
	for _, opt := range opts {
		err = multierr.Combine(err, opt.apply(&sOpts))
	}
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "already has")
	test.That(t, err.Error(), test.ShouldContainSubstring, "sometype")

	err = nil
	opts = []ServerOption{WithEntityDataLoader(credentialsTypeInternal, nil)}
	for _, opt := range opts {
		err = multierr.Combine(err, opt.apply(&sOpts))
	}
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "cannot")
	test.That(t, err.Error(), test.ShouldContainSubstring, "externally")
	test.That(t, err.Error(), test.ShouldContainSubstring, string(credentialsTypeInternal))

	err = nil
	opts = []ServerOption{WithEntityDataLoader("", nil)}
	for _, opt := range opts {
		err = multierr.Combine(err, opt.apply(&sOpts))
	}
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "empty")
}

func TestWithTokenVerificationKeyProvider(t *testing.T) {
	opts := []ServerOption{WithTokenVerificationKeyProvider("sometype", TokenVerificationKeyProviderFunc(
		func(ctx context.Context, token *jwt.Token) (interface{}, error) {
			return map[string]string{"a": "1"}, nil
		}),
	)}

	var sOpts serverOptions
	for _, opt := range opts {
		test.That(t, opt.apply(&sOpts), test.ShouldBeNil)
	}

	opts = append(opts, WithTokenVerificationKeyProvider("sometype", TokenVerificationKeyProviderFunc(
		func(ctx context.Context, token *jwt.Token) (interface{}, error) {
			return map[string]string{"a": "2"}, nil
		})))
	var err error
	for _, opt := range opts {
		err = multierr.Combine(err, opt.apply(&sOpts))
	}
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "already has")
	test.That(t, err.Error(), test.ShouldContainSubstring, "sometype")

	err = nil
	opts = []ServerOption{WithTokenVerificationKeyProvider(credentialsTypeInternal, nil)}
	for _, opt := range opts {
		err = multierr.Combine(err, opt.apply(&sOpts))
	}
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "cannot")
	test.That(t, err.Error(), test.ShouldContainSubstring, "externally")
	test.That(t, err.Error(), test.ShouldContainSubstring, string(credentialsTypeInternal))

	err = nil
	opts = []ServerOption{WithTokenVerificationKeyProvider("", nil)}
	for _, opt := range opts {
		err = multierr.Combine(err, opt.apply(&sOpts))
	}
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "empty")
}
