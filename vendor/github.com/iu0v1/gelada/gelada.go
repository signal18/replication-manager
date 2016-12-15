// Package gelada provides a tool for HTTP session authentication control (via cookie).
//
// Gelada use a part of great Gorilla web toolkit, 'gorilla/sessions' package
// (refer to http://github.com/gorilla/sessions for more information).
package gelada

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/sessions"
)

// AuthProviderType - AuthProvider type
type AuthProviderType func(user, password string) bool

// Gelada - main struct.
type Gelada struct {
	options    *Options
	store      *sessions.CookieStore
	exceptions []*exception
}

// exception is used to hold objects from the ExceptionList
type exception struct {
	Rule    *regexp.Regexp
	RawRule string
}

// Options - structure, which is used to configure Gelada.
type Options struct {
	// http.Cookie options
	// Please, look at http://golang.org/pkg/net/http/#Cookie
	Path     string
	Domain   string
	MaxAge   int
	Secure   bool
	HTTPOnly bool

	// Cookie session name.
	// Default: "gelada-session"
	SessionName string

	// Duration of session. In seconds.
	// Default: 86400 (24 hours)
	SessionLifeTime int

	// Authentication and encryption keys. This is required for encoding and
	// decoding authenticated and optionally encrypted cookie values.
	//
	// Recommended to use a key with 32 or 64 bytes, and block key
	// length must correspond to the block size of the encryption algorithm.
	// For AES, used by default, valid lengths are 16, 24, or 32 bytes to select
	// AES-128, AES-192, or AES-256.
	//
	// For more information, please refer to http://www.gorillatoolkit.org/pkg/securecookie
	//
	// Default: 261AD9502C583BD7D8AA03083598653B, E9F6FDFAC2772D33FC5C7B3D6E4DDAFF
	// But use the default key only for testing. It's not secure.
	SessionKeys [][]byte

	// Assign a user's session with his browser user agent value.
	// Default: false
	BindUserAgent bool

	// Assign a user's session with his host value (IP address).
	// Default: false
	BindUserHost bool

	// Path to login handler, for redirect the client to authentication page.
	LoginRoute string

	// HTML field names, to retrieve 'user' and 'password' data from login form.
	// Deafult: "login" and "password"
	LoginUserFieldName     string
	LoginPasswordFieldName string

	// Path for redirect a client after authentication.
	// If option does not set - clients will be redirected to URL's, which
	// they tried to open before the authentication.
	PostLoginRoute string

	// Evil twin brother of LoginRoute. He ends the client session.
	LogoutRoute string

	// Similarly to PostLoginRoute.
	PostLogoutRoute string

	// Gelada can use an existing Gorilla session (CookieStore).
	// If GorillaCookieStore was set - SessionKeys will be ignored.
	GorillaCookieStore *sessions.CookieStore

	// AuthProvider provide opportunity to handle auth data.
	// It's take a login and password data, check it,
	// and return 'true' on success and 'false' on fail.
	AuthProvider AuthProviderType

	// Exceptions is a list of rules to be able to create exceptions for some
	// auth-free routes.
	//
	// Example. We set GlobalAuth on whole project. But we want provide some
	// zone without auth (all /noauth/... for example). Then we add "/noauth/.*"
	// to Exceptions. Bingo! All places will require authorization, except pages
	// on /noauth/... .
	Exceptions []string

	// AuthGuard is a tool for handle and processing login attempts.
	AuthGuard AuthGuard

	// UnauthorizedHeaderName - heder which will be sent to the client if the
	// user is not authorized.
	// Sends only if it was selected.
	UnauthorizedHeaderName string
}

// AuthGuard - interface for options.AuthGuard fuction.
type AuthGuard interface {
	Check(username string, req *http.Request) bool
	Complaint(username string, req *http.Request)
}

// Client contain info about the current user session
// and provide some helper methods.
type Client struct {
	Username   string
	UserAgent  string
	UserHost   string
	LoginDate  time.Time
	ExpireDate time.Time

	gelada  *Gelada
	session *sessions.Session
}

