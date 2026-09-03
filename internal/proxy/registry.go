package proxy

import "net/http"

type Key struct {
	Method  string
	Pattern string
}

type Owner interface {
	Ready() bool
	http.Handler
}

type Registry struct {
	routes map[Key]Owner
}

func NewRegistry(routes map[Key]Owner) *Registry {
	return &Registry{routes: routes}
}

func (r *Registry) Resolve(method, path string) (Owner, bool) {
	if r == nil {
		return nil, false
	}
	owner, ok := r.routes[Key{Method: method, Pattern: path}]
	return owner, ok && owner != nil && owner.Ready()
}

func (r *Registry) IsNative(method, path string) bool {
	_, ok := r.Resolve(method, path)
	return ok
}

// NativeMethods records the methods actually registered by the Go router.
// Keeping methods explicit prevents a native GET from swallowing a Python-
// owned POST/PUT/DELETE for the same path.
var NativeMethods = map[string]map[string]bool{
	"/health":                {http.MethodGet: true},
	"/api/session":           {http.MethodGet: true},
	"/api/sessions":          {http.MethodGet: true},
	"/api/sessions/search":   {http.MethodGet: true},
	"/api/list":              {http.MethodGet: true},
	"/api/file":              {http.MethodGet: true},
	"/api/file/raw":          {http.MethodGet: true},
	"/api/workspaces":        {http.MethodGet: true},
	"/api/session/new":       {http.MethodPost: true},
	"/api/session/update":    {http.MethodPost: true},
	"/api/session/delete":    {http.MethodPost: true},
	"/api/session/rename":    {http.MethodPost: true},
	"/api/workspaces/add":    {http.MethodPost: true},
	"/api/workspaces/remove": {http.MethodPost: true},
	"/api/workspaces/rename": {http.MethodPost: true},
	"/api/file/save":         {http.MethodPost: true},
	"/api/file/create":       {http.MethodPost: true},
	"/api/file/delete":       {http.MethodPost: true},
	"/api/upload":            {http.MethodPost: true},
	"/api/session/export":    {http.MethodGet: true},
	"/api/skills/save":       {http.MethodPost: true},
	"/api/skills/delete":     {http.MethodPost: true},
	"/api/skills/toggle":     {http.MethodPost: true},
	"/api/memory/write":      {http.MethodPost: true},
}

func IsNativeMethod(method, path string) bool {
	return NativeMethods[path][method]
}

var NativeRoutes = map[string]bool{
	"/health":                true,
	"/api/session":           true,
	"/api/sessions":          true,
	"/api/sessions/search":   true,
	"/api/list":              true,
	"/api/file":              true,
	"/api/file/raw":          true,
	"/api/workspaces":        true,
	"/api/session/new":       true,
	"/api/session/update":    true,
	"/api/session/delete":    true,
	"/api/session/rename":    true,
	"/api/workspaces/add":    true,
	"/api/workspaces/remove": true,
	"/api/workspaces/rename": true,
	"/api/file/save":         true,
	"/api/file/create":       true,
	"/api/file/delete":       true,
	"/api/upload":            true,
	"/api/session/export":    true,
	"/api/skills/save":       true,
	"/api/skills/delete":     true,
	"/api/skills/toggle":     true,
	"/api/memory/write":      true,
}

func IsNative(path string) bool { return NativeRoutes[path] }

func NativeKeys() []Key {
	keys := make([]Key, 0, len(NativeRoutes))
	// Default method is GET for reads, POST for mutations - enumerate both where applicable
	for path := range NativeRoutes {
		keys = append(keys, Key{Pattern: path})
	}
	return keys
}
