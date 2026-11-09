package entry

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// serviceMachine is a home with this command and ropeshiftd beside it, and a service manager that
// answers every call.
func serviceMachine(t *testing.T, goos string, variables map[string]string) (services, *fake.ProcessRunner) {
	t.Helper()
	home := apitest.ShortDir(t)
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ropeshift", "ropeshiftd"} {
		if err := os.WriteFile(filepath.Join(bin, name), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runner := fake.NewProcessRunner()
	for _, tool := range []string{"/bin/launchctl", "/usr/bin/systemctl"} {
		runner.Install(tool, func(spec ports.ProcessSpec, _ <-chan struct{}) int {
			if spec.Args[0] == "print" {
				return 113
			}
			return 0
		})
	}
	return services{
		goos: goos, home: home, uid: 501, executable: filepath.Join(bin, "ropeshift"), runner: runner,
		lookup: lookupIn(variables), environ: []string{"HOME=" + home},
		tool: func(name string) (string, error) {
			if goos == "darwin" {
				return "/bin/" + name, nil
			}
			return "/usr/bin/" + name, nil
		},
	}, runner
}

func lookupIn(variables map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := variables[name]
		return value, ok
	}
}

// The agent runs ropeshiftd from beside this command with the daemon's flags, keeps the shell's PATH,
// locale, and ropeshift's location variables and no other, and writes its log in the installation's
// directory, which the install makes, because launchd makes no directory for a log.
func TestServiceInstallOnMacOS(t *testing.T) {
	s, runner := serviceMachine(t, "darwin", nil)
	state := filepath.Join(t.TempDir(), "state")
	s.lookup = lookupIn(map[string]string{
		"PATH": "/opt/bin:/usr/bin", "LANG": "en_US.UTF-8", "ANTHROPIC_API_KEY": "secret", "HOME": "elsewhere",
		"ROPESHIFT_STATE_DIR": state, "ROPESHIFT_WEB": "off",
	})
	installed, err := s.install(t.Context(), "", []string{"--source", "/repo", "--intake-tick", "5s"})
	if err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(s.home, "Library", "LaunchAgents", "dev.ropeshift.ropeshiftd.plist")
	if installed != (cli.Service{Name: "dev.ropeshift.ropeshiftd", Path: agent}) {
		t.Errorf("installed %+v", installed)
	}
	raw, err := os.ReadFile(agent)
	if err != nil {
		t.Fatal(err)
	}
	daemon := filepath.Join(filepath.Dir(s.executable), "ropeshiftd")
	for _, want := range []string{
		"<string>" + daemon + "</string>\n\t\t<string>solo</string>\n\t\t<string>--source</string>\n\t\t<string>/repo</string>\n\t\t<string>--intake-tick</string>\n\t\t<string>5s</string>\n",
		"<key>LANG</key>\n\t\t<string>en_US.UTF-8</string>\n\t\t<key>PATH</key>\n\t\t<string>/opt/bin:/usr/bin</string>\n\t\t<key>ROPESHIFT_STATE_DIR</key>\n\t\t<string>" + state + "</string>\n\t\t<key>ROPESHIFT_WEB</key>\n\t\t<string>off</string>\n\t</dict>",
		"<key>StandardOutPath</key>\n\t<string>" + state + "/dev.ropeshift.ropeshiftd.log</string>",
		"<key>ProcessType</key>\n\t<string>Interactive</string>",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the agent holds no %q:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "elsewhere") {
		t.Errorf("the agent holds a variable it must not:\n%s", raw)
	}
	if info, err := os.Stat(state); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the installation's directory: %v, %v", info, err)
	}
	got := runner.Spawned()
	if len(got) != 2 || got[1].Path != "/bin/launchctl" || !slices.Equal(got[1].Args, []string{"bootstrap", "gui/501", agent}) {
		t.Errorf("calls %+v", got)
	}
	if !slices.Equal(got[1].Env, s.environ) {
		t.Errorf("launchctl's environment %q", got[1].Env)
	}
}

