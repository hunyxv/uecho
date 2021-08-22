package uecho

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/acme"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// Handler defines a interface to serve HTTP requests.
type Handler interface {
	Handle(*Context) error
}

var _ Handler = HandlerFunc(nil)

// Handler defines a function to serve HTTP requests.
type HandlerFunc func(*Context) error

func (f HandlerFunc) Handle(c *Context) error {
	return f(c)
}

// WrapHandler uecho.Handler 包装为 echo.HandlerFunc
func WrapHandler(h Handler) echo.HandlerFunc {
	return func(c echo.Context) error {
		uctx, ok := c.(*Context)
		if !ok {
			panic("context: type err")
		}
		return h.Handle(uctx)
	}
}

// WrapUHandler echo.HandlerFunc 包装为 uecho.Handler
func WrapUHandler(h echo.HandlerFunc) Handler {
	hfunc := func(c *Context) error {
		return h(c)
	}
	return HandlerFunc(hfunc)
}

var (
	NotFoundHandler = HandlerFunc(func(c *Context) error {
		err := errReplyPool.Get().(*errReply)
		err.reset()
		err.Reply = ErrNotFound
		return err
	})

	MethodNotAllowedHandler = HandlerFunc(func(c *Context) error {
		err := errReplyPool.Get().(*errReply)
		err.reset()
		err.Reply = ErrMethodNotAllowed
		return err
	})
)

/* pprof
https://github.com/sevennt/echo-pprof
*/

// Common struct for Echo & Group.
type common struct{}

func (common) static(prefix, root string, get func(string, Handler, ...echo.MiddlewareFunc) *echo.Route) *echo.Route {
	hfunc := func(c *Context) error {
		p, err := url.PathUnescape(c.Param("*"))
		if err != nil {
			return err
		}

		name := filepath.Join(root, filepath.Clean("/"+p)) // "/"+ for security
		fi, err := os.Stat(name)
		if err != nil {
			// The access path does not exist
			return echo.NotFoundHandler(c)
		}

		// If the request is for a directory and does not end with "/"
		p = c.Request().URL.Path // path must not be empty.
		if fi.IsDir() && p[len(p)-1] != '/' {
			// Redirect to ends with "/"
			return c.Redirect(http.StatusMovedPermanently, p+"/")
		}
		return c.File(name)
	}
	h := HandlerFunc(hfunc)
	// Handle added routes based on trailing slash:
	// 	/prefix  => exact route "/prefix" + any route "/prefix/*"
	// 	/prefix/ => only any route "/prefix/*"
	if prefix != "" {
		if prefix[len(prefix)-1] == '/' {
			// Only add any route for intentional trailing slash
			return get(prefix+"*", h)
		}
		get(prefix, h)
	}
	return get(prefix+"/*", h)
}

func (common) file(path, file string, get func(string, Handler, ...echo.MiddlewareFunc) *echo.Route,
	m ...echo.MiddlewareFunc) *echo.Route {
	f := func(c *Context) error {
		return c.File(file)
	}
	return get(path, HandlerFunc(f), m...)
}

type UEcho struct {
	common
	*echo.Echo

	startupMutex  sync.RWMutex
	premiddleware []echo.MiddlewareFunc
	middleware    []echo.MiddlewareFunc
	pool          sync.Pool
	router        *Router
	routers       map[string]*Router
}

func New(logger *logrus.Logger) *UEcho {
	e := &UEcho{
		Echo:    echo.New(),
		routers: map[string]*Router{},
	}
	e.Server.Handler = e
	e.TLSServer.Handler = e
	e.pool.New = func() interface{} {
		c := new(Context)
		c.setLogrus(logger)
		c.init(e.Echo.AcquireContext())
		return c
	}
	e.HTTPErrorHandler = e.DefaultHTTPErrorHandler

	e.router = NewRouter(e)
	return e
}

// Router returns the default router.
func (e *UEcho) Router() *Router {
	return e.router
}

// Routers returns the map of host => router.
func (e *UEcho) Routers() map[string]*Router {
	return e.routers
}

