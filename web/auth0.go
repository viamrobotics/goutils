package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/edaniels/golog"
	"go.opencensus.io/trace"
	"goji.io"
	"goji.io/pat"
	"golang.org/x/oauth2"

	"go.viam.com/utils"
)

// AuthProviderConfig config options with constants that will probably need to be manually configured after
// retrieval from the auth provider web UI or API (e.g. for Auth0, FusionAuth).
type AuthProviderConfig struct {
	Domain     string
	ClientID   string
	Secret     string
	BaseURL    string
	EnableTest bool
}

// AuthProvider should include all state that we need to share with auth callbacks or to make customizations on the
// internals of the specific auth mechanisms we implement for a particular provider.
type AuthProvider struct {
	io.Closer

	config   AuthProviderConfig
	sessions *SessionManager

	authOIConfig  *oidc.Config
	authConfig    oauth2.Config
	httpTransport *http.Transport

	redirectURL string

	// important to have different auth providers have different cookie name so that we force
	// a re-login and throw away old browser state if we migrate auth providers
	stateCookieName   string
	stateCookieMaxAge time.Duration
}

// Close called by io.Closer
func (s *AuthProvider) Close() error {
	s.httpTransport.CloseIdleConnections()
	return nil
}

func (s *AuthProvider) newAuthProvider(ctx context.Context) (*oidc.Provider, error) {
	p, err := oidc.NewProvider(ctx, s.config.Domain)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}
	return p, nil
}

// InstallAuth0 does initial setup and installs routes for auth0.
func InstallAuth0(
	ctx context.Context,
	mux *goji.Mux,
	sessions *SessionManager,
	config AuthProviderConfig,
	logger golog.Logger,
) (io.Closer, error) {
	authProvider, err := installAuthProvider(
		ctx,
		sessions,
		config,
		"/callback",
		"auth0_redirect_state")
	if err != nil {
		return nil, err
	}

	installAuthProviderRoutes(
		mux,
		authProvider,
		// see https://auth0.com/docs/authenticate/login/logout/redirect-users-after-logout
		"/v2/logout",
		authProvider.redirectURL,
		authProvider.stateCookieName,
		authProvider.stateCookieMaxAge,
		logger)

	return authProvider, nil
}

// InstallFusionAuth does initial setup and installs routes for FusionAuth
func InstallFusionAuth(
	ctx context.Context,
	mux *goji.Mux,
	sessions *SessionManager,
	config AuthProviderConfig,
	logger golog.Logger,
) (io.Closer, error) {
	authProvider, err := installAuthProvider(
		ctx,
		sessions,
		config,
		"/callback",
		"fa_redirect_state")
	if err != nil {
		return nil, err
	}

	installAuthProviderRoutes(
		mux,
		authProvider,
		"/logout",
		authProvider.redirectURL,
		authProvider.stateCookieName,
		authProvider.stateCookieMaxAge,
		logger)

	return authProvider, nil
}

func installAuthProvider(
	ctx context.Context,
	sessions *SessionManager,
	config AuthProviderConfig,
	redirectUrl string,
	providerCookieName string,
) (*AuthProvider, error) {
	if config.Domain == "" {
		return nil, errors.New("need a domain for auth provider")
	}

	if config.BaseURL == "" {
		return nil, errors.New("need a base URL for auth provider")
	}

	if sessions == nil {
		return nil, errors.New("sessions needed for auth provider")
	}

	state := &AuthProvider{
		config:            config,
		sessions:          sessions,
		redirectURL:       redirectUrl,
		stateCookieName:   providerCookieName,
		stateCookieMaxAge: time.Minute * 10,
	}

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
	state.httpTransport = &httpTransport

	state.authConfig = oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.Secret,
		RedirectURL:  config.BaseURL + redirectUrl,
		Endpoint:     p.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}

	return state, nil
}

