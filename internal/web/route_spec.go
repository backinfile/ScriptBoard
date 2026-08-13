package web

import (
	"net/http"
	"scriptboard/internal/identity"
	"strings"
)

type routeAuthMode string

const (
	routeAuthPublic   routeAuthMode = "public"
	routeAuthSession  routeAuthMode = "session"
	routeAuthExternal routeAuthMode = "external-capability"
)

type routeCSRFPolicy string

const (
	routeCSRFNone     routeCSRFPolicy = "none"
	routeCSRFRequired routeCSRFPolicy = "required"
)

// RouteSpec is the fail-closed security declaration attached to every HTTP
// route. Method and path are split out so tests can generate a complete
// method/role matrix without maintaining a second route inventory.
type RouteSpec struct {
	Pattern      string
	Method       string
	Path         string
	Auth         routeAuthMode
	Permission   identity.Permission
	StepUp       bool
	CSRF         routeCSRFPolicy
	MaxBodyBytes int64
}

type declaredRouteHandler struct {
	auth       routeAuthMode
	permission identity.Permission
	stepUp     bool
	handler    http.Handler
}

func (handler declaredRouteHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	handler.handler.ServeHTTP(response, request)
}

type declaredRouteMux struct {
	mux   *http.ServeMux
	specs []RouteSpec
}

func newDeclaredRouteMux() *declaredRouteMux {
	return &declaredRouteMux{mux: http.NewServeMux()}
}

func (mux *declaredRouteMux) Handle(pattern string, handler http.Handler) {
	declared, ok := handler.(declaredRouteHandler)
	if !ok {
		panic("route has no security declaration: " + pattern)
	}
	mux.register(pattern, declared)
}

func (mux *declaredRouteMux) Public(pattern string, handler http.HandlerFunc) {
	mux.register(pattern, declaredRouteHandler{auth: routeAuthPublic, handler: handler})
}

func (mux *declaredRouteMux) External(pattern string, handler http.HandlerFunc) {
	mux.register(pattern, declaredRouteHandler{auth: routeAuthExternal, handler: handler})
}

func (mux *declaredRouteMux) register(pattern string, declared declaredRouteHandler) {
	method, path := splitRoutePattern(pattern)
	csrf := routeCSRFNone
	if declared.auth == routeAuthSession && method != http.MethodGet && method != http.MethodHead {
		csrf = routeCSRFRequired
	}
	maxBodyBytes := int64(0)
	if method != http.MethodGet && method != http.MethodHead &&
		path != "/resources/files/upload" && path != "/settings/ai/runtime/offline" && path != "/trigger" {
		maxBodyBytes = maxFormRequestBytes
	}
	spec := RouteSpec{
		Pattern: pattern, Method: method, Path: path, Auth: declared.auth,
		Permission: declared.permission, StepUp: declared.stepUp, CSRF: csrf, MaxBodyBytes: maxBodyBytes,
	}
	mux.specs = append(mux.specs, spec)
	mux.mux.Handle(pattern, enforceRouteRequestPolicy(spec, declared))
}

func enforceRouteRequestPolicy(spec RouteSpec, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if spec.MaxBodyBytes > 0 && request.Body != nil {
			if request.ContentLength > spec.MaxBodyBytes {
				http.Error(response, "request body is too large", http.StatusRequestEntityTooLarge)
				return
			}
			resetReadDeadline := setRequestReadDeadline(response, boundedFormReadTimeout)
			defer resetReadDeadline()
			request.Body = http.MaxBytesReader(response, request.Body, spec.MaxBodyBytes)
		}
		mutating := spec.Method != http.MethodGet && spec.Method != http.MethodHead && spec.Method != http.MethodOptions
		if mutating && spec.Auth != routeAuthExternal && !validRequestOrigin(request) {
			http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (mux *declaredRouteMux) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	mux.mux.ServeHTTP(response, request)
}

func (mux *declaredRouteMux) Specs() []RouteSpec {
	return append([]RouteSpec(nil), mux.specs...)
}

func (a *App) declaredRoutePermission(method, path string) identity.Permission {
	for _, spec := range a.routeSpecs {
		if spec.Method == method && spec.Path == path {
			return spec.Permission
		}
	}
	panic("protected route has no permission declaration: " + method + " " + path)
}

func (a *App) declaredRouteStepUp(method, path string) bool {
	for _, spec := range a.routeSpecs {
		if spec.Method == method && spec.Path == path {
			return spec.StepUp
		}
	}
	panic("protected route has no authentication declaration: " + method + " " + path)
}

func splitRoutePattern(pattern string) (string, string) {
	method, path, found := strings.Cut(pattern, " ")
	if !found || method == "" || path == "" {
		panic("route must declare an HTTP method: " + pattern)
	}
	return method, path
}
