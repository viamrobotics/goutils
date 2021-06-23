package secrets

import (
	"context"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.viam.com/test"
)

func TestEnv(t *testing.T) {
	ctx := context.Background()
	s, err := NewSource(ctx, SourceTypeEnv)
	test.That(t, err, test.ShouldBeNil)

	_, err = s.Get(ctx, "lias08123hoiuqhwodaoishdfaoid")
	test.That(t, err, test.ShouldEqual, ErrNotFound)

	key := primitive.NewObjectID().Hex()
	value := "foo"
	test.That(t, os.Setenv(key, value), test.ShouldBeNil)

	u, err := s.Get(ctx, key)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, os.Getenv(key), test.ShouldEqual, u)
}
