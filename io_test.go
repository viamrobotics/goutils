package utils

import (
	"context"
	"testing"

	"go.viam.com/test"
)

func TestReadBytes(t *testing.T) {
	x, err := ReadBytes(context.Background(), &dummyReader{}, 4)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(x), test.ShouldEqual, 4)
	test.That(t, x[0], test.ShouldEqual, 0x5)
	test.That(t, x[1], test.ShouldEqual, 0x5)
	test.That(t, x[2], test.ShouldEqual, 0x5)
	test.That(t, x[3], test.ShouldEqual, 0x5)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ReadBytes(ctx, &dummyReader{}, 4)
	test.That(t, err, test.ShouldBeError, context.Canceled)
}

type dummyReader struct{}

func (r *dummyReader) Read(buf []byte) (int, error) {
	buf[0] = 0x5
	buf[1] = 0x5
	return 2, nil
}
