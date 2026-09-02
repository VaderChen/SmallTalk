package main

import "strings"

func splitPathFromBase(path string, base string) []string {
	path = strings.TrimSpace(path)
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = "/"
	}
	if !strings.HasPrefix(path, base) {
		return nil
	}
	s := strings.TrimPrefix(path, base)
	s = strings.Trim(s, "/")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