// TimeToEndOfSession returns the amount of time (seconds) left before the end of
// the current user session.
func (c *Client) TimeToEndOfSession() int {
	t := c.ExpireDate.Sub(time.Now().Local()).Seconds()
	if t <= 0 {
		return 0
	}
	return int(t)
}

// Logout - ends the user's session. Ignore a PostLogoutRoute option and does not
// redirect after session end.
func (c *Client) Logout(res http.ResponseWriter, req *http.Request) error {
	c.session.Options.MaxAge = -1
	if err := c.session.Save(req, res); err != nil {
		return fmt.Errorf("user logout error: %v\n", err)
	}
	return nil
}

// Expire returns state of current user session.
// 'true' if session is expired, and 'false' if the session has not expired.
func (c *Client) Expire() bool {
	t := c.ExpireDate.Sub(time.Now().Local()).Seconds()
	if t <= 0 {
		return true
	}
	return false
}

// New - init and return new Gelada struct.
func New(o Options) (*Gelada, error) {
	g := &Gelada{options: &o}

	// check mandatory options
	if g.options.Path == "" {
		g.options.Path = "/"
	}

	if g.options.SessionName == "" {
		g.options.SessionName = "gelada-session"
	}

	if g.options.SessionLifeTime == 0 {
		g.options.SessionLifeTime = 86400
	}

	if g.options.GorillaCookieStore == nil {
		if len(g.options.SessionKeys) == 0 { // create default store
			g.store = sessions.NewCookieStore(
				[]byte("261AD9502C583BD7D8AA03083598653B"),
				[]byte("E9F6FDFAC2772D33FC5C7B3D6E4DDAFF"),
			)
		} else { // use user keys
			g.store = sessions.NewCookieStore(g.options.SessionKeys...)
		}
	} else {
		g.store = g.options.GorillaCookieStore
	}

	if g.options.LoginRoute == "" {
		return nil, errors.New("LoginRoute not declared")
	}

	if g.options.LoginUserFieldName == "" {
		g.options.LoginUserFieldName = "login"
	}

	if g.options.LoginPasswordFieldName == "" {
		g.options.LoginPasswordFieldName = "password"
	}

	if g.options.PostLogoutRoute == "" {
		g.options.PostLogoutRoute = "/"
	}

	// if g.options.LogoutRoute == "" {
	// 	return nil, errors.New("LogoutRoute not declared")
	// }

	if g.options.AuthProvider == nil {
		return nil, errors.New("AuthProvider not declared")
	}

	if len(g.options.Exceptions) > 0 {
		noAuthRules := []*exception{}
		for i, rule := range g.options.Exceptions {
			exceptionObject := &exception{
				RawRule: rule,
			}

			if len(exceptionObject.RawRule) == 0 {
				e := fmt.Errorf("error in exception rule (%d): empty rule", i)
				return nil, e
			}

			expr, err := regexp.Compile(exceptionObject.RawRule)
			if err != nil {
				e := fmt.Errorf("error in exception rule (%d: %s): %v\n",
					i, exceptionObject.RawRule, err,
				)
				return nil, e
			}

			exceptionObject.Rule = expr
			noAuthRules = append(noAuthRules, exceptionObject)

		}
		g.exceptions = noAuthRules
	}

	g.store.Options = &sessions.Options{
		Path:     g.options.Path,
		Domain:   g.options.Domain,
		MaxAge:   g.options.MaxAge,
		Secure:   g.options.Secure,
		HttpOnly: g.options.HTTPOnly,
	}

	return g, nil
}

