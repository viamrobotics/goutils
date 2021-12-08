package web

import (
	"bytes"
	"errors"
	"log"
	"net/http"

	"go.viam.com/utils"
)

// ErrorResponse lets you specify a status code
type ErrorResponse interface {
	Error() string
	Status() int
}

// ErrorResponseStatus creates an error response with a specific code
func ErrorResponseStatus(code int) ErrorResponse {
	return errorResponseStatus(code)
}

type errorResponseStatus int

func (s errorResponseStatus) Error() string {
	return http.StatusText(int(s))
}

func (s errorResponseStatus) Status() int {
	return int(s)
}

// HandleError returns true if there was an error and you should stop
func HandleError(w http.ResponseWriter, err error, context ...string) bool {
	if err == nil {
		return false
	}

	log.Println(err)

	var er ErrorResponse
	if errors.As(err, &er) {
		w.WriteHeader(er.Status())
	} else {
		w.WriteHeader(500)
	}

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
