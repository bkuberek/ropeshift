# Get started with the web UI

This guide sets up ropeshift on one machine, macOS or Linux, and signs you in to its web UI. A few steps need a terminal; after them, you work in the web UI. At the end you have a running service, a worker whose agent CLI is usable, and a first look around the web UI.

## What needs a terminal

| Step | Why it needs a terminal | Where the web UI shows it afterwards |
|------|-------------------------|--------------------------------------|
| Install ropeshift, git, and tmux | A package manager installs software | None |
| Install Claude Code and sign in | Ropeshift never reads, stores, or relays agent credentials, so Claude Code signs in through Anthropic's own flow | Workers and resources, Drivers |
| `ropeshift init` | The first passphrase comes only from a terminal, and setting up the machine is a command | Sign in, with that passphrase |
| Start the service | It acts on the machine's service manager | The web UI at `http://127.0.0.1:7317` |
| `loginctl enable-linger`, on Linux | A setting of the service manager | None |

## In a terminal

### 1. Install ropeshift

Install a release with Homebrew:

```sh
brew install bkuberek/tap/ropeshift
```

Or with the install script, which installs to `~/.local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/bkuberek/ropeshift/main/install.sh | sh
```

The README's [Install](../README.md#install) section lists the `.deb` and `.rpm` packages. Check the installation:

```sh
ropeshift version
```

It prints one line: the release, the commit it was built from, when it was built, the Go version, and the platform.

```text
ropeshift <version> (commit <commit>, built <date>, <go version>, <os>/<arch>)
```

### 2. Install git and tmux

Ropeshift works in your git repositories, and runs a planning session in a tmux pane. On macOS:

```sh
brew install git tmux
```

On Linux, install both from your distribution's packages, such as `sudo apt install git tmux`.

### 3. Install Claude Code and sign in

