package core

import (
	"os"
	"path/filepath"
	"strings"
)

// FileExists returns true if path exists and is a regular file.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func ResolveLocalFile(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if FileExists(path) {
		return path
	}
	base := filepath.Base(path)
	cands := []string{base, filepath.Join(".", base)}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, base), filepath.Join(wd, path))
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		cands = append(cands, filepath.Join(dir, base), filepath.Join(dir, path))
	}
	for _, p := range cands {
		if FileExists(p) {
			return p
		}
	}
	return path
}

func AppendResult(filename, text string) {
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(text + "\n")
}

func ParseKeywords(input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	raw := strings.FieldsFunc(input, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	})
	var out []string
	for _, k := range raw {
		k = strings.TrimSpace(k)
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}
