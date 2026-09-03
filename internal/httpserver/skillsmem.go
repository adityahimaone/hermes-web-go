package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/skillsmem"
)

// SkillsMemRouter mounts the native Skills + Memory panel routes. Writes
// (skills save/delete/toggle, memory write) mirror the Python handlers and are
// native — no proxy involvement.
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

	// ── Writes (native, mirror Python handlers) ──
	r.Post("/api/skills/save", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name     string `json:"name"`
			Content  string `json:"content"`
			Category string `json:"category"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if strings.TrimSpace(body.Name) == "" || body.Content == "" {
			writeError(w, http.StatusBadRequest, "name and content are required")
			return
		}
		path, err := skillsmem.SaveSkill(home, body.Name, body.Content, body.Category)
		if err != nil {
			if errors.Is(err, skillsmem.ErrInvalidSkillName) {
				writeError(w, http.StatusBadRequest, "Invalid skill name")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to save skill")
			return
		}
		name := strings.ToLower(strings.TrimSpace(body.Name))
		name = strings.ReplaceAll(name, " ", "-")
		writeJSON(w, map[string]any{"ok": true, "name": name, "path": path})
	})

	r.Post("/api/skills/delete", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if err := skillsmem.DeleteSkill(home, body.Name); err != nil {
			if errors.Is(err, skillsmem.ErrInvalidSkillName) {
				writeError(w, http.StatusBadRequest, "Invalid skill name")
				return
			}
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, http.StatusNotFound, "Skill not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to delete skill")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "name": body.Name})
	})

	r.Post("/api/skills/toggle", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			writeError(w, http.StatusBadRequest, "name and enabled are required")
			return
		}
		if _, err := skillsmem.FindSkillMD(home, body.Name); err != nil {
			writeError(w, http.StatusNotFound, "Skill not found")
			return
		}
		if err := skillsmem.ToggleSkill(home, body.Name, !body.Enabled); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to toggle skill")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "name": body.Name, "enabled": body.Enabled})
	})

	r.Post("/api/memory/write", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Section string `json:"section"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.Content == "" {
			writeError(w, http.StatusBadRequest, "section and content are required")
			return
		}
		path, err := skillsmem.WriteMemory(home, body.Section, body.Content)
		if err != nil {
			if errors.Is(err, skillsmem.ErrInvalidSection) {
				writeError(w, http.StatusBadRequest, "section must be \"memory\", \"user\", or \"soul\"")
				return
			}
			if errors.Is(err, skillsmem.ErrSymlinkedTarget) {
				writeError(w, http.StatusBadRequest, "Cannot write to a symlinked memory file")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to write memory")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "section": body.Section, "path": path})
	})
}
