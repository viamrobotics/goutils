package web

import (
	"net/http"

	"go.viam.com/utils"
)

// PanicCapture allows recovery during a request handler from panics. It prints a
// formatted log to the underlying logger.
type PanicCapture struct {
	Logger utils.ZapCompatibleLogger
}

// Recover captures and prints the error if recover() has an error.
func (p *PanicCapture) Recover(w http.ResponseWriter, r *http.Request) {
	err := recover()
	if err == nil {
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusInternalServerError)
	if _, err := w.Write([]byte("internal server error")); err != nil {
		p.Logger.Warnf("failed to write to response: %s", err)
	}

	p.Logger.Errorf("Unhandled error: %s", err)
}
