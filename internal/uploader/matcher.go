package uploader

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

type IgnoreMatcher struct {
	rules []ignoreRule
}

type ignoreRule struct {
	raw      string
	negated  bool
	dirOnly  bool
	anchored bool
	segments []string
}

func NewIgnoreMatcher(patterns []string) (*IgnoreMatcher, error) {
	rules := make([]ignoreRule, 0, len(patterns))
	for _, raw := range patterns {
		if raw == "" {
			continue
		}
		rule, err := parseIgnoreRule(raw)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return &IgnoreMatcher{rules: rules}, nil
}

func parseIgnoreRule(raw string) (ignoreRule, error) {
	rule := ignoreRule{raw: raw}

	if strings.HasPrefix(raw, `\!`) || strings.HasPrefix(raw, `\#`) {
		raw = raw[1:]
	}
	if strings.HasPrefix(raw, "!") {
		rule.negated = true
		raw = raw[1:]
	}
	if raw == "" {
		return ignoreRule{}, fmt.Errorf("invalid ignore pattern %q", rule.raw)
	}
	if strings.HasSuffix(raw, "/") {
		rule.dirOnly = true
		raw = strings.TrimSuffix(raw, "/")
	}
	if strings.HasPrefix(raw, "/") {
		rule.anchored = true
		raw = strings.TrimPrefix(raw, "/")
	}
	if raw == "" {
		return ignoreRule{}, fmt.Errorf("invalid ignore pattern %q", rule.raw)
	}
	if !rule.anchored && raw != "**" && !strings.HasPrefix(raw, "**/") {
		raw = "**/" + raw
	}

	rule.segments = strings.Split(raw, "/")
	for _, segment := range rule.segments {
		if segment == "" {
			return ignoreRule{}, fmt.Errorf("invalid ignore pattern %q", rule.raw)
		}
		if segment == "**" {
			continue
		}
		if _, err := path.Match(segment, "x"); err != nil {
			return ignoreRule{}, fmt.Errorf("invalid ignore pattern %q: %w", rule.raw, err)
		}
	}
	return rule, nil
}

func (m *IgnoreMatcher) Ignored(relPath string, isDir bool) (bool, error) {
	relPath = filepath.ToSlash(strings.TrimPrefix(relPath, "./"))
	if relPath == "" || relPath == "." {
		return false, nil
	}
	segments := splitPath(relPath)
	ignored := false
	for _, rule := range m.rules {
		matched, err := rule.matches(segments, isDir)
		if err != nil {
			return false, err
		}
		if matched {
			ignored = !rule.negated
		}
	}
	return ignored, nil
}

func (r ignoreRule) matches(segments []string, isDir bool) (bool, error) {
	if r.dirOnly {
		if isDir {
			return matchSegments(r.segments, segments)
		}
		for i := 1; i < len(segments); i++ {
			matched, err := matchSegments(r.segments, segments[:i])
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	}
	return matchSegments(r.segments, segments)
}

func matchSegments(patternSegments, pathSegments []string) (bool, error) {
	if len(patternSegments) == 0 {
		return len(pathSegments) == 0, nil
	}
	if patternSegments[0] == "**" {
		for i := 0; i <= len(pathSegments); i++ {
			matched, err := matchSegments(patternSegments[1:], pathSegments[i:])
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	}
	if len(pathSegments) == 0 {
		return false, nil
	}
	matched, err := path.Match(patternSegments[0], pathSegments[0])
	if err != nil {
		return false, err
	}
	if !matched {
		return false, nil
	}
	return matchSegments(patternSegments[1:], pathSegments[1:])
}

func splitPath(relPath string) []string {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	out := parts[:0]
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		out = append(out, part)
	}
	return out
}
