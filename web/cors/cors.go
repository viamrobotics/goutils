// Package cors wraps the cors package with needed defaults.
package cors

import (
	"net/http"
	"time"

	"github.com/rs/cors"
)

// Cors http handler.
type Cors = cors.Cors

var (
	// Allow all http methods by default.
	defaultAllowedMethods = []string{
		http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodHead,
	}

	defaultExposedHeaders = []string{
		"Grpc-Encoding", // grpc-web
		"Grpc-Message",  // grpc-web
		"Grpc-Status",   // grpc-web
	}

	defaultCacheAge = time.Second * 3600
)

// AllowAll returns CORs handler configured for our public APIs.
func AllowAll() *Cors {
	return cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: defaultAllowedMethods,
		// allow all headers
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   defaultExposedHeaders,
		AllowCredentials: false,
		MaxAge:           int(defaultCacheAge.Seconds()),
	})
}
