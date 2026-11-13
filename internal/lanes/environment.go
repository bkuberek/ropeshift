package lanes

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
)

// TagPrefix starts every variable ropeshift sets on a lane. A lane's children
// inherit these tags and nothing else from ropeshift.
const TagPrefix = "ROPESHIFT_"

// SecretRefEnv is the form of a secret reference that names an environment variable
// (D9). The allowlist passes it through, so no code path reads its value for use.
const SecretRefEnv = "env:"

// EnvComposeProject names a lane's compose project. It is the one variable a lane gets
// from ropeshift that carries no tag, because compose reads it by this name.
const EnvComposeProject = "COMPOSE_PROJECT_NAME"

// inherited is everything a lane takes from the process that started it. A session
// started inside another session inherits that session's markers, so anything not
// named here stays out (FR-DRV-10).
var inherited = []string{
	"HOME", "USER", "LOGNAME", "PATH", "TERM", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR", "TZ",
}

// loginShells are the programs a spawn must not go through. A login shell reads the
// user's profile files, which would put back what the allowlist took out.
var loginShells = []string{"sh", "bash", "zsh", "dash", "ksh", "fish", "csh", "tcsh", "login"}

// Spawn and environment refusals.
var (
	// ErrSessionVariable means a reference names a variable that marks or
	// authenticates an agent session, which no lane may inherit.
	ErrSessionVariable = errors.New("a session variable cannot pass to a lane")
	// ErrForgeCredential means a reference names a variable through which the forge's CLI reads a
	// token or its configuration, which no lane may have (ADR-0020).
	ErrForgeCredential = errors.New("a forge credential cannot pass to a lane")
	// ErrBadReference means a secret reference has no form this build reads.
	ErrBadReference = errors.New("bad secret reference")
	// ErrMissingVariable means a reference names a variable that is not set.
	ErrMissingVariable = errors.New("a referenced variable is not set")
	// ErrBadTag means a tag does not carry ropeshift's prefix.
	ErrBadTag = errors.New("bad lane tag")
	// ErrShellSpawn means a spawn would go through a shell.
	ErrShellSpawn = errors.New("a spawn goes directly to its binary")
	// ErrRelativePath means a spawn names no absolute binary, so PATH would choose
	// what runs.
	ErrRelativePath = errors.New("a spawn names an absolute path")
)

// sessionVariable reports a name that belongs to an agent session, to the member who could
// approve what one asks (D26), or to a delegate acting for that member (ADR-0024).
func sessionVariable(name string) bool {
	return name == "CLAUDECODE" || strings.HasPrefix(name, "CLAUDE_CODE_") || name == authority.EnvMemberToken || name == authority.EnvDelegateToken
}

// EnvForgeConfig is where the forge's CLI reads its configuration and its sign-in
// (https://cli.github.com/manual/gh_help_environment). A lane's points at an empty directory of its own.
const EnvForgeConfig = "GH_CONFIG_DIR"

// forgeVariable reports a name through which `gh` reads a token or its configuration. A lane never gets
// one from a profile, and the allowlist already leaves each out.
func forgeVariable(name string) bool {
	switch name {
	case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", EnvForgeConfig:
		return true
	}
	return false
}

// EnvironmentSpec is what a lane's environment is built from.
type EnvironmentSpec struct {
	// Lookup reads the process's environment.
	Lookup func(name string) (string, bool)
	// SecretRefs are the profile's references. An `env:` reference adds its one
	// variable to the allowlist.
	SecretRefs []string
	// Tags are ropeshift's own variables.
	Tags map[string]string
	// Allocation is what this lane holds beside its worktree. Empty fields add nothing.
	Allocation Allocation
	// ForgeConfigDir is the empty directory the lane's `gh` reads its configuration from, so the person's
	// sign-in is not there to use. Empty sets nothing.
	ForgeConfigDir string
}

// BuildEnvironment returns the whole environment of a lane, sorted so that a spec
// always gives the same bytes. Nothing reaches a lane except what this returns.
func BuildEnvironment(spec EnvironmentSpec) ([]string, error) {
	values := map[string]string{}
	for _, name := range inherited {
		if value, ok := spec.Lookup(name); ok {
			values[name] = value
		}
	}
	for _, ref := range spec.SecretRefs {
		name, value, err := passThrough(ref, spec.Lookup)
		if err != nil {
			return nil, err
		}
		if name != "" {
			values[name] = value
		}
	}
	for name, value := range spec.Tags {
		if !strings.HasPrefix(name, TagPrefix) {
			return nil, fmt.Errorf("%w: %s", ErrBadTag, name)
		}
		if sessionVariable(name) {
			return nil, fmt.Errorf("%w: %s", ErrSessionVariable, name)
		}
		values[name] = value
	}
	if spec.Allocation.Ports > 0 {
		values[EnvPortBase] = strconv.FormatInt(spec.Allocation.PortBase, 10)
	}
	if spec.Allocation.ComposeProject != "" {
		values[EnvComposeProject] = spec.Allocation.ComposeProject
	}
	if spec.ForgeConfigDir != "" {
		values[EnvForgeConfig] = spec.ForgeConfigDir
	}
	out := make([]string, 0, len(values))
	for name, value := range values {
		out = append(out, name+"="+value)
	}
	slices.Sort(out)
	return out, nil
}

// passThrough reads one `env:` reference. Other forms bind to the CLI in the driver,
// so they add nothing here.
func passThrough(ref string, lookup func(string) (string, bool)) (string, string, error) {
	name, isEnv := strings.CutPrefix(ref, SecretRefEnv)
	if !isEnv {
		if strings.Contains(ref, ":") {
			return "", "", nil
		}
		return "", "", fmt.Errorf("%w: %q", ErrBadReference, ref)
	}
	if name == "" {
		return "", "", fmt.Errorf("%w: %q", ErrBadReference, ref)
	}
	if sessionVariable(name) {
		return "", "", fmt.Errorf("%w: %s", ErrSessionVariable, name)
	}
	if forgeVariable(name) {
		return "", "", fmt.Errorf("%w: %s", ErrForgeCredential, name)
	}
	value, ok := lookup(name)
	if !ok {
		return "", "", fmt.Errorf("%w: %s", ErrMissingVariable, name)
	}
	return name, value, nil
}

// CheckSpawn refuses a spawn that is not a direct one.
func CheckSpawn(binary string) error {
	if !path.IsAbs(binary) {
		return fmt.Errorf("%w: %q", ErrRelativePath, binary)
	}
	if slices.Contains(loginShells, path.Base(binary)) {
		return fmt.Errorf("%w: %s", ErrShellSpawn, binary)
	}
	return nil
}

// ShellQuote makes a value one word of a shell command. The agent CLI runs a generated
// command through a shell, so a state directory with a space in it needs this.
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