Install Claude Code, then run it and sign in through [Anthropic's own flow](https://code.claude.com/docs/en/authentication):

```sh
claude
```

Check the sign-in:

```sh
claude auth status
```

It says whether you are signed in, and how. A machine signed in with a Claude subscription starts at the `attended` level; read [Claude subscription accounts](../README.md#claude-subscription-accounts) before you go on.

### 4. Set up the machine

```sh
ropeshift init
```

It asks for a new passphrase twice. You sign in to the web UI with it, and a change you make from a terminal asks for it. On a machine signed in with a subscription, it prints:

```text
Daemon: started for this run, and stopped at its end.
New passphrase:
Again:
Passphrase: set. A change asks for it on the terminal, and eval "$(ropeshift login)" holds a session in one shell.
Worker: registered as <worker-id>.
Agent CLIs:
  claude-code <version>: signed in with a subscription. This worker starts at attended, so an orchestrator claims work only inside a run window you open. Anthropic's Consumer Terms restrict automated use of a subscription, and no vendor has approved this use. Read docs/onboarding.md, and ropeshift worker risk prints what a member acknowledges before this worker runs unattended.
Ropeshift never reads, stores, relays, or proxies an agent CLI's credentials. Each CLI keeps its own sign-in.
Sample contract: wrote <installation directory>/sample-contract.md.
  Copy it into a repository, change it to your own work, and commit it, and intake reads it.
Before the first lane:
  Lanes run as the same operating system user as you, so nothing on this machine separates an agent
  from your files, your browser, or a session you hold. A passphrase raises the cost of an agent
  approving its own request, and does not make it impossible.
  A repository's own settings and hooks run in its lanes. Run lanes on work from outside your team
  only behind another operating system user, a container, a virtual machine, or another machine.
  docs/onboarding.md and docs/authorization.md say more.
Next: start the daemon with ropeshiftd solo --source <repository>.
```

- `Worker: registered as <worker-id>` names this machine's worker.
- Under `Agent CLIs:`, a line that says `not installed` or `not signed in` says what to do. Do it, and run `ropeshift init` again. A machine signed in with an API key starts at `unattended` instead. A Claude Code version ropeshift has not qualified adds a sentence saying the worker checks it before its first lane.
- It exits 0 once an agent CLI is usable.

The last line names a daemon in the foreground. This guide runs the daemon as a service instead, in the next step.

### 5. Start the service

With a Homebrew install:

```sh
brew services start ropeshift
```

With any other install:

```sh
ropeshift service install
```

It writes the service's definition for launchd on macOS or systemd on Linux, starts the daemon, and prints the service's name and its file. Use one of the two, never both: two daemons for one installation conflict. [Run the daemon as a service](onboarding.md#run-the-daemon-as-a-service) says more.

On Linux, turn lingering on once, so the daemon and its lanes keep running after you log out:

```sh
loginctl enable-linger "$USER"
```

The daemon serves the web UI at `http://127.0.0.1:7317`, on this machine only.

## In the web UI

### 1. Open the web UI

Open `http://127.0.0.1:7317` in a browser on the same machine. You see the Sign in page, with Passphrase.

![The Sign in page, with the Passphrase field and the Sign in button](assets/screenshots/get-started-01-light.png#gh-light-mode-only)
![The Sign in page, with the Passphrase field and the Sign in button](assets/screenshots/get-started-01-dark.png#gh-dark-mode-only)

### 2. Sign in

Enter the passphrase you set with `ropeshift init`, and select Sign in. You see Needs you, with nothing waiting. The project switcher at the top shows Default, the project every installation starts with.

![Needs you with nothing waiting, the project switcher on Default, and the sidebar](assets/screenshots/get-started-02-light.png#gh-light-mode-only)
![Needs you with nothing waiting, the project switcher on Default, and the sidebar](assets/screenshots/get-started-02-dark.png#gh-dark-mode-only)

### 3. Check the worker

In the sidebar, under Installation, select Workers and resources. Machines lists this machine, with its state, its lanes against its cap, and its measures. Drivers shows `claude-code` with its result, Usable; its version; whether ropeshift has qualified that version; and its sign-in, such as `subscription signed in`.

![Workers and resources, with this machine under Machines and claude-code under Drivers](assets/screenshots/get-started-03-light.png#gh-light-mode-only)
![Workers and resources, with this machine under Machines and claude-code under Drivers](assets/screenshots/get-started-03-dark.png#gh-dark-mode-only)

### 4. Find the worker's level

In the sidebar, under Project, select Settings, then Autonomy and run windows. The worker's level is `attended` on a subscription: it claims work only inside a run window a member opens, in the Run window panel. Read the stated risk is there until a member acknowledges it.

![Autonomy and run windows, with the worker at attended and Read the stated risk](assets/screenshots/get-started-04-light.png#gh-light-mode-only)
![Autonomy and run windows, with the worker at attended and Read the stated risk](assets/screenshots/get-started-04-dark.png#gh-dark-mode-only)

### 5. Read the stated risk

Select Read the stated risk. The stated risk says what running automated sessions on this sign-in means. If you stand behind it, select I stand behind it: the panel says the stated risk is acknowledged for this worker. The worker stays at `attended` until you choose another level, and it runs `unattended` only once a member has acknowledged the risk.

![The stated risk, with Cancel and I stand behind it](assets/screenshots/get-started-05-light.png#gh-light-mode-only)
![The stated risk, with Cancel and I stand behind it](assets/screenshots/get-started-05-dark.png#gh-dark-mode-only)

### 6. Open the command palette

Press `Ctrl K`, or `⌘ K` on macOS, or select Search or run a command at the top. The command palette searches views, records, and actions. With nothing typed, it lists the actions of the page you are on, such as Switch project, Create project, and Start orchestrator. Type to narrow the list, start with `>` for actions only or `#` for records only, and press `Enter` to open one. `Esc` closes it.

![The command palette open over Needs you](assets/screenshots/get-started-06-light.png#gh-light-mode-only)
![The command palette open over Needs you](assets/screenshots/get-started-06-dark.png#gh-dark-mode-only)

### 7. Show the keyboard shortcuts

Press `?`. The sheet lists every shortcut: the ones that work anywhere, `G` then a letter to go to a view, and the keys of lists and decisions. The Single-key shortcuts switch turns off the shortcuts of one key, such as `P` and `G`. `Esc` closes the sheet.

![The keyboard shortcuts sheet](assets/screenshots/get-started-07-light.png#gh-light-mode-only)
![The keyboard shortcuts sheet](assets/screenshots/get-started-07-dark.png#gh-dark-mode-only)

## A tour of the frame

Every view sits in the same frame, as the window in step 2 shows.

- The top bar holds the project switcher, which `P` opens; Search or run a command, the palette; Needs you, with the count of what waits for you in every project; the conversation panel, which `.` opens and closes; the theme; and your account, with Sign out.
- The sidebar puts Needs you and Overview first, then the project's views in groups: Plan (Roadmap, Plans, Planning, Documents, Decisions, Open questions), Work (Work orders, Floor, Reviews, Measures), Talk (Conversations), and Project (Repositories, Agents, Settings, Audit). Installation (Projects, Workers and resources, Members, Usage) belongs to the whole installation.
- The theme follows the system's light or dark setting. The theme button, and Theme at the foot of the sidebar, switch between the system's setting, light, and dark.
- The status bar at the bottom shows whether the view is live, the lanes running against the machine's cap, the worker's measures, and the estimated cost. Its right end names the version the daemon runs, and Settings, About gives its version, commit, and build date.

## Next

Set up your projects, the next guide in [Learn ropeshift](../README.md#learn-ropeshift).