func (g *Gelada) checkAuth(res http.ResponseWriter, req *http.Request) bool {
	session, err := g.store.Get(req, g.options.SessionName)
	if err != nil {
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return false
	}

	loginRedirect := func() {
		var redirectURL string

		ru, ok := session.Values["postLoginRedirect"]
		if ok {
			redirectURL = ru.(string)
		} else {
			redirectURL = req.URL.String()
		}

		if g.options.UnauthorizedHeaderName != "" {
			res.Header().Set(g.options.UnauthorizedHeaderName, "unauthorized")
		}

		session.Values["postLoginRedirect"] = redirectURL
		if err := session.Save(req, res); err != nil {
			http.Error(res, err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(res, req, g.options.LoginRoute, http.StatusFound)
	}

	currentTime := time.Now().Local()

	userExpireTimeRaw, ok := session.Values["expireTime"]
	if !ok {
		loginRedirect()
		return false
	}

	userExpireTime, err := time.Parse(time.RFC3339, userExpireTimeRaw.(string))
	if err != nil {
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return false
	}

	if userExpireTime.Before(currentTime) {
		loginRedirect()
		return false
	}

	if g.options.BindUserAgent {
		val, ok := session.Values["useragent"]
		if !ok {
			loginRedirect()
			return false
		}

		if req.UserAgent() != val.(string) {
			loginRedirect()
			return false
		}
	}

	if g.options.BindUserHost {
		val, ok := session.Values["userHost"]
		if !ok {
			loginRedirect()
			return false
		}

		if strings.Split(req.RemoteAddr, ":")[0] != val.(string) {
			loginRedirect()
			return false
		}
	}

	return true
}

// noAuthExeption check exeptions
func (g *Gelada) noAuthExeption(req *http.Request) bool {
	for _, rule := range g.exceptions {
		if rule.Rule.MatchString(req.URL.String()) {
			return true
		}
	}
	return false
}

// GlobalAuth provides the opportunity to wrap all requests for auth control.
//
// Example.
//    g, _ := gelada.New(options)
//    mux := http.NewServeMux()
//    mux.HandleFunc("/api/", apiHandler)
//
//    http.Handle("/", g.GlobalAuth(mux)) // wrap all requests
func (g *Gelada) GlobalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		if req.URL.Path == g.options.LoginRoute || g.noAuthExeption(req) {
			if g.options.UnauthorizedHeaderName != "" {
				res.Header().Set(g.options.UnauthorizedHeaderName, "unauthorized")
			}
			next.ServeHTTP(res, req)
			return
		}

		if g.checkAuth(res, req) {
			next.ServeHTTP(res, req)
		}
	})
}

// Auth provides the ability to control authorization for the individual handlers.
//
// Example.
//    g, _ := gelada.New(options)
//    mux := http.NewServeMux()
//    mux.HandleFunc("/api/", g.Auth(apiHandler)) // auth control only for this handler
//    mux.HandleFunc("/main", mainHandler)
//
//    http.Handle("/", mux)
func (g *Gelada) Auth(f http.HandlerFunc) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
		if req.URL.Path == g.options.LoginRoute || g.noAuthExeption(req) {
			if g.options.UnauthorizedHeaderName != "" {
				res.Header().Set(g.options.UnauthorizedHeaderName, "unauthorized")
			}
			f.ServeHTTP(res, req)
			return
		}

		if g.checkAuth(res, req) {
			f.ServeHTTP(res, req)
		}
	}
}

