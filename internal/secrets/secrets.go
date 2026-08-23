package secrets

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

type Finding struct {
	Path   string
	Reason string
}

func Scan(root string) ([]Finding, error) {
	var findings []Finding
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative == ".git" {
				return filepath.SkipDir
			}
			if relative != "." && strings.EqualFold(entry.Name(), "secrets") {
				findings = append(findings, Finding{Path: filepath.ToSlash(relative) + "/", Reason: "secrets directory"})
				return filepath.SkipDir
			}
			return nil
		}
		if reason := sensitiveReason(entry.Name()); reason != "" {
			findings = append(findings, Finding{Path: filepath.ToSlash(relative), Reason: reason})
		}
		return nil
	})
	sort.Slice(findings, func(i, j int) bool { return findings[i].Path < findings[j].Path })
	return findings, err
}

func sensitiveReason(name string) string {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".example", ".sample", ".template"} {
		if strings.HasSuffix(lower, suffix) {
			return ""
		}
	}
	if lower == ".env" || strings.HasPrefix(lower, ".env.") {
		return "environment file"
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx"} {
		if strings.HasSuffix(lower, suffix) {
			return "private credential file"
		}
	}
	if strings.HasPrefix(lower, "credentials.") {
		return "credentials file"
	}
	return ""
}