func TestServiceInstallOnLinux(t *testing.T) {
	s, runner := serviceMachine(t, "linux", nil)
	xdgState := filepath.Join(t.TempDir(), "xdg-state")
	s.lookup = lookupIn(map[string]string{"PATH": "/usr/bin", "XDG_CONFIG_HOME": filepath.Join(s.home, "config"), "XDG_STATE_HOME": xdgState})
	installed, err := s.install(t.Context(), "solo-two", []string{"--source", "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(s.home, "config", "systemd", "user", "solo-two.service")
	if installed != (cli.Service{Name: "solo-two", Path: unit}) {
		t.Errorf("installed %+v", installed)
	}
	raw, err := os.ReadFile(unit)
	if err != nil {
		t.Fatal(err)
	}
	daemon := filepath.Join(filepath.Dir(s.executable), "ropeshiftd")
	for _, want := range []string{
		`ExecStart="` + daemon + `" "solo" "--source" "/repo"` + "\n",
		`Environment="PATH=/usr/bin"` + "\n" + `Environment="XDG_STATE_HOME=` + xdgState + `"` + "\n",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the unit holds no %q:\n%s", want, raw)
		}
	}
	var calls []string
	for _, spec := range runner.Spawned() {
		calls = append(calls, spec.Path+" "+strings.Join(spec.Args, " "))
	}
	want := []string{"/usr/bin/systemctl --user daemon-reload", "/usr/bin/systemctl --user enable solo-two.service", "/usr/bin/systemctl --user restart solo-two.service"}
	if !slices.Equal(calls, want) {
		t.Errorf("calls %q", calls)
	}
}

// A user unit with no XDG_CONFIG_HOME goes where the user manager looks by default.
func TestAUnitGoesInTheDefaultConfigDirectory(t *testing.T) {
	s, _ := serviceMachine(t, "linux", map[string]string{"PATH": "/usr/bin"})
	installed, err := s.install(t.Context(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(s.home, ".config", "systemd", "user", "ropeshiftd.service"); installed.Path != want || installed.Name != "ropeshiftd" {
		t.Errorf("installed %+v", installed)
	}
}

// Nothing is written for a service the daemon could not run.
func TestServiceInstallRefusesWhatCouldNotRun(t *testing.T) {
	tests := []struct {
		name  string
		svc   string
		args  []string
		usage bool
	}{
		{"a name with a slash", "a/b", nil, true},
		{"a name with a space", "a b", nil, true},
		{"a flag the daemon does not have", "", []string{"--no-such-flag"}, true},
		{"an argument the daemon does not take", "", []string{"extra"}, true},
		{"a source a service would read from no directory", "", []string{"--source", "repo"}, true},
		{"no ropeshiftd beside this command", "", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, runner := serviceMachine(t, "darwin", map[string]string{"PATH": "/usr/bin"})
			if !tt.usage {
				if err := os.Remove(filepath.Join(filepath.Dir(s.executable), "ropeshiftd")); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.install(t.Context(), tt.svc, tt.args)
			if err == nil || errors.Is(err, cli.ErrServiceUsage) != tt.usage {
				t.Errorf("err %v", err)
			}
			if spawned := runner.Spawned(); len(spawned) != 0 {
				t.Errorf("calls %+v", spawned)
			}
			if _, err := os.Stat(filepath.Join(s.home, "Library", "LaunchAgents")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("something was written: %v", err)
			}
		})
	}
}

func TestServiceUninstallRemovesWhatInstallWrote(t *testing.T) {
	s, _ := serviceMachine(t, "linux", map[string]string{"PATH": "/usr/bin"})
	installed, err := s.install(t.Context(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := s.uninstall(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if removed != installed {
		t.Errorf("removed %+v, installed %+v", removed, installed)
	}
	if _, err := s.uninstall(t.Context(), ""); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a second uninstall: %v", err)
	}
	if _, err := s.uninstall(t.Context(), "a/b"); !errors.Is(err, cli.ErrServiceUsage) {
		t.Errorf("a bad name: %v", err)
	}
}

func TestNoServiceOnAnotherPlatform(t *testing.T) {
	s, _ := serviceMachine(t, "plan9", map[string]string{"PATH": "/usr/bin"})
	if _, err := s.install(t.Context(), "", nil); err == nil {
		t.Error("installed on a platform with no service manager")
	}
}
