package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"go.opencensus.io/trace"
	"goji.io"
	"goji.io/pat"
	"golang.org/x/oauth2"

	"go.viam.com/utils"
)

// Auth0Config config for auth0
type Auth0Config struct {
	Domain     string
	ClientID   string
	Secret     string
	WebRoot    string
	EnableTest bool
}

type auth0State struct {
	config   Auth0Config
	sessions *SessionManager

	authOIConfig       *oidc.Config
	authConfig         oauth2.Config
	auth0HTTPTransport *http.Transport
}

func (s *auth0State) Close() error {
	s.auth0HTTPTransport.CloseIdleConnections()
	return nil
}

func (s *auth0State) newAuthProvider(ctx context.Context) (*oidc.Provider, error) {
	p, err := oidc.NewProvider(ctx, s.config.Domain)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}
	return p, nil
}

// InstallAuth0 does initial setup and installs routes for auth0
func InstallAuth0(ctx context.Context, mux *goji.Mux, sessions *SessionManager, config Auth0Config) (io.Closer, error) {
	if config.Domain == "" {
		return nil, errors.New("need a domain for auth0")
	}

	if config.WebRoot == "" {
		return nil, errors.New("need a WebRoot for auth0")
	}

	if sessions == nil {
		return nil, errors.New("sessions needed for auth0")
	}

	state := &auth0State{
		config:   config,
		sessions: sessions,
	}

	// init auth
	state.authOIConfig = &oidc.Config{
		ClientID: config.ClientID,
	}

	var httpTransport http.Transport
	ctx = oidc.ClientContext(ctx, &http.Client{Transport: &httpTransport})

	p, err := state.newAuthProvider(ctx)
	if err != nil {
		httpTransport.CloseIdleConnections()
		return nil, err
	}
	state.auth0HTTPTransport = &httpTransport

	state.authConfig = oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.Secret,
		RedirectURL:  config.WebRoot + "/callback",
		Endpoint:     p.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}

	mux.Handle(pat.New("/callback"), &callbackHandler{state})
	mux.Handle(pat.New("/login"), &loginHandler{state})
	mux.Handle(pat.New("/logout"), &logoutHandler{state})

	if state.config.EnableTest {
		mux.Handle(pat.New("/token-callback"), &tokenCallbackHandler{state})
	}

	return state, nil
}

type callbackHandler struct {
	state *auth0State
}

func (h *callbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ctx, span := trace.StartSpan(ctx, r.URL.Path)
	defer span.End()

	session, err := h.state.sessions.Get(r, true)
	if HandleError(w, err, "getting session") {
		return
	}

	if r.URL.Query().Get("state") != session.Data["state"] {
		http.Error(w, "Invalid state parameter", http.StatusBadRequest)
		return
	}

	token, err := h.state.authConfig.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		log.Printf("no token found: %v", err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "No id_token field in oauth2 token.", http.StatusInternalServerError)
		return
	}

	p, err := h.state.newAuthProvider(ctx)
	if HandleError(w, err) {
		return
	}

	idToken, err := p.Verifier(h.state.authOIConfig).Verify(ctx, rawIDToken)
	if err != nil {
		http.Error(w, "Failed to verify ID Token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Getting now the userInfo
	var profile map[string]interface{}
	if err := idToken.Claims(&profile); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	session.Data["id_token"] = rawIDToken
	session.Data["access_token"] = token.AccessToken
	session.Data["profile"] = profile

	backto, _ := session.Data["backto"].(string)
	if len(backto) == 0 {
		backto = "/"
	}

	session.Data["backto"] = ""
	err = session.Save(ctx, r, w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, backto, http.StatusSeeOther)

}

// Handle programmatically generated access + id tokens
// Currently used only in testing
type tokenCallbackHandler struct {
	state *auth0State
}

func (h *tokenCallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ctx, span := trace.StartSpan(ctx, r.URL.Path)
	defer span.End()

	session, err := h.state.sessions.Get(r, true)
	if HandleError(w, err, "getting session") {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, errors.New("method not allowed").Error(), http.StatusMethodNotAllowed)
		return
	}

	defer utils.UncheckedErrorFunc(r.Body.Close)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, errors.New("unable to read message body").Error(), http.StatusBadRequest)
		return
	}

	jsonToken := map[string]interface{}{}
	if err := json.Unmarshal(bodyBytes, &jsonToken); HandleError(w, err, "reading token") {
		return
	}

	token := &oauth2.Token{}
	if err := json.Unmarshal(bodyBytes, &token); HandleError(w, err, "reading token") {
		return
	}

	if e, ok := jsonToken["expires_in"].(float64); !ok {
		HandleError(w, errors.New("could not determine token expiry"), "reading token")
		return
	} else if e != 0 {
		token.Expiry = time.Now().Add(time.Duration(e) * time.Second)
	}

	token = token.WithExtra(jsonToken)

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "No id_token field in oauth2 token.", http.StatusInternalServerError)
		return
	}

	p, err := h.state.newAuthProvider(ctx)
	if HandleError(w, err) {
		return
	}

	idToken, err := p.Verifier(h.state.authOIConfig).Verify(ctx, rawIDToken)
	if err != nil {
		http.Error(w, "Failed to verify ID Token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Getting now the userInfo
	var profile map[string]interface{}
	if err := idToken.Claims(&profile); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	session.Data["id_token"] = rawIDToken
	session.Data["access_token"] = token.AccessToken
	session.Data["profile"] = profile

	backto, _ := session.Data["backto"].(string)
	if len(backto) == 0 {
		backto = "/"
	}

	session.Data["backto"] = ""
	err = session.Save(ctx, r, w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, backto, http.StatusSeeOther)

}

// --------------------------------

type loginHandler struct {
	state *auth0State
}

func (h *loginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ctx, span := trace.StartSpan(ctx, r.URL.Path)
	defer span.End()

	// Generate random state
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if HandleError(w, err, "error getting random number") {
		return
	}
	state := base64.StdEncoding.EncodeToString(b)

	session, err := h.state.sessions.Get(r, true)
	if HandleError(w, err, "error getting session") {
		return
	}
	session.Data["state"] = state

	if r.FormValue("backto") != "" {
		session.Data["backto"] = r.FormValue("backto")
	}
	if session.Data["backto"] == "" {
		session.Data["backto"] = r.Header.Get("Referer")
	}
	err = session.Save(ctx, r, w)
	if HandleError(w, err, "error saving session") {
		return
	}

	http.Redirect(w, r, h.state.authConfig.AuthCodeURL(state), http.StatusTemporaryRedirect)
}

// --------------------------------

type logoutHandler struct {
	state *auth0State
}

func (h *logoutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ctx, span := trace.StartSpan(ctx, r.URL.Path)
	defer span.End()

	logoutURL, err := url.Parse(h.state.config.Domain)
	if HandleError(w, err, "internal config error parsing domain") {
		return
	}

	logoutURL.Path = "/v2/logout"
	parameters := url.Values{}

	parameters.Add("returnTo", h.state.config.WebRoot)
	parameters.Add("client_id", h.state.config.ClientID)
	logoutURL.RawQuery = parameters.Encode()

	h.state.sessions.DeleteSession(ctx, r, w)
	http.Redirect(w, r, logoutURL.String(), http.StatusTemporaryRedirect)
}
