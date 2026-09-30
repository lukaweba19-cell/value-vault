// Package proxy implements the saved proxy-list store (files and server URLs)
// plus the HTTP handlers behind /api/proxy-lists and /api/proxy-fetch.
package proxy

import (
	"encoding/json"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"strings"

	"xbox-checker/internal/core"
)

const SessionsDir = "sessions"

func listsFile() string {
	dir := filepath.Join(SessionsDir, "proxies")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "saved.json")
}

type SavedList struct {
	Name  string   `json:"name"`
	Path  string   `json:"path"`
	Type  string   `json:"type"`
	URLs  []string `json:"urls,omitempty"`
	Count int      `json:"count"`
}

func loadLists() []SavedList {
	b, err := os.ReadFile(listsFile())
	if err != nil {
		return nil
	}
	var out []SavedList
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func saveLists(list []SavedList) {
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(listsFile(), b, 0644)
}

func HandleLists(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == stdhttp.MethodPost {
		var req SavedList
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			stdhttp.Error(w, err.Error(), 400)
			return
		}
		req.Path = strings.TrimSpace(req.Path)
		if req.Type == "" {
			if len(req.URLs) > 0 {
				req.Type = "server"
			} else {
				req.Type = "file"
			}
		}
		if req.Type == "server" {
			var urls []string
			for _, u := range req.URLs {
				u = strings.TrimSpace(u)
				if u != "" {
					urls = append(urls, u)
				}
			}
			req.URLs = urls
			if len(req.URLs) == 0 {
				stdhttp.Error(w, "no proxy URLs", 400)
				return
			}
			if px, err := FetchFromURLs(req.URLs); err == nil {
				req.Count = len(px)
			}
		} else if req.Path == "" {
			stdhttp.Error(w, "no proxy path", 400)
			return
		} else {
			if p := core.ResolveLocalFile(req.Path); p != "" {
				req.Path = p
			}
			if px, err := Load(req.Path); err == nil {
				req.Count = len(px)
				EnsurePool(req.Path, px)
			}
		}
		if req.Name == "" {
			if req.Path != "" {
				req.Name = filepath.Base(req.Path)
			} else {
				req.Name = "proxy-server"
			}
		}
		list := loadLists()
		found := false
		for i := range list {
			if strings.EqualFold(list[i].Path, req.Path) || strings.EqualFold(list[i].Name, req.Name) {
				list[i] = req
				found = true
				break
			}
		}
		if !found {
			list = append(list, req)
		}
		saveLists(list)
		_ = json.NewEncoder(w).Encode(list)
		return
	}
	if r.Method == stdhttp.MethodDelete {
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		list := loadLists()
		out := list[:0]
		for _, x := range list {
			if name != "" && strings.EqualFold(x.Name, name) {
				continue
			}
			if path != "" && strings.EqualFold(x.Path, path) {
				continue
			}
			if name == "" && path == "" {
				out = append(out, x)
				continue
			}
			out = append(out, x)
		}
		saveLists(out)
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	list := loadLists()
	for i := range list {
		if list[i].Type == "server" {
			continue
		}
		if p := strings.TrimSpace(list[i].Path); p != "" {
			if px, err := Load(p); err == nil {
				list[i].Count = len(px)
			}
		}
	}
	_ = json.NewEncoder(w).Encode(list)
}

func HandleFetch(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req struct {
		URLs []string `json:"urls"`
		Path string   `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var (
		px  []string
		err error
	)
	if p := strings.TrimSpace(req.Path); p != "" {
		if resolved := core.ResolveLocalFile(p); resolved != "" {
			p = resolved
		}
		px, err = Load(p)
	} else {
		px, err = FetchFromURLs(req.URLs)
	}
	if err != nil {
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "count": len(px)})
}
