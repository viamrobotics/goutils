package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"go.opencensus.io/trace"

	"go.viam.com/utils"
)

// APIHandler what a user has to implement to use APIMiddleware
type APIHandler interface {
	// return (result, error)
	// if both are null, do nothing
	ServeAPI(w http.ResponseWriter, r *http.Request) (interface{}, error)
}

// APIMiddleware simple layer between http.Handler interface that does json marshalling and error handling
type APIMiddleware struct {
	Handler APIHandler
}

func handleAPIError(w http.ResponseWriter, err error, extra interface{}) bool {
	if err == nil {
		return false
	}

	log.Printf("api error: %s %s\n", err, extra)

	data := map[string]interface{}{"err": err.Error()}
	if extra != nil {
		data["extra"] = extra
	}

	js, err := json.Marshal(data)
	if err != nil {
		temp := fmt.Sprintf("err not able to be converted to json (%s) (%s)", data, err)
		w.WriteHeader(500)
		_, err = w.Write([]byte(temp))
		if err != nil {
			// hack for linter
			return true
		}
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		_, err = w.Write(js)
		if err != nil {
			// hack for linter
			return true
		}
	}

	return true
}

// ServeHTTP call the api
func (am *APIMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	ctx, span := trace.StartSpan(ctx, r.URL.Path)
	defer span.End()

	r = r.WithContext(ctx)

	data, err := am.Handler.ServeAPI(w, r)
	if handleAPIError(w, err, data) {
		return
	}

	if data == nil {
		return
	}

	js, err := json.Marshal(data)
	if handleAPIError(w, err, nil) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(js)
	utils.UncheckedError(err)
}
