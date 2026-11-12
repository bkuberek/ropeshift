// Package rules holds the checks that keep the module's shape honest: dependency
// direction (ADR-0003), a fake for every port (NFR-TEST-1), and the license rule
// (NFR-LIC-1, NFR-LIC-2). The checks read source and module metadata, so a rule
// breaks a test and not a convention.
package rules

import "strings"

// Layer is where a package sits in the hexagon.
type Layer string

// The layers of the module layout.
const (
	LayerMain         Layer = "main"
	LayerEntry        Layer = "entry"
	LayerDomain       Layer = "domain"
	LayerPorts        Layer = "ports"
	LayerApplication  Layer = "application"
	LayerAdapter      Layer = "adapter"
	LayerAPI          Layer = "api"
	LayerAPIServer    Layer = "api-server"
	LayerClient       Layer = "client"
	LayerSupport      Layer = "support"
	LayerUnclassified Layer = "unclassified"
)

// domainPackages are the top-level names that scream the domain.
var domainPackages = map[string]bool{
	"authority":    true,
	"contextpacks": true,
	"lanes":        true,
	"ledger":       true,
	"measures":     true,
	"messages":     true,
	"parking":      true,
	"planning":     true,
	"questions":    true,
	"roster":       true,
	"workorders":   true,
}

// clientPackages reach the ledger only through the API or the worker's socket.
var clientPackages = map[string]bool{
	"cli":       true,
	"hook":      true,
	"mcpserver": true,
	"shim":      true,
	"top":       true,
}

// Classify names the layer of a package from its path inside the module.
func Classify(rel string) Layer {
	parts := strings.Split(rel, "/")
	if parts[0] == "cmd" {
		return LayerMain
	}
	if parts[0] != "internal" || len(parts) < 2 {
		return LayerUnclassified
	}
	name := parts[1]
	switch {
	case domainPackages[name]:
		return LayerDomain
	case clientPackages[name]:
		return LayerClient
	}
	switch name {
	case "entry":
		return LayerEntry
	case "ports":
		return LayerPorts
	case "control", "worker":
		return LayerApplication
	case "adapters":
		return LayerAdapter
	case "api":
		if len(parts) > 2 && parts[2] == "server" {
			return LayerAPIServer
		}
		return LayerAPI
	case "crashpoint", "version":
		return LayerSupport
	}
	return LayerUnclassified
}

// pureStandardLibrary lists the standard packages that reach no process, file,
// network, clock source, or entropy source. Domain and port packages may import
// only these, so the domain cannot reach a model, a process, or a database.
var pureStandardLibrary = map[string]bool{
	"bytes":           true,
	"cmp":             true,
	"context":         true,
	"crypto/hmac":     true,
	"crypto/sha256":   true,
	"encoding/base32": true,
	"encoding/hex":    true,
	"encoding/json":   true,
	"errors":          true,
	"fmt":             true,
	"hash":            true,
	"io":              true,
	"iter":            true,
	"maps":            true,
	"math":            true,
	"path":            true,
	"regexp":          true,
	"slices":          true,
	"sort":            true,
	"strconv":         true,
	"strings":         true,
	"time":            true,
	"unicode":         true,
	"unicode/utf8":    true,
}
