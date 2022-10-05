package web

import (
	"bytes"
	"errors"
	"net/http"

	"github.com/edaniels/golog"

	"go.viam.com/utils"
)

// ErrorResponse lets you specify a status code.
type ErrorResponse interface {
	Error() string
	Status() int
}

// ErrorResponseStatus creates an error response with a specific code.
func ErrorResponseStatus(code int) ErrorResponse {
	return responseStatusError(code)
}

type responseStatusError int

func (s responseStatusError) Error() string {
	return http.StatusText(int(s))
}

func (s responseStatusError) Status() int {
	return int(s)
}

// HandleError returns true if there was an error and you should stop.
func HandleError(w http.ResponseWriter, err error, logger golog.Logger, context ...string) bool {
	if err == nil {
		return false
	}

	logger.Info(err)

	statusCode := http.StatusInternalServerError

	var er ErrorResponse
	if errors.As(err, &er) {
		statusCode = er.Status()
	}

	// Log internal errors.
	if statusCode >= 500 {
		logger.Errorf("Error during http response: %s", err)
	}

	w.WriteHeader(statusCode)

	var b bytes.Buffer

	for _, x := range context {
		b.WriteString(x)
		b.WriteByte('\n')
	}
	b.WriteString(err.Error())
	b.WriteByte('\n')

	_, err = b.WriteTo(w)
	utils.UncheckedError(err)
	return true
}