// DefaultHTTPErrorHandler is the default HTTP error handler. It sends a JSON response
// with status code.
func (e *UEcho) DefaultHTTPErrorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}

	// Send response
	var er *errReply
	er, ok := err.(*errReply)
	if !ok {
		if echoHttpErr, ok := err.(*echo.HTTPError); ok {
			er = errReplyPool.Get().(*errReply)
			er.reset()
			er.Reply = NewReply(echoHttpErr.Code, echoHttpErr.Code, fmt.Sprint(echoHttpErr.Message))
		} else {
			er = errReplyPool.Get().(*errReply)
			er.reset()
			er.Reply = NewReply(http.StatusInternalServerError,
				http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		}
	}
	defer func() {
		errReplyPool.Put(er)
	}()

	code := er.EC()
	message := er.EM()
	if e.Debug {
		message = er.Error()
	}

	if c.Request().Method == http.MethodHead { // Issue #608
		err = c.NoContent(code)
	} else {
		err = c.JSON(er.HTTPCode(), &HttpApiResponse{
			EC: code,
			EM: message,
		})
	}
	if err != nil {
		e.Logger.Error(err)
	}
}

// Pre adds middleware to the chain which is run before router.
func (e *UEcho) Pre(middleware ...echo.MiddlewareFunc) {
	e.premiddleware = append(e.premiddleware, middleware...)
}

// Use adds middleware to the chain which is run after router.
func (e *UEcho) Use(middleware ...echo.MiddlewareFunc) {
	e.middleware = append(e.middleware, middleware...)
}

// CONNECT registers a new CONNECT route for a path with matching handler in the
// router with optional route-level middleware.
func (e *UEcho) CONNECT(path string, h Handler, m ...echo.MiddlewareFunc) *echo.Route {
	return e.Add(http.MethodConnect, path, h, m...)
}

// DELETE registers a new DELETE route for a path with matching handler in the router
// with optional route-level middleware.
func (e *UEcho) DELETE(path string, h Handler, m ...echo.MiddlewareFunc) *echo.Route {
	return e.Add(http.MethodDelete, path, h, m...)
}

// GET registers a new GET route for a path with matching handler in the router
// with optional route-level middleware.
func (e *UEcho) GET(path string, h Handler, m ...echo.MiddlewareFunc) *echo.Route {
	return e.Add(http.MethodGet, path, h, m...)
}

// HEAD registers a new HEAD route for a path with matching handler in the
// router with optional route-level middleware.
func (e *UEcho) HEAD(path string, h Handler, m ...echo.MiddlewareFunc) *echo.Route {
	return e.Add(http.MethodHead, path, h, m...)
}

// OPTIONS registers a new OPTIONS route for a path with matching handler in the
// router with optional route-level middleware.
func (e *UEcho) OPTIONS(path string, h Handler, m ...echo.MiddlewareFunc) *echo.Route {
	return e.Add(http.MethodOptions, path, h, m...)
}

// PATCH registers a new PATCH route for a path with matching handler in the
// router with optional route-level middleware.
func (e *UEcho) PATCH(path string, h Handler, m ...echo.MiddlewareFunc) *echo.Route {
	return e.Add(http.MethodPatch, path, h, m...)
}

// POST registers a new POST route for a path with matching handler in the
// router with optional route-level middleware.
func (e *UEcho) POST(path string, h Handler, m ...echo.MiddlewareFunc) *echo.Route {
	return e.Add(http.MethodPost, path, h, m...)
}

// PUT registers a new PUT route for a path with matching handler in the
// router with optional route-level middleware.
func (e *UEcho) PUT(path string, h Handler, m ...echo.MiddlewareFunc) *echo.Route {
	return e.Add(http.MethodPut, path, h, m...)
}

// TRACE registers a new TRACE route for a path with matching handler in the
// router with optional route-level middleware.
func (e *UEcho) TRACE(path string, h Handler, m ...echo.MiddlewareFunc) *echo.Route {
	return e.Add(http.MethodTrace, path, h, m...)
}

