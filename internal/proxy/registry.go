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
	"/health":                            {http.MethodGet: true},
	"/api/session":                       {http.MethodGet: true},
	"/api/sessions":                      {http.MethodGet: true},
	"/api/sessions/search":               {http.MethodGet: true},
	"/api/list":                          {http.MethodGet: true},
	"/api/file":                          {http.MethodGet: true},
	"/api/file/raw":                      {http.MethodGet: true},
	"/api/workspaces":                    {http.MethodGet: true},
	"/api/session/new":                   {http.MethodPost: true},
	"/api/session/update":                {http.MethodPost: true},
	"/api/session/delete":                {http.MethodPost: true},
	"/api/session/rename":                {http.MethodPost: true},
	"/api/session/status":                {http.MethodGet: true},
	"/api/session/usage":                 {http.MethodGet: true},
	"/api/session/pin":                   {http.MethodPost: true},
	"/api/session/archive":               {http.MethodPost: true},
	"/api/session/move":                  {http.MethodPost: true},
	"/api/session/toolsets":              {http.MethodPost: true},
	"/api/session/draft":                 {http.MethodGet: true, http.MethodPost: true},
	"/api/session/truncate":              {http.MethodPost: true},
	"/api/session/clear":                 {http.MethodPost: true},
	"/api/session/duplicate":             {http.MethodPost: true},
	"/api/sessions/cleanup":              {http.MethodPost: true},
	"/api/sessions/cleanup_zero_message": {http.MethodPost: true},
	"/api/session/conversation-rounds":   {http.MethodPost: true},
	"/api/profile/active":                {http.MethodGet: true},
	"/api/profiles":                      {http.MethodGet: true},
	"/api/model/auxiliary":               {http.MethodGet: true},
	"/api/model/set":                     {http.MethodPost: true},
	"/api/providers":                     {http.MethodPost: true},
	"/api/providers/delete":              {http.MethodPost: true},
	"/api/settings":                      {http.MethodGet: true, http.MethodPost: true},
	"/api/workspaces/add":                {http.MethodPost: true},
	"/api/workspaces/remove":             {http.MethodPost: true},
	"/api/workspaces/rename":             {http.MethodPost: true},
	"/api/file/save":                     {http.MethodPost: true},
	"/api/file/create":                   {http.MethodPost: true},
	"/api/file/delete":                   {http.MethodPost: true},
	"/api/upload":                        {http.MethodPost: true},
	"/api/session/export":                {http.MethodGet: true},
	"/api/skills/save":                   {http.MethodPost: true},
	"/api/skills/delete":                 {http.MethodPost: true},
	"/api/skills/toggle":                 {http.MethodPost: true},
	"/api/memory/write":                  {http.MethodPost: true},
	"/api/crons":                         {http.MethodGet: true},
	"/api/crons/output":                  {http.MethodGet: true},
	"/api/crons/run":                     {http.MethodGet: true},
	"/api/crons/create":                  {http.MethodPost: true},
	"/api/crons/update":                  {http.MethodPost: true},
	"/api/crons/delete":                  {http.MethodPost: true},
	"/api/crons/pause":                   {http.MethodPost: true},
	"/api/crons/resume":                  {http.MethodPost: true},
	"/api/crons/delivery-options":        {http.MethodGet: true},
	"/api/approval/pending":              {http.MethodGet: true},
	"/api/approval/respond":              {http.MethodPost: true},
	"/api/auth/login":                    {http.MethodPost: true},
	"/api/auth/status":                   {http.MethodGet: true},
}

func IsNativeMethod(method, path string) bool {
	return NativeMethods[path][method]
}

var NativeRoutes = map[string]bool{
	"/health":                            true,
	"/api/session":                       true,
	"/api/sessions":                      true,
	"/api/sessions/search":               true,
	"/api/list":                          true,
	"/api/file":                          true,
	"/api/file/raw":                      true,
	"/api/workspaces":                    true,
	"/api/session/new":                   true,
	"/api/session/update":                true,
	"/api/session/delete":                true,
	"/api/session/rename":                true,
	"/api/session/status":                true,
	"/api/session/usage":                 true,
	"/api/session/pin":                   true,
	"/api/session/archive":               true,
	"/api/session/move":                  true,
	"/api/session/toolsets":              true,
	"/api/session/draft":                 true,
	"/api/session/truncate":              true,
	"/api/session/clear":                 true,
	"/api/session/duplicate":             true,
	"/api/sessions/cleanup":              true,
	"/api/sessions/cleanup_zero_message": true,
	"/api/workspaces/add":                true,
	"/api/workspaces/remove":             true,
	"/api/workspaces/rename":             true,
	"/api/file/save":                     true,
	"/api/file/create":                   true,
	"/api/file/delete":                   true,
	"/api/upload":                        true,
	"/api/session/export":                true,
	"/api/skills/save":                   true,
	"/api/skills/delete":                 true,
	"/api/skills/toggle":                 true,
	"/api/memory/write":                  true,
	"/api/crons":                         true,
	"/api/crons/output":                  true,
	"/api/crons/run":                     true,
	"/api/crons/create":                  true,
	"/api/crons/update":                  true,
	"/api/crons/delete":                  true,
	"/api/crons/pause":                   true,
	"/api/crons/resume":                  true,
	"/api/crons/delivery-options":        true,
	"/api/approval/pending":              true,
	"/api/approval/respond":              true,
	"/api/auth/login":                    true,
	"/api/auth/status":                   true,
	"/api/profile/active":                true,
	"/api/profiles":                      true,
	"/api/settings":                      true,
	"/api/model/auxiliary":               true,
	"/api/model/set":                     true,
	"/api/providers":                     true,
	"/api/providers/delete":              true,
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