func installAuthProviderRoutes(
	mux *goji.Mux,
	authProvider *AuthProvider,
	providerLogoutURL string,
	redirectURL string,
	redirectStateCookieName string,
	redirectStateCookieMaxAge time.Duration,
	logger golog.Logger) {
	mux.Handle(pat.New("/login"), &loginHandler{
		authProvider,
		logger,
		redirectStateCookieName,
		redirectStateCookieMaxAge})
	mux.Handle(pat.New(redirectURL), &callbackHandler{
		authProvider,
		logger,
		redirectStateCookieName})
	mux.Handle(pat.New("/logout"), &logoutHandler{
		authProvider,
		logger,
		providerLogoutURL,
	})
	mux.Handle(pat.New("/token"), &tokenHandler{
		authProvider,
		logger,
		redirectStateCookieName,
		redirectStateCookieMaxAge})

	if authProvider.config.EnableTest {
		mux.Handle(pat.New("/token-callback"), &tokenCallbackHandler{authProvider, logger})
	}
}

type callbackHandler struct {
	state                   *AuthProvider
	logger                  golog.Logger
	redirectStateCookieName string
}

func (h *callbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ctx, span := trace.StartSpan(ctx, r.URL.Path)
	defer span.End()

	stateCookie, err := r.Cookie(h.redirectStateCookieName)
	if HandleError(w, err, h.logger, "getting redirect cookie") {
		return
	}
	//http.SetCookie(w, &http.Cookie{
	//	Name:     h.redirectStateCookieName,
	//	Value:    "",
	//	Path:     "/",
	//	MaxAge:   -1,
	//	Secure:   r.TLS != nil,
	//	SameSite: http.SameSiteLaxMode,
	//	HttpOnly: true,
	//})
	stateParts := strings.SplitN(stateCookie.Value, ":", 2)
	if len(stateParts) != 2 {
		w.WriteHeader(http.StatusBadRequest)
		_, err := w.Write([]byte("invalid state parameter"))
		utils.UncheckedError(err)
		return
	}

	session, err := h.state.sessions.store.Get(r.Context(), stateParts[0])
	if HandleError(w, err, h.logger, "getting session") {
		return
	}

	if r.URL.Query().Get("state") != stateParts[1] {
		http.Error(w, "Invalid state parameter", http.StatusBadRequest)
		return
	}

	token, err := h.state.authConfig.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		h.logger.Debugw("no token found", "error", err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	session, err = verifyAndSaveToken(ctx, h.state, session, token)
	if HandleError(w, err, h.logger) {
		return
	}

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

	_, err = w.Write([]byte(fmt.Sprintf(`<html>
<head>
<meta http-equiv="refresh" content="0;URL='%s'"/>
</head>
</html>
`, backto)))
	utils.UncheckedError(err)
}

// Handle programmatically generated access + id tokens
// Currently used only in testing.
type tokenCallbackHandler struct {
	state  *AuthProvider
	logger golog.Logger
}

func (h *tokenCallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ctx, span := trace.StartSpan(ctx, r.URL.Path)
	defer span.End()

	session, err := h.state.sessions.Get(r, true)
	if HandleError(w, err, h.logger, "getting session") {
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
	if err := json.Unmarshal(bodyBytes, &jsonToken); HandleError(w, err, h.logger, "reading token") {
		return
	}

	token := &oauth2.Token{}
	if err := json.Unmarshal(bodyBytes, &token); HandleError(w, err, h.logger, "reading token") {
		return
	}

	if e, ok := jsonToken["expires_in"].(float64); !ok {
		HandleError(w, errors.New("could not determine token expiry"), h.logger, "reading token")
		return
	} else if e != 0 {
		token.Expiry = time.Now().Add(time.Duration(e) * time.Second)
	}

	token = token.WithExtra(jsonToken)

	session, err = verifyAndSaveToken(ctx, h.state, session, token)
	if HandleError(w, err, h.logger) {
		return
	}

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

func verifyAndSaveToken(ctx context.Context, state *AuthProvider, session *Session, token *oauth2.Token) (*Session, error) {
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("no id_token field in oauth2 token")
	}

	p, err := state.newAuthProvider(ctx)
	if err != nil {
		return nil, err
	}

	idToken, err := p.Verifier(state.authOIConfig).Verify(ctx, rawIDToken)
	if err != nil {
		return nil, errors.New("failed to verify ID Token: " + err.Error())
	}

	// Getting now the userInfo
	var profile map[string]interface{}
	if err := idToken.Claims(&profile); err != nil {
		return nil, err
	}

	session.Data["id_token"] = rawIDToken
	session.Data["access_token"] = token.AccessToken
	session.Data["profile"] = profile

	return session, nil
}

// --------------------------------
type tokenHandler struct {
	state                     *AuthProvider
	logger                    golog.Logger
	redirectStateCookieName   string
	redirectStateCookieMaxAge time.Duration
}

func (h *tokenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ctx, span := trace.StartSpan(ctx, r.URL.Path)
	defer span.End()

	stateCookie, err := r.Cookie(h.redirectStateCookieName)
	if HandleError(w, err, h.logger, "getting redirect cookie") {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     h.redirectStateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
	})
	stateParts := strings.SplitN(stateCookie.Value, ":", 2)
	if len(stateParts) != 2 {
		w.WriteHeader(http.StatusBadRequest)
		_, err := w.Write([]byte("invalid state parameter"))
		utils.UncheckedError(err)
		return
	}

	session, err := h.state.sessions.store.Get(r.Context(), stateParts[0])
	if HandleError(w, err, h.logger, "getting session") {
		return
	}

	//token, err := h.state.authConfig.Exchange(ctx, r.URL.Query().Get("code"))
	//if err != nil {
	//	h.logger.Debugw("no token found", "error", err)
	//	w.WriteHeader(http.StatusUnauthorized)
	//	return
	//}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write([]byte(session.Data["access_token"].(string)))
	utils.UncheckedError(err)
}

// --------------------------------

type loginHandler struct {
	state                     *AuthProvider
	logger                    golog.Logger
	redirectStateCookieName   string
	redirectStateCookieMaxAge time.Duration
}

func (h *loginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ctx, span := trace.StartSpan(ctx, r.URL.Path)
	defer span.End()

	// Generate random state
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if HandleError(w, err, h.logger, "error getting random number") {
		return
	}
	state := base64.StdEncoding.EncodeToString(b)

	session, err := h.state.sessions.Get(r, true)
	if HandleError(w, err, h.logger, "error getting session") {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     h.redirectStateCookieName,
		Value:    fmt.Sprintf("%s:%s", session.id, state),
		Path:     "/",
		MaxAge:   int(h.redirectStateCookieMaxAge.Seconds()),
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
	})

	if r.FormValue("backto") != "" {
		session.Data["backto"] = r.FormValue("backto")
	}
	if session.Data["backto"] == "" {
		session.Data["backto"] = r.Header.Get("Referer")
	}
	err = session.Save(ctx, r, w)
	if HandleError(w, err, h.logger, "error saving session") {
		return
	}

	http.Redirect(w, r, h.state.authConfig.AuthCodeURL(state), http.StatusTemporaryRedirect)
}

// --------------------------------

type logoutHandler struct {
	state             *AuthProvider
	logger            golog.Logger
	providerLogoutUrl string
}

func (h *logoutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ctx, span := trace.StartSpan(ctx, r.URL.Path)
	defer span.End()

	logoutURL, err := url.Parse(h.state.config.Domain)
	if HandleError(w, err, h.logger, "internal config error parsing domain") {
		return
	}

	logoutURL.Path = h.providerLogoutUrl
	parameters := url.Values{}

	parameters.Add("returnTo", h.state.config.BaseURL)
	parameters.Add("client_id", h.state.config.ClientID)
	logoutURL.RawQuery = parameters.Encode()

	h.state.sessions.DeleteSession(ctx, r, w)
	http.Redirect(w, r, logoutURL.String(), http.StatusTemporaryRedirect)
}
