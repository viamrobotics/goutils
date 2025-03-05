package utils

import (
	"errors"
	"fmt"
	"slices"
)

// RetryError is emitted by RetryNTimes if all the attempts fail. It unwraps to the last error from retrying.
type RetryError struct {
	inner    error
	attempts int
}

func (e *RetryError) Error() string {
	return fmt.Sprintf("failed after %d retry attempts: %v", e.attempts, e.inner)
}

func (e *RetryError) Unwrap() error {
	return e.inner
}

// RetryNTimes will run `fallibleFunc` `retryAttempts` times before failing with the last error it got from the function.
// If `retryableErrors` is supplied, only those errors will be retried.
func RetryNTimes[T any](fallibleFunc func() (T, error), retryAttempts int, retryableErrors ...error) (T, error) {
	var lastError error

	for numRetries := 0; numRetries < retryAttempts; numRetries++ {
		val, err := fallibleFunc()
		if err == nil || (len(retryableErrors) != 0 &&
			!slices.ContainsFunc(retryableErrors, func(target error) bool { return errors.Is(err, target) })) {
			return val, err
		}
		lastError = err
	}

	var emptyT T
	return emptyT, &RetryError{attempts: retryAttempts, inner: lastError}
}