// Any registers a new route for all HTTP methods and path with matching handler
// in the router with optional route-level middleware.
func (e *UEcho) Any(path string, handler Handler, middleware ...echo.MiddlewareFunc) []*echo.Route {
	routes := make([]*echo.Route, len(methods))
	for i, m := range methods {
		routes[i] = e.Add(m, path, handler, middleware...)
	}
	return routes
}

// Match registers a new route for multiple HTTP methods and path with matching
// handler in the router with optional route-level middleware.
func (e *UEcho) Match(methods []string, path string, handler Handler, middleware ...echo.MiddlewareFunc) []*echo.Route {
	routes := make([]*echo.Route, len(methods))
	for i, m := range methods {
		routes[i] = e.Add(m, path, handler, middleware...)
	}
	return routes
}

// Static registers a new route with path prefix to serve static files from the
// provided root directory.
func (e *UEcho) Static(prefix, root string) *echo.Route {
	if root == "" {
		root = "." // For security we want to restrict to CWD.
	}
	return e.static(prefix, root, e.GET)
}

// File registers a new route with path to serve a static file with optional route-level middleware.
func (e *UEcho) File(path, file string, m ...echo.MiddlewareFunc) *echo.Route {
	return e.file(path, file, e.GET, m...)
}

// Add registers a new route for an HTTP method and path with matching handler
// in the router with optional route-level middleware.
func (e *UEcho) Add(method, path string, handler Handler, middleware ...echo.MiddlewareFunc) *echo.Route {
	return e.add("", method, path, handler, middleware...) //e.Echo.Add(method, path, WrapHandler(handler), middleware...)
}

// Host creates a new router group for the provided host and optional host-level middleware.
func (e *UEcho) Host(name string, m ...echo.MiddlewareFunc) (g *Group) {
	e.routers[name] = NewRouter(e)
	g = &Group{host: name, echo: e}
	g.Use(m...)
	return
}

// Group creates a new router group with prefix and optional group-level middleware.
func (e *UEcho) Group(prefix string, m ...echo.MiddlewareFunc) (g *Group) {
	g = &Group{prefix: prefix, echo: e}
	g.Use(m...)
	return
}

func (e *UEcho) findRouter(host string) *Router {
	if len(e.routers) > 0 {
		if r, ok := e.routers[host]; ok {
			return r
		}
	}
	return e.router
}

func handlerName(h Handler) string {
	t := reflect.ValueOf(h).Type()
	if t.Kind() == reflect.Func {
		return runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
	}
	return t.String()
}

func (e *UEcho) add(host, method, path string, handler Handler, middleware ...echo.MiddlewareFunc) *echo.Route {
	name := handlerName(handler)
	router := e.findRouter(host)
	router.Add(method, path, HandlerFunc(func(c *Context) error {
		h := applyMiddleware(WrapHandler(handler), middleware...)
		return h(c)
	}))
	r := &echo.Route{
		Method: method,
		Path:   path,
		Name:   name,
	}
	e.router.routes[method+path] = r
	return r
}

// AcquireContext returns an empty `Context` instance from the pool.
// You must return the context by calling `ReleaseContext()`.
func (e *UEcho) AcquireContext() *Context {
	c := e.pool.Get().(*Context)
	c.init(e.Echo.AcquireContext())
	return c
}

// ReleaseContext returns the `Context` instance back to the pool.
// You must call it after `AcquireContext()`.
func (e *UEcho) ReleaseContext(c *Context) {
	ec := c.Context
	c.reset()
	e.Echo.ReleaseContext(ec)
	e.pool.Put(c)
}

// ServeHTTP implements `http.Handler` interface, which serves HTTP requests.
func (e *UEcho) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Acquire context
	c := e.AcquireContext()
	c.Reset(r, w)
	h := echo.NotFoundHandler

	if e.premiddleware == nil {
		e.findRouter(r.Host).Find(r.Method, GetPath(r), c.Context)
		h = c.Handler()
		h = applyMiddleware(h, e.middleware...)
	} else {
		h = func(c echo.Context) error {
			uc := c.(*Context)
			e.findRouter(r.Host).Find(r.Method, GetPath(r), uc.Context)
			h = c.Handler()
			h = applyMiddleware(h, e.middleware...)
			return h(c)
		}
		h = applyMiddleware(h, e.premiddleware...)
	}

	// Execute chain
	if err := h(c); err != nil {
		e.HTTPErrorHandler(err, c)
	}

	// Release context
	e.ReleaseContext(c)
}

