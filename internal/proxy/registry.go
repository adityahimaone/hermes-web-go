package proxy

var NativeRoutes = map[string]bool{"/health": true}

func IsNative(path string) bool { return NativeRoutes[path] }
