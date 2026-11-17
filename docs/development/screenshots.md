# Screenshots

Three targets make and publish the images of the web UI and of `ropeshift top`. The README, the user docs, and the website use them. None of them runs in `make check`, starts a real agent session, or needs an API key.

| Target | What it does |
| --- | --- |
| `make screenshots` | Captures every view of the web UI, and `ropeshift top`, on a sample project |
| `make guide-shots` | Walks the guides' steps through the web UI and captures the images the guides show |
| `make screenshots-publish` | Optimizes captures and copies the published ones into `docs/assets/screenshots/` |

A test holds the user docs to their images: `go test ./test/rules` fails for a missing image, a file that is not a lowercase `.png` under `docs/assets/`, an image over 250 KB, a light image without its dark pair, a published image no doc shows or this page does not list, a relative link or anchor that does not resolve, a link to the repository's files on `github.com`, and a doc that calls the product anything but fair source.

## Run it

You need what `make check` needs, plus tmux, because the sample's planning session runs in a pane. The first run installs the web packages and Playwright's Chromium when they are missing. The kit keeps Playwright's browsers in `.scratch/playwright` in the checkout, which git ignores, and never in the cache every project on the machine shares, unless `PLAYWRIGHT_BROWSERS_PATH` already names another directory. To use a Chrome or Chromium you have in place of Playwright's, set `ROPESHIFT_TEST_CHROME` to its executable, as [web tests](web-tests.md#the-browser) say.

```sh
make screenshots
```

The images go to `.scratch/shots/<version>/`, which git ignores. Three variables change the run, and `make guide-shots` takes them too:

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

[scripts/screenshots.sh](../../scripts/screenshots.sh) builds the web UI and the binary, stamped with the version `git describe` gives, with the `screenshots` build tag, which reports [a fixed machine](#the-fixed-machine). It then runs [test/screenshots](../../test/screenshots) with the same tag, which:

1. Makes an isolated installation under a new temporary directory: its own home, state, runtime, configuration, and worktree directories, named outright with `ROPESHIFT_STATE_DIR` and `ROPESHIFT_WORKTREE_DIR`, so it never reads or writes your own installation, its daemon, or its port.
2. Starts the daemon from that installation, with the web UI on a loopback port the kernel chooses and the terminal server on a socket inside it.
3. Lays out the sample through the binary's command line: two projects, three repositories with local remotes behind a neutral address, a plan with milestones and contracts approved and a second version waiting, decisions and assumptions, lanes in each state, reviews with verdicts, questions an orchestrator takes up the ladder, a delegate's confirmation cards, a planning session, and a conversation with the assistant. Every session is the fake agent CLI from [test/doubles](../../test/doubles), playing a script in [fixture_test.go](../../test/screenshots/fixture_test.go).
4. Runs [web/scripts/screenshots.mjs](../../web/scripts/screenshots.mjs), which opens each view in Playwright's Chromium and waits for its data and fonts before each image.
5. Stops the daemon, the lanes, and the terminal server, ends anything else still running from the installation, and removes it.

### The fixed machine

The binary under capture reports one fixed machine in place of the one it runs on, so every image shows the same figures and nothing of the machine that captured it. The Workers view, the status bar, and `ropeshift top` all read it:

| Measure | What it reports |
| --- | --- |
| Cores | 10 |
| Load | 1.84, 1.62, and 1.47 over one, five, and fifteen minutes |
| Memory | 9.6 GiB used of 32 GiB, 22.4 GiB available |
| Free disk | 412 GiB of 926 GiB on each watched volume |
| CPU use and memory pressure | Unavailable, as on macOS |
| Worker ID | `3f9a2c7e5b1d4086a4e2c8f06d9b1e57` |

Admission reads the same machine. [figures_screenshots.go](../../internal/adapters/machine/figures_screenshots.go) and [identity_screenshots.go](../../internal/worker/identity_screenshots.go) hold it, and only the `screenshots` build tag compiles them. `go test ./test/rules` fails when a normal build holds either, so a release reports the machine it runs on. A ref built with `SHOTS_REF` from before the tag existed reports its own machine.

## The view captures

Each view is captured at 1440 by 900 and at 390 by 844, in the light and the dark theme, at twice the pixel density, as `<view>-<theme>-<width>.png`. The list of views and the records each one opens is in [screenshots_test.go](../../test/screenshots/screenshots_test.go). The run also writes `top.txt`, what `ropeshift top --once` prints, and `top.png`, the same text as a terminal shows it, at one pixel to the pixel.

The run fails a view whose text shows the installation's temporary path, the fake agent CLI's, or this machine's home directory, user name, or host name, and keeps its image so you can see where. The planning session's pane shows the fake agent CLI's own screen. Look at every image before it is published.

## The guide walk

The guides show their steps in images, and the walk makes them by pressing the controls a guide names. A step that fails fails the run, so a guide that the walk captures is a guide whose steps work.

```sh
make guide-shots
make guide-shots GUIDE=get-started
make guide-shots CHECKS=1
```

The first walks and captures every guide. The second captures one guide, after walking the guides before it in its world without capturing them, because each guide starts from the state the ones before it left. The third runs the checks of [the product gaps](#the-product-gap-checks) in place of a capture.

### Worlds

A guide's world is the installation its walk starts from. Each theme runs its own installation, so every act of a walk happens once per theme.

| World | What it holds |
| --- | --- |
| `harbor` | A fresh installation: the first project, a connected worker at `attended`, the fake agent CLI signed in with a subscription, and two clones, `harbor-api` and `harbor-web`, on disk and registered nowhere. Guides 1 to 7 walk it as one story |
| `sample` | The sample project of `make screenshots`, for the guides that read a project with history |

Go prepares only what the web UI cannot do: the clones, the fake agent CLI's scripted turns, and the installation. Everything a guide tells a person to do in the web UI, the walk does in the web UI.

The harbor clones live in one fixed, neutral folder, `/tmp/ropeshift-guides/src`, so a field that shows a path shows that one and the private-word check can stay on for every image. The kit creates the folder, removes it at the end of the run, and refuses to start while it exists, which another walk, or one that was killed, leaves. Remove it and run again.

### The step format

A guide is one file, `test/screenshots/guides/<guide>.json`, named for the prefix of its images. [guides_format_test.go](../../test/screenshots/guides_format_test.go) checks the format on every `go test`, so a malformed step fails `make check` and not a capture run.

```json
{
  "guide": "get-started",
  "title": "Get started with the web UI",
  "world": "harbor",
  "order": 1,
  "signed_out": true,
  "steps": [
    {
      "step": "sign in with the passphrase",
      "route": "/",
      "actions": [
        { "fill": { "label": "Passphrase", "value": "{{passphrase}}" } },
        { "click": { "role": "button", "name": "Sign in" } },
        { "waitFor": { "text": "Nothing needs you" } }
      ],
      "capture": { "name": "get-started-02", "clip": "window" }
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `guide` | The guide's prefix, which names the file and begins each image's name |
| `world` | `harbor` or `sample` |
| `order` | The guide's place among those of its world, starting at 1 |
| `signed_out` | Starts the guide with no session, so its first steps sign in through the page. Any other guide starts signed in |
| `step` | The step's name, unique in the guide, which a failed run says |
| `route` | An optional path to open before the actions. A step with no route continues on the page the one before it left |
| `actions` | The actions of the step, in order |
| `capture` | An optional image: `name` is `<guide>-NN`, and `clip` says what it shows |

An action is exactly one of four:

| Action | What it does |
| --- | --- |
| `click {role, name}` | Presses the control with that role and accessible name |
| `fill {label, value}` | Types `value` into the field with that label |
| `press` | Presses a key, such as `Enter` or `Escape` |
| `waitFor {text}` | Waits until the text shows |

A name, label, or text matches whole, and must match one element, so a step that is ambiguous fails and does not press whichever control came first. `exact: false` matches part of it. `in {role, name}` limits a click, fill, or wait to the dialog, region, or other element it names. A value or a route can hold `{{name}}`, which a variable of the world replaces: `passphrase` in every world, and `src`, `api`, and `web` in harbor, the folders of the clones.

A `clip` is `"window"`, the whole 1440 by 900 window, or one element: `{"selector": "main"}`, or `{"role": "dialog", "name": "Register repositories"}`, with an optional `pad`, the pixels to keep around it, which must fit in the window. A dialog, a card, or a panel is clipped to its element, and a whole window is for a step that shows a view's place in the frame.

[web/scripts/guides.mjs](../../web/scripts/guides.mjs) walks the steps, and [web/src/guides.test.ts](../../web/src/guides.test.ts) tests it on a page of its own in `make web-test`. Each image is written as `<name>-<theme>.png` at 1440 by 900 and twice the pixel density. A guide's images are not captured at phone width.

The walk fails a capture whose page shows the installation's temporary path, the fake agent CLI's, or this machine's home directory, user name, or host name, and keeps its image. A failed step keeps what the page showed as `failed-<guide>-<step number>-<theme>.png` beside the images.

## The product gap checks

`make guide-shots CHECKS=1` runs two checks that the guides depend on, on a harbor installation, and fails when a gap is real.

| Check | What it holds |
| --- | --- |
| `TestNoCommandSpendsAJoinToken` | Nothing a person runs spends a join token: `ropeshift worker token` makes one, and neither program has a command that takes one |
| `TestAssistantAnswersInALaterProject` | A conversation with the assistant works in a project created after the installation's first, as it does in the first |

## Publishing images

Publishing copies captures into `docs/assets/screenshots/`, where the docs show them. It runs on the directory a capture run wrote:

```sh
make screenshots-publish SHOTS=.scratch/shots/<version>
make screenshots-publish SHOTS=.scratch/shots/<version> GUIDE=get-started
```

[scripts/screenshots-publish.sh](../../scripts/screenshots-publish.sh) reads the table below, stops when a capture it names is missing, and puts each capture through `pngquant --quality=70-90 --speed 1 --strip` into the published name. It copies nothing when a capture is missing or an optimized image is over the size limit.

The first publishes every row, so its directory must hold every view capture and every guide's. The second publishes one guide's rows alone, the ones whose capture is `<guide>-NN-<theme>.png`, from the directory of a `make guide-shots GUIDE=<guide>` run. It holds them to the same checks: both themes of each, and each image at most 250 KB. It stops when the table has no row for the guide. `pngquant` is the optimizer, and the step stops with the command that installs it, `brew install pngquant` or your distribution's package, when it is missing.

Each published image is at most 250 KB, which `go test ./test/rules` holds the docs to. A window capture of a view comes to 100 to 210 KB, a dialog or a card clipped to its element to much less. Clip a capture that is over the limit to the element that matters. A published image is refreshed only when its view changes, because each refresh adds the whole image to the repository's history.

### The published images

Every published image has a row, and every row names a capture and the name it is published as. `<theme>` is `light` and `dark`, and each doc shows both, one for each GitHub theme. The view captures of `make screenshots` are the 1440-wide ones, dropping the width from the name. A guide's rows name its captures, as `get-started-01-<theme>.png` to `get-started-07-<theme>.png`.

| Capture | Published as | Shown in |
| --- | --- | --- |
| `needs-you-<theme>-1440.png` | `needs-you-<theme>.png` | README, web UI guide |
| `needs-you-question-<theme>-1440.png` | `needs-you-question-<theme>.png` | First plan |
| `decisions-<theme>-1440.png` | `decisions-<theme>.png` | README |
| `floor-<theme>-1440.png` | `floor-<theme>.png` | README, web UI guide, first plan |
| `measures-<theme>-1440.png` | `measures-<theme>.png` | README, web UI guide |
| `roadmap-<theme>-1440.png` | `roadmap-<theme>.png` | Web UI guide |
| `planning-<theme>-1440.png` | `planning-<theme>.png` | First plan |
| `plan-approval-<theme>-1440.png` | `plan-approval-<theme>.png` | Web UI guide, first plan |
| `work-order-<theme>-1440.png` | `work-order-<theme>.png` | First plan |
| `documents-requirement-<theme>-1440.png` | `documents-<theme>.png` | Web UI guide |
| `conversations-<theme>-1440.png` | `conversations-<theme>.png` | Web UI guide |
| `agents-project-<theme>-1440.png` | `agents-<theme>.png` | Web UI guide |
| `settings-project-<theme>-1440.png` | `settings-<theme>.png` | Web UI guide |
| `members-<theme>-1440.png` | `members-<theme>.png` | Web UI guide |
| `repositories-<theme>-1440.png` | `repositories-<theme>.png` | Set up a project |
| `repository-<theme>-1440.png` | `repository-<theme>.png` | GitHub guide |
| `import-<theme>-1440.png` | `import-<theme>.png` | Adopt guide |
| `top.png` | `top.png` | README |
| `get-started-01-<theme>.png` | `get-started-01-<theme>.png` | Get started |
| `get-started-02-<theme>.png` | `get-started-02-<theme>.png` | Get started |
| `get-started-03-<theme>.png` | `get-started-03-<theme>.png` | Get started |
| `get-started-04-<theme>.png` | `get-started-04-<theme>.png` | Get started |
| `get-started-05-<theme>.png` | `get-started-05-<theme>.png` | Get started |
| `get-started-06-<theme>.png` | `get-started-06-<theme>.png` | Get started |
| `get-started-07-<theme>.png` | `get-started-07-<theme>.png` | Get started |

After a release, run `make screenshots` at its tag, look at each image, and publish. Then update the version that the README's screenshots section and the web UI guide name.
