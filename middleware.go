package openidauth

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/emanoelxavier/openid2go/openid"
	"github.com/mholt/caddy/caddyhttp/httpserver"
)

// A function literal that fulfils the requirement of openId.PrivdersGetter
// It is used to sert up a new provider with the issuer and client ids from
// the configuration.
func getProviderFunc(issuer string, clientIds []string) openid.GetProvidersFunc {
	return func() ([]openid.Provider, error) {
		provider, err := openid.NewProvider(issuer, clientIds)
		if err != nil {
			return nil, err
		}
		return []openid.Provider{provider}, nil
	}
}

// This struct fulfils the http.Handler interface that the openid.Authenticate
// function uses. It will be used to store the authenticate result
// so that we can read it back in this middleware and make decisions
// based on it.
type authenticationSuccessHandler struct {
	Authenticated bool
}

// After successful validation of a token this handler will be called
func (t *authenticationSuccessHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.Authenticated = true
}

// This error handler allows us to customize the response
func onAuthenticateFailed(e error, rw http.ResponseWriter, r *http.Request) bool {
	if verr, ok := e.(*openid.ValidationError); ok {
		httpStatus := verr.HTTPStatus

		switch verr.Code {
		case openid.ValidationErrorGetOpenIdConfigurationFailure:
			httpStatus = http.StatusServiceUnavailable
		case openid.ValidationErrorAuthorizationHeaderNotFound:
			// Instead of responding with 400 Bad Response we want to say 401 Unauthorized
			// and indicate that this resource is protected and that you can authenticate
			// using a Bearer token. 400 Bad response was set in the validation error from
			// the underlaying openid code.
			httpStatus = http.StatusUnauthorized
			rw.Header().Add("WWW-Authenticate", "Bearer")
		}
		http.Error(rw, verr.Message, httpStatus)
	} else {
		// Not supposed to happen, but if it does we will have some information to go on.
		rw.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(rw, e.Error())
	}

	// We have handled the error, so return true to halt the execution so that
	// the next handler is not going to be called.
	return /*halt=*/ true
}

// ServeHTTP is the main entry point for the middleware during execution.
func (h auth) ServeHTTP(w http.ResponseWriter, r *http.Request) (int, error) {

	// To support having the token as a query parameter we extract it here and
	// insert it as an Authorization header so that the underlaying code
	// (which only can use the Authorization header) works.
	// Note that tokens supplied via form data in the request body is NOT supported.
	// According to the OpenID spec this MAY be implemented, but would require buffering the
	// full request body to be able to both read it here and forward it to the backend.
	token := r.URL.Query().Get("access_token")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}

	// If the requested path matches a path in the configuration, validate the JWT
	for _, p := range h.Paths {
		if !httpserver.Path(r.URL.Path).Matches(p) {
			continue
		}

		// Path matches. Authenticate
		authHandler := authenticationSuccessHandler{false}
		openid.Authenticate(h.Configuration, &authHandler).ServeHTTP(w, r)
		if !authHandler.Authenticated {
			// The success handler was not called, so it failed.
			// We return 0 to indicate that the response has already been written.
			return 0, errors.New("Token verification failed")
		}
		// Authenticated so call next middleware
		return h.Next.ServeHTTP(w, r)
	}

	// pass request if no paths protected with JWT or the code above falls through
	return h.Next.ServeHTTP(w, r)
}
