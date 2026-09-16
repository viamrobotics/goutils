package rpc

import (
	"testing"

	"go.viam.com/test"
)

func TestRedactedCallerAuthMetadata(t *testing.T) {
	t.Run("strips secret credential fields, keeps identity", func(t *testing.T) {
		orig := map[string]string{
			"key":         "hqcnrl3uc194yhjqkwilx5j7yxxcv3jb", // plaintext API key — must be dropped
			"key_id":      "16b590ba-9e46-4ace-8e86-70157e6d507d",
			"app_user_id": "user-123",
			"email":       "benji@viam.com",
		}
		got := redactedCallerAuthMetadata(orig)

		test.That(t, got, test.ShouldResemble, map[string]string{
			"key_id":      "16b590ba-9e46-4ace-8e86-70157e6d507d",
			"app_user_id": "user-123",
			"email":       "benji@viam.com",
		})
		_, hasKey := got["key"]
		test.That(t, hasKey, test.ShouldBeFalse)

		// the caller's map (shared with the request auth entity) must be untouched.
		_, origStillHasKey := orig["key"]
		test.That(t, origStillHasKey, test.ShouldBeTrue)
	})

	t.Run("no sensitive fields is a faithful copy", func(t *testing.T) {
		orig := map[string]string{"key_id": "abc", "email": "x@y.z"}
		got := redactedCallerAuthMetadata(orig)
		test.That(t, got, test.ShouldResemble, orig)
	})

	t.Run("nil and empty pass through", func(t *testing.T) {
		test.That(t, redactedCallerAuthMetadata(nil), test.ShouldBeNil)
		test.That(t, redactedCallerAuthMetadata(map[string]string{}), test.ShouldResemble, map[string]string{})
	})
}
