package uecho

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

var methods = [...]string{
	http.MethodConnect,
	http.MethodDelete,
	http.MethodGet,
	http.MethodHead,
	http.MethodOptions,
	http.MethodPatch,
	http.MethodPost,
	http.MethodPut,
	http.MethodTrace,
	echo.PROPFIND,
	echo.REPORT,
}

type Router struct {
	*echo.Router

	routes map[string]*echo.Route
}

func NewRouter(e *UEcho) *Router {
	return &Router{
		Router: echo.NewRouter(e.Echo),
		routes: map[string]*echo.Route{},
	}
}

// Add registers a new route for method and path with matching handler.
func (r *Router) Add(method, path string, h Handler) {
	r.Router.Add(method, path, WrapHandler(h))
}

func (r *Router) Find(method, path string, c echo.Context) {
	r.Router.Find(method, path, c)
}
