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
	"/api/providers/delete":              {http.MethodPost: true},
	"/api/profile/create":                {http.MethodPost: true},
	"/api/profile/switch":                {http.MethodPost: true},
	"/api/profile/update":                {http.MethodPost: true},
	"/api/profile/delete":                {http.MethodPost: true},
	"/api/models":                        {http.MethodGet: true},
	"/api/models/live":                   {http.MethodGet: true},
	"/api/models/refresh":                {http.MethodPost: true},
	"/api/providers":                     {http.MethodGet: true, http.MethodPost: true},
	"/api/providers/self-hosted":         {http.MethodPost: true},
	"/api/provider/quota":                {http.MethodGet: true},
	"/api/provider/cost-history":         {http.MethodGet: true},
	"/api/reasoning":                     {http.MethodGet: true, http.MethodPost: true},
	"/api/dashboard/config":              {http.MethodGet: true},
	"/api/dashboard/status":              {http.MethodGet: true},
	"/api/projects":                      {http.MethodGet: true},
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
	"/api/crons/recent":                  {http.MethodGet: true},
	"/api/crons/history":                 {http.MethodGet: true},
	"/api/crons/status":                  {http.MethodGet: true},
	"/api/approval/pending":              {http.MethodGet: true},
	"/api/approval/respond":              {http.MethodPost: true},
	"/api/auth/login":                    {http.MethodPost: true},
	"/api/auth/status":                   {http.MethodGet: true},
	"/api/clarify/pending":               {http.MethodGet: true},
	"/api/session/stream":                {http.MethodGet: true},
	"/api/logs":                          {http.MethodGet: true},
	"/api/client-events/log":             {http.MethodPost: true},
	"/api/session/compress/status":       {http.MethodGet: true},
	"/api/health/agent":                  {http.MethodGet: true},
	"/api/git/status":                    {http.MethodGet: true},
	"/api/git/branches":                  {http.MethodGet: true},
	"/api/git/diff":                      {http.MethodGet: true},
	"/api/git/stage":                     {http.MethodPost: true},
	"/api/git/unstage":                   {http.MethodPost: true},
	"/api/git/discard":                   {http.MethodPost: true},
	"/api/git/commit":                    {http.MethodPost: true},
	"/api/git/commit-selected":           {http.MethodPost: true},
	"/api/git/fetch":                     {http.MethodPost: true},
	"/api/git/pull":                      {http.MethodPost: true},
	"/api/git/push":                      {http.MethodPost: true},
	"/api/git/checkout":                  {http.MethodPost: true},
	"/api/git/stash-checkout":            {http.MethodPost: true},
	"/api/notes":                         {http.MethodGet: true},
	"/api/notes/sources":                 {http.MethodGet: true},
	"/api/notes/search":                  {http.MethodGet: true},
	"/api/notes/item":                    {http.MethodGet: true},
	"/api/wiki/browse":                   {http.MethodGet: true},
	"/api/wiki/page":                     {http.MethodGet: true},
	"/api/workspaces/suggest":            {http.MethodGet: true},
	"/api/workspaces/health":             {http.MethodGet: true},
	"/api/workspaces/filemap":            {http.MethodGet: true},
	"/api/plugins":                       {http.MethodGet: true},
	"/api/auth/logout":                   {http.MethodPost: true},
	"/api/commands":                      {http.MethodGet: true},
	"/api/commands/bundles":              {http.MethodGet: true},
	"/api/personalities":                 {http.MethodGet: true},
	"/api/prompts":                       {http.MethodGet: true},
	"/api/default-model":                 {http.MethodPost: true},
	"/api/knowledge":                     {http.MethodGet: true},
	"/api/csp-report":                    {http.MethodPost: true},
	"/api/transcribe/capability":         {http.MethodGet: true},
	"/api/wiki/status":                   {http.MethodGet: true},
	"/api/insights":                      {http.MethodGet: true},
	"/api/updates/check":                 {http.MethodGet: true},
	"/api/onboarding/status":             {http.MethodGet: true},
	"/api/git-info":                      {http.MethodGet: true},
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
	"/api/crons/recent":                  true,
	"/api/crons/history":                 true,
	"/api/crons/status":                  true,
	"/api/approval/pending":              true,
	"/api/approval/respond":              true,
	"/api/auth/login":                    true,
	"/api/auth/status":                   true,
	"/api/clarify/pending":               true,
	"/api/session/stream":                true,
	"/api/logs":                          true,
	"/api/client-events/log":             true,
	"/api/session/compress/status":       true,
	"/api/health/agent":                  true,
	"/api/git/status":                    true,
	"/api/git/branches":                  true,
	"/api/git/diff":                      true,
	"/api/git/stage":                     true,
	"/api/git/unstage":                   true,
	"/api/git/discard":                   true,
	"/api/git/commit":                    true,
	"/api/git/commit-selected":           true,
	"/api/git/fetch":                     true,
	"/api/git/pull":                      true,
	"/api/git/push":                      true,
	"/api/git/checkout":                  true,
	"/api/git/stash-checkout":            true,
	"/api/notes":                         true,
	"/api/notes/sources":                 true,
	"/api/notes/search":                  true,
	"/api/notes/item":                    true,
	"/api/wiki/browse":                   true,
	"/api/wiki/page":                     true,
	"/api/workspaces/suggest":            true,
	"/api/workspaces/health":             true,
	"/api/workspaces/filemap":            true,
	"/api/plugins":                       true,
	"/api/auth/logout":                   true,
	"/api/commands":                      true,
	"/api/commands/bundles":              true,
	"/api/personalities":                 true,
	"/api/prompts":                       true,
	"/api/default-model":                 true,
	"/api/knowledge":                     true,
	"/api/csp-report":                    true,
	"/api/transcribe/capability":         true,
	"/api/wiki/status":                   true,
	"/api/insights":                      true,
	"/api/updates/check":                 true,
	"/api/onboarding/status":             true,
	"/api/git-info":                      true,
	"/api/profile/active":                true,
	"/api/profiles":                      true,
	"/api/settings":                      true,
	"/api/model/auxiliary":               true,
	"/api/model/set":                     true,
	"/api/providers":                     true,
	"/api/providers/delete":              true,
	"/api/profile/create":                true,
	"/api/profile/switch":                true,
	"/api/profile/update":                true,
	"/api/profile/delete":                true,
	"/api/models":                        true,
	"/api/models/live":                   true,
	"/api/models/refresh":                true,
	"/api/providers/self-hosted":         true,
	"/api/provider/quota":                true,
	"/api/provider/cost-history":         true,
	"/api/reasoning":                     true,
	"/api/session/conversation-rounds":   true,
	"/api/dashboard/config":              true,
	"/api/dashboard/status":              true,
	"/api/projects":                      true,
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
