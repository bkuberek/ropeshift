package rules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Module is one module in the build list.
type Module struct {
	Path    string
	Version string
	Dir     string
}

// License is the classification of one module.
type License struct {
	Name    string
	Allowed bool
}

// licenseFile matches the names modules use for a license at their root.
var licenseFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|unlicense)([._-].*)?$`)

// A denied license is found first, because a restricted text can quote a
// permissive one. NFR-LIC-2 asks that every license allow hosted use.
var deniedLicenses = []struct{ name, phrase string }{
	{"SSPL", "server side public license"},
	{"BUSL", "business source license"},
	{"Elastic", "elastic license"},
	{"Commons-Clause", "commons clause"},
	{"FSL", "functional source license"},
	{"PolyForm", "polyform"},
	{"AGPL", "gnu affero general public license"},
}

// allowedLicenses are OSI-approved or public domain (NFR-LIC-1), and all of them
// allow hosted use. Every phrase in a row must appear.
var allowedLicenses = []struct {
	name    string
	phrases []string
}{
	{"Apache-2.0", []string{"apache license", "version 2.0"}},
	{"MPL-2.0", []string{"mozilla public license version 2.0"}},
	{"MIT", []string{"permission is hereby granted, free of charge", "shall be included in all copies"}},
	{"ISC", []string{"permission to use, copy, modify, and/or distribute this software for any purpose"}},
	{"BSD-3-Clause", []string{"redistribution and use in source and binary forms", "neither the name"}},
	{"BSD-3-Clause", []string{"redistribution and use in source and binary forms", "names of its contributors may not be used"}},
	{"BSD-2-Clause", []string{"redistribution and use in source and binary forms"}},
	{"Public-Domain", []string{"released into the public domain"}},
	{"Public-Domain", []string{"the author disclaims copyright to this source code"}},
}

// ClassifyModule reads the license files at a module's root. A module with no
// recognized license fails, so that an unknown text is a decision and not a pass.
func ClassifyModule(dir string) (License, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return License{}, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !licenseFile.MatchString(entry.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return License{}, err
		}
		name, allowed := classifyText(string(raw))
		if !allowed && name != "unknown" {
			return License{Name: name}, nil
		}
		if allowed && !slices.Contains(names, name) {
			names = append(names, name)
		}
		if !allowed {
			names = append(names, "unknown")
		}
	}
	if len(names) == 0 {
		return License{Name: "none"}, nil
	}
	if slices.Contains(names, "unknown") {
		return License{Name: "unknown"}, nil
	}
	return License{Name: strings.Join(names, " AND "), Allowed: true}, nil
}

var spaces = regexp.MustCompile(`\s+`)

func classifyText(text string) (string, bool) {
	normalized := spaces.ReplaceAllString(strings.ToLower(text), " ")
	for _, denied := range deniedLicenses {
		if strings.Contains(normalized, denied.phrase) {
			return denied.name, false
		}
	}
	for _, license := range allowedLicenses {
		matched := true
		for _, phrase := range license.phrases {
			matched = matched && strings.Contains(normalized, phrase)
		}
		if matched {
			return license.name, true
		}
	}
	return "unknown", false
}

// CheckLicenses returns one report line per module and a violation per module
// that fails the rule.
func CheckLicenses(modules []Module) (report []string, violations []Violation) {
	for _, module := range modules {
		license, err := ClassifyModule(module.Dir)
		if err != nil {
			report = append(report, fmt.Sprintf("%s %s ERROR", module.Path, module.Version))
			violations = append(violations, Violation{"license", module.Path, err.Error()})
			continue
		}
		report = append(report, fmt.Sprintf("%s %s %s", module.Path, module.Version, license.Name))
		if !license.Allowed {
			violations = append(violations, Violation{"license", module.Path, "license " + license.Name + " is not allowed"})
		}
	}
	return report, violations
}

// ListModules returns every module in the build list except the main one. A
// module whose source is not in the cache is downloaded, because the license
// rule reads its files.
func ListModules(root string) ([]Module, error) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	var modules []Module
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var entry struct {
			Path    string
			Version string
			Dir     string
			Main    bool
			Replace *struct{ Dir string }
		}
		err := decoder.Decode(&entry)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if entry.Main {
			continue
		}
		if entry.Replace != nil && entry.Replace.Dir != "" {
			entry.Dir = entry.Replace.Dir
		}
		if entry.Dir == "" {
			if entry.Dir, err = downloadModule(root, entry.Path, entry.Version); err != nil {
				return nil, err
			}
		}
		modules = append(modules, Module{Path: entry.Path, Version: entry.Version, Dir: entry.Dir})
	}
	return modules, nil
}

func downloadModule(root, path, version string) (string, error) {
	cmd := exec.Command("go", "mod", "download", "-json", path+"@"+version)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("download %s@%s: %w", path, version, err)
	}
	var result struct{ Dir string }
	if err := json.Unmarshal(out, &result); err != nil {
		return "", err
	}
	return result.Dir, nil
}