// Start starts an HTTP server.
func (e *UEcho) Start(address string) error {
	e.startupMutex.Lock()
	e.Server.Addr = address
	if err := e.configureServer(e.Server); err != nil {
		e.startupMutex.Unlock()
		return err
	}
	e.startupMutex.Unlock()
	return e.Server.Serve(e.Listener)
}

// StartTLS starts an HTTPS server.
// If `certFile` or `keyFile` is `string` the values are treated as file paths.
// If `certFile` or `keyFile` is `[]byte` the values are treated as the certificate or key as-is.
func (e *UEcho) StartTLS(address string, certFile, keyFile interface{}) (err error) {
	e.startupMutex.Lock()
	var cert []byte
	if cert, err = filepathOrContent(certFile); err != nil {
		e.startupMutex.Unlock()
		return
	}

	var key []byte
	if key, err = filepathOrContent(keyFile); err != nil {
		e.startupMutex.Unlock()
		return
	}

	s := e.TLSServer
	s.TLSConfig = new(tls.Config)
	s.TLSConfig.Certificates = make([]tls.Certificate, 1)
	if s.TLSConfig.Certificates[0], err = tls.X509KeyPair(cert, key); err != nil {
		e.startupMutex.Unlock()
		return
	}

	e.configureTLS(address)
	if err := e.configureServer(s); err != nil {
		e.startupMutex.Unlock()
		return err
	}
	e.startupMutex.Unlock()
	return s.Serve(e.TLSListener)
}

func filepathOrContent(fileOrContent interface{}) (content []byte, err error) {
	switch v := fileOrContent.(type) {
	case string:
		return ioutil.ReadFile(v)
	case []byte:
		return v, nil
	default:
		return nil, echo.ErrInvalidCertOrKeyType
	}
}

// StartAutoTLS starts an HTTPS server using certificates automatically installed from https://letsencrypt.org.
func (e *UEcho) StartAutoTLS(address string) error {
	e.startupMutex.Lock()
	s := e.TLSServer
	s.TLSConfig = new(tls.Config)
	s.TLSConfig.GetCertificate = e.AutoTLSManager.GetCertificate
	s.TLSConfig.NextProtos = append(s.TLSConfig.NextProtos, acme.ALPNProto)

	e.configureTLS(address)
	if err := e.configureServer(s); err != nil {
		e.startupMutex.Unlock()
		return err
	}
	e.startupMutex.Unlock()
	return s.Serve(e.TLSListener)
}

func (e *UEcho) configureTLS(address string) {
	s := e.TLSServer
	s.Addr = address
	if !e.DisableHTTP2 {
		s.TLSConfig.NextProtos = append(s.TLSConfig.NextProtos, "h2")
	}
}

// StartServer starts a custom http server.
func (e *UEcho) StartServer(s *http.Server) (err error) {
	e.startupMutex.Lock()
	if err := e.configureServer(s); err != nil {
		e.startupMutex.Unlock()
		return err
	}
	if s.TLSConfig != nil {
		e.startupMutex.Unlock()
		return s.Serve(e.TLSListener)
	}
	e.startupMutex.Unlock()
	return s.Serve(e.Listener)
}

