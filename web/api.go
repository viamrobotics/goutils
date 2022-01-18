// Package web contains utilities to help build out a web service.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/edaniels/golog"
	"go.opencensus.io/trace"

	"go.viam.com/utils"
)

// APIHandler what a user has to implement to use APIMiddleware.
type APIHandler interface {
	// return (result, error)
	// if both are null, do nothing
	ServeAPI(w http.ResponseWriter, r *http.Request) (interface{}, error)
}

// APIMiddleware simple layer between http.Handler interface that does json marshalling and error handling.
type APIMiddleware struct {
	Handler APIHandler
	Logger  golog.Logger
}

func handleAPIError(w http.ResponseWriter, err error, logger golog.Logger, extra interface{}) bool {
	if err == nil {
		return false
	}

	logger.Debugw("api issue", "error", err, "extra", extra)

	data := map[string]interface{}{"err": err.Error()}
	if extra != nil {
		data["extra"] = extra
	}

	js, marshalErr := json.Marshal(data)
	if marshalErr != nil {
		temp := fmt.Sprintf("err not able to be converted to json (%s) (%s)", data, err)
		w.WriteHeader(500)
		_, err = w.Write([]byte(temp))
		if err != nil {
			// hack for linter
			return true
		}
	} else {
		w.Header().Set("Content-Type", "application/json")
		var er ErrorResponse
		if errors.As(err, &er) {
			w.WriteHeader(er.Status())
		} else {
			w.WriteHeader(500)
		}
		_, err = w.Write(js)
		if err != nil {
			// hack for linter
			return true
		}
	}

	return true
}

// ServeHTTP call the api.
func (am *APIMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	ctx, span := trace.StartSpan(ctx, r.URL.Path)
	defer span.End()

	r = r.WithContext(ctx)

	data, err := am.Handler.ServeAPI(w, r)
	if handleAPIError(w, err, am.Logger, data) {
		return
	}

	if data == nil {
		return
	}

	js, err := json.Marshal(data)
	if handleAPIError(w, err, am.Logger, nil) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(js)
	utils.UncheckedError(err)
}
