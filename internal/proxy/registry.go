package proxy

var NativeRoutes = map[string]bool{
	"/health":              true,
	"/api/session":         true,
	"/api/sessions":        true,
	"/api/sessions/search": true,
	"/api/list":            true,
	"/api/file":            true,
	"/api/file/raw":        true,
	"/api/workspaces":      true,
}

func IsNative(path string) bool { return NativeRoutes[path] }
