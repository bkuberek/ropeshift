# Screenshots

`make screenshots` captures every view of the web UI, and `ropeshift top`, on a sample project. The README and the website use these images. The target never runs in `make check`, starts no real agent session, and needs no API key.

## Run it

You need what `make check` needs, plus tmux, because the sample's planning session runs in a pane. The first run installs the web packages and Playwright's Chromium when they are missing.

```sh
make screenshots
```

The images go to `.scratch/shots/<version>/`, which git ignores. Three variables change the run:

| Variable | What it does |
| --- | --- |
| `SHOTS_OUT` | The directory the images go to |
| `SHOTS_REF` | A commit or tag to build in place of this checkout, such as `v0.1.0-alpha.13`. Fetch it first |
| `SHOTS_KEEP=1` | Keeps the isolated installation after the run, for debugging. Its path is in the log |

```sh
git fetch origin tag v0.1.0-alpha.13
make screenshots SHOTS_REF=v0.1.0-alpha.13 SHOTS_OUT=/tmp/shots
```

The capture code always comes from this checkout, so a ref much older than it may lack a command or a view the sample uses.

## What it does

[scripts/screenshots.sh](../scripts/screenshots.sh) builds the web UI and the binary, stamped with the version `git describe` gives. It then runs [test/screenshots](../test/screenshots) with the `screenshots` build tag, which:

1. Makes an isolated installation under a new temporary directory: its own home, state, runtime, and configuration directories, so it never reads or writes your own installation, its daemon, or its port.
2. Starts the daemon from that installation, with the web UI on a loopback port the kernel chooses and the terminal server on a socket inside it.
3. Lays out the sample through the binary's command line: two projects, three repositories with local remotes behind a neutral address, a plan with milestones and contracts approved and a second version waiting, decisions and assumptions, lanes in each state, reviews with verdicts, questions an orchestrator takes up the ladder, a delegate's confirmation cards, a planning session, and a conversation with the assistant. Every session is the fake agent CLI from [test/doubles](../test/doubles), playing a script in [fixture_test.go](../test/screenshots/fixture_test.go).
4. Runs [web/scripts/screenshots.mjs](../web/scripts/screenshots.mjs), which opens each view in Playwright's Chromium and waits for its data and fonts before each image.
5. Stops the daemon, the lanes, and the terminal server, ends anything else still running from the installation, and removes it.

## The images

Each view is captured at 1440 by 900 and at 390 by 844, in the light and the dark theme, at twice the pixel density, as `<view>-<theme>-<width>.png`. The list of views and the records each one opens is in [screenshots_test.go](../test/screenshots/screenshots_test.go). The run also writes `top.txt`, what `ropeshift top --once` prints, and `top.png`, the same text as a terminal shows it.

The run fails a view whose text shows the installation's temporary path, the fake agent CLI's, or this machine's home directory, user name, or host name, and keeps its image so you can see where. The planning session's pane shows the fake agent CLI's own screen. Look at every image before it is published.