// AuthHandler is a handler for processing a request for authorization.
func (g *Gelada) AuthHandler(res http.ResponseWriter, req *http.Request) {
	user := req.FormValue(g.options.LoginUserFieldName)
	password := req.FormValue(g.options.LoginPasswordFieldName)

	if g.options.AuthGuard != nil {
		if !g.options.AuthGuard.Check(user, req) {
			http.Redirect(res, req, g.options.LoginRoute, http.StatusFound)
			return
		}
	}

	auth := g.options.AuthProvider(user, password)
	if auth {
		session, err := g.store.Get(req, g.options.SessionName)
		if err != nil {
			http.Error(res, err.Error(), http.StatusInternalServerError)
			return
		}

		currentTime := time.Now().Local()

		session.Values["user"] = user
		session.Values["loginTime"] = currentTime.Format(time.RFC3339)
		session.Values["expireTime"] = currentTime.
			Add(time.Second * time.Duration(g.options.SessionLifeTime)).
			Format(time.RFC3339)
		session.Values["userHost"] = strings.Split(req.RemoteAddr, ":")[0]
		session.Values["useragent"] = req.UserAgent()

		redirectURL := "/"
		if g.options.PostLoginRoute == "" {
			r, ok := session.Values["postLoginRedirect"]
			if ok {
				redirectURL = r.(string)
			}
		} else {
			redirectURL = g.options.PostLoginRoute
		}
		if redirectURL == g.options.LoginRoute {
			redirectURL = "/"
		}

		session.Values["postLoginRedirect"] = ""

		if err := session.Save(req, res); err != nil {
			http.Error(res, err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(res, req, redirectURL, http.StatusFound)
	} else {
		if g.options.AuthGuard != nil {
			g.options.AuthGuard.Complaint(user, req)
		}
		http.Redirect(res, req, g.options.LoginRoute, http.StatusFound)
	}
}

// LogoutHandler is a handler for processing a logout action.
func (g *Gelada) LogoutHandler(res http.ResponseWriter, req *http.Request) {
	session, err := g.store.Get(req, g.options.SessionName)
	if err != nil {
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}

	session.Options.MaxAge = -1
	if err := session.Save(req, res); err != nil {
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(res, req, g.options.PostLogoutRoute, http.StatusFound)
}

// GetClient return Client for current session.
func (g *Gelada) GetClient(req *http.Request) (*Client, error) {
	client := &Client{}

	session, err := g.store.Get(req, g.options.SessionName)
	if err != nil {
		return client, errors.New("fail to get cookies session: " + err.Error())
	}

	un, ok := session.Values["user"]
	if ok {
		client.Username = un.(string)
	}

	userLoginTimeRaw, ok := session.Values["loginTime"]
	if ok {
		userLoginTime, err := time.Parse(time.RFC3339, userLoginTimeRaw.(string))
		if err != nil {
			return client, errors.New("fail to parse user login time: " + err.Error())
		}
		client.LoginDate = userLoginTime
	}

	userExpireTimeRaw, ok := session.Values["expireTime"]
	if ok {
		userExpireTime, err := time.Parse(time.RFC3339, userExpireTimeRaw.(string))
		if err != nil {
			return client, errors.New("fail to parse user expire time: " + err.Error())
		}
		client.ExpireDate = userExpireTime
	}

	ua, ok := session.Values["useragent"]
	if ok {
		client.UserAgent = ua.(string)
	}

	uh, ok := session.Values["userHost"]
	if ok {
		client.UserHost = uh.(string)
	}

	client.gelada = g
	client.session = session

	return client, nil
}

////////////////////////////////////////////////////////////////////////////////
//                    some helpers and predefined stuff                       //
////////////////////////////////////////////////////////////////////////////////

// SimpleAuthPage provide simple auth page handler.
func (g *Gelada) SimpleAuthPage(res http.ResponseWriter, req *http.Request) {
	var loginPage = template.Must(template.New("").Parse(`
		<html><head></head><body>
		<center>
		<form id="login_form" action="{{.LoginRoute}}" method="POST" style="padding-top:15%;">
		<input type="text" name="{{.LoginUserFieldName}}" placeholder="Login" autofocus><br>
		<input type="{{.LoginPasswordFieldName}}" placeholder="Password" name="password"><br>
		<input type="submit" value="LOGIN">
		</form></center></body>
		</html>`),
	)
	loginPage.Execute(res, g.options)
}

// SimpleAuthProvider provide simple AuthProvider based on key=value list.
func (g *Gelada) SimpleAuthProvider(userlist map[string]string) AuthProviderType {
	return func(u, p string) bool {
		pass, ok := userlist[u]
		if !ok {
			return false
		}
		if len(p) != len(pass) {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(pass), []byte(p)) != 1 {
			return false
		}
		return true
	}
}
