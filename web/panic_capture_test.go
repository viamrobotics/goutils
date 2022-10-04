package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/edaniels/golog"
	"go.viam.com/test"
)

func TestPanicCapture(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	logger, observedLogs := golog.NewObservedTestLogger(t)
	capture := PanicCapture{Logger: logger}

	handlerWithError := func(err error, w http.ResponseWriter) {
		defer capture.Recover(w, req)
		panic(err)
	}

	w := httptest.NewRecorder()
	handlerWithError(errors.New("some error"), w)

	test.That(t, w.Code, test.ShouldEqual, http.StatusInternalServerError)
	test.That(t, w.Body.String(), test.ShouldEqual, "internal server error")
	test.That(t, observedLogs.All(), test.ShouldHaveLength, 1)
	test.That(t, observedLogs.All()[0].Message, test.ShouldContainSubstring, "Unhandled error: some error")
}
