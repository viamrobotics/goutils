package utils

import (
	"context"
	"errors"
	"testing"

	"go.viam.com/test"
)

func TestRetryNTimes(t *testing.T) {
	ctxBg := context.Background()
	t.Run("success on first try", func(t *testing.T) {
		attempts := 0
		result, err := RetryNTimesWithSleep(ctxBg, func() (string, error) {
			attempts++
			return "success", nil
		}, 3, 0)

		test.That(t, err, test.ShouldBeNil)
		test.That(t, result, test.ShouldEqual, "success")
		test.That(t, attempts, test.ShouldEqual, 1)
	})

	t.Run("success after retries", func(t *testing.T) {
		attempts := 0
		result, err := RetryNTimesWithSleep(ctxBg, func() (string, error) {
			attempts++
			if attempts < 3 {
				return "", errors.New("temporary error")
			}
			return "success", nil
		}, 3, 0)

		test.That(t, err, test.ShouldBeNil)
		test.That(t, result, test.ShouldEqual, "success")
		test.That(t, attempts, test.ShouldEqual, 3)
	})

	t.Run("failure after all retries", func(t *testing.T) {
		attempts := 0
		result, err := RetryNTimesWithSleep(ctxBg, func() (string, error) {
			attempts++
			return "", errors.New("persistent error")
		}, 3, 0)

		test.That(t, err, test.ShouldNotBeNil)
		var retryErr *RetryError
		test.That(t, errors.As(err, &retryErr), test.ShouldBeTrue)
		test.That(t, retryErr.Error(), test.ShouldContainSubstring, "failed after 3 retry attempts")
		test.That(t, retryErr.Unwrap().Error(), test.ShouldEqual, "persistent error")
		test.That(t, result, test.ShouldEqual, "")
		test.That(t, attempts, test.ShouldEqual, 3)
	})

	t.Run("retry only specified errors", func(t *testing.T) {
		retryableErr := errors.New("retryable")
		nonRetryableErr := errors.New("non-retryable")

		attempts := 0
		result, err := RetryNTimesWithSleep(ctxBg, func() (string, error) {
			attempts++
			if attempts == 1 {
				return "", retryableErr
			}
			return "", nonRetryableErr
		}, 3, 0, retryableErr)

		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, errors.Is(err, nonRetryableErr), test.ShouldBeTrue)
		test.That(t, result, test.ShouldEqual, "")
		test.That(t, attempts, test.ShouldEqual, 2)
	})

	t.Run("multiple retryable errors", func(t *testing.T) {
		err1 := errors.New("error1")
		err2 := errors.New("error2")
		nonRetryableErr := errors.New("non-retryable")

		attempts := 0
		result, err := RetryNTimesWithSleep(ctxBg, func() (string, error) {
			attempts++
			switch attempts {
			case 1:
				return "", err1
			case 2:
				return "", err2
			default:
				return "", nonRetryableErr
			}
		}, 3, 0, err1, err2)

		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, errors.Is(err, nonRetryableErr), test.ShouldBeTrue)
		test.That(t, result, test.ShouldEqual, "")
		test.That(t, attempts, test.ShouldEqual, 3)
	})

	t.Run("zero retries", func(t *testing.T) {
		attempts := 0
		result, err := RetryNTimesWithSleep(ctxBg, func() (string, error) {
			attempts++
			return "", errors.New("error")
		}, 0, 0)

		test.That(t, err, test.ShouldNotBeNil)
		var retryErr *RetryError
		test.That(t, errors.As(err, &retryErr), test.ShouldBeTrue)
		test.That(t, retryErr.Error(), test.ShouldContainSubstring, "failed after 0 retry attempts")
		test.That(t, result, test.ShouldEqual, "")
		test.That(t, attempts, test.ShouldEqual, 0)
	})
}
