package httpserver

import (
	"errors"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/skillsmem"
)

// SkillsMemRouter mounts the native read-only Skills + Memory panel routes.
// Writes (skills toggle, memory write) stay behind the proxy.
func SkillsMemRouter(r chi.Router, home string) {
	r.Get("/api/skills", func(w http.ResponseWriter, r *http.Request) {
		skills, err := skillsmem.ListSkills(home)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read skills")
			return
		}
		writeJSON(w, map[string]any{"skills": skills})
	})

	r.Get("/api/skills/usage", func(w http.ResponseWriter, r *http.Request) {
		usage, err := skillsmem.ReadUsage(home)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read skill usage")
			return
		}
		writeJSON(w, usage)
	})

	r.Get("/api/skills/content", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		content, err := skillsmem.SkillContent(home, name)
		if err != nil {
			if errors.Is(err, skillsmem.ErrInvalidSkillName) {
				writeError(w, http.StatusBadRequest, "Invalid skill name")
				return
			}
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, http.StatusNotFound, "Skill not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to read skill")
			return
		}
		writeJSON(w, content)
	})

	r.Get("/api/memory", func(w http.ResponseWriter, r *http.Request) {
		memory, err := skillsmem.ReadMemory(home)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read memory")
			return
		}
		writeJSON(w, memory)
	})
}