func (e *UEcho) configureServer(s *http.Server) (err error) {
	// Setup
	s.ErrorLog = e.StdLogger
	s.Handler = e
	if e.Debug {
		e.Logger.SetLevel(log.DEBUG)
	}

	if !e.HideBanner {
		fmt.Printf(banner, "v"+echo.Version, website)
	}

	if s.TLSConfig == nil {
		if e.Listener == nil {
			e.Listener, err = newListener(s.Addr, e.ListenerNetwork)
			if err != nil {
				return err
			}
		}
		if !e.HidePort {
			fmt.Printf("⇨ http server started on %s\n", e.Listener.Addr())
		}
		return nil
	}
	if e.TLSListener == nil {
		l, err := newListener(s.Addr, e.ListenerNetwork)
		if err != nil {
			return err
		}
		e.TLSListener = tls.NewListener(l, s.TLSConfig)
	}
	if !e.HidePort {
		fmt.Printf("⇨ https server started on %s\n", e.TLSListener.Addr())
	}
	return nil
}

// StartH2CServer starts a custom http/2 server with h2c (HTTP/2 Cleartext).
func (e *UEcho) StartH2CServer(address string, h2s *http2.Server) (err error) {
	e.startupMutex.Lock()
	// Setup
	s := e.Server
	s.Addr = address
	e.Logger.SetOutput(e.Logger.Output())
	s.ErrorLog = e.StdLogger
	s.Handler = h2c.NewHandler(e, h2s)
	if e.Debug {
		e.Logger.SetLevel(log.DEBUG)
	}

	if !e.HideBanner {
		fmt.Printf(banner, "v"+echo.Version, website)
	}

	if e.Listener == nil {
		e.Listener, err = newListener(s.Addr, e.ListenerNetwork)
		if err != nil {
			e.startupMutex.Unlock()
			return err
		}
	}
	if !e.HidePort {
		fmt.Printf("⇨ http server started on %s\n", e.Listener.Addr())
	}
	e.startupMutex.Unlock()
	return s.Serve(e.Listener)
}

// Close immediately stops the server.
// It internally calls `http.Server#Close()`.
func (e *UEcho) Close() error {
	e.startupMutex.Lock()
	defer e.startupMutex.Unlock()
	if err := e.TLSServer.Close(); err != nil {
		return err
	}
	return e.Server.Close()
}

// Shutdown stops the server gracefully.
// It internally calls `http.Server#Shutdown()`.
func (e *UEcho) Shutdown(ctx context.Context) error {
	e.startupMutex.Lock()
	defer e.startupMutex.Unlock()
	if err := e.TLSServer.Shutdown(ctx); err != nil {
		return err
	}
	return e.Server.Shutdown(ctx)
}

// GetPath returns RawPath, if it's empty returns Path from URL
// Difference between RawPath and Path is:
//  * Path is where request path is stored. Value is stored in decoded form: /%47%6f%2f becomes /Go/.
//  * RawPath is an optional field which only gets set if the default encoding is different from Path.
func GetPath(r *http.Request) string {
	path := r.URL.RawPath
	if path == "" {
		path = r.URL.Path
	}
	return path
}

type tcpKeepAliveListener struct {
	*net.TCPListener
}

func (ln tcpKeepAliveListener) Accept() (c net.Conn, err error) {
	if c, err = ln.AcceptTCP(); err != nil {
		return
	} else if err = c.(*net.TCPConn).SetKeepAlive(true); err != nil {
		return
	}
	// Ignore error from setting the KeepAlivePeriod as some systems, such as
	// OpenBSD, do not support setting TCP_USER_TIMEOUT on IPPROTO_TCP
	_ = c.(*net.TCPConn).SetKeepAlivePeriod(3 * time.Minute)
	return
}

func newListener(address, network string) (*tcpKeepAliveListener, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, echo.ErrInvalidListenerNetwork
	}
	l, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	return &tcpKeepAliveListener{l.(*net.TCPListener)}, nil
}

func applyMiddleware(h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.HandlerFunc {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}

const (
	website = "https://echo.labstack.com"
	// 字体为 smslant （http://www.network-science.de/ascii/）
	banner = `
       ____    __      
 __ __/ __/___/ /  ___ 
/ // / _// __/ _ \/ _ \
\_,_/___/\__/_//_/\___/%s
High performance, minimalist Go web framework
Forked from https://github.com/labstack/echo
%s
____________________________________O/_______
                                    O\
`
)
