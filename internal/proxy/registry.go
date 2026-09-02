package proxy

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
}

func IsNative(path string) bool { return NativeRoutes[path] }
