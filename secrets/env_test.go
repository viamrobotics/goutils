package secrets

import (
	"context"
	"os"
	"testing"

	"go.viam.com/test"
)

func TestEnv(t *testing.T) {
	ctx := context.Background()
	s, err := NewSecretSource(ctx, SecretSourceTypeEnv)
	test.That(t, err, test.ShouldBeNil)

	_, err = s.Get(ctx, "lias08123hoiuqhwodaoishdfaoid")
	test.That(t, err, test.ShouldEqual, ErrSecretNotFound)

	u, err := s.Get(ctx, "USER")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, os.Getenv("USER"), test.ShouldEqual, u)

}
