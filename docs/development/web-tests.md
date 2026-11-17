# Web tests

`make web-test` runs the web UI's type check and its test suites from `web/`. Most run in happy-dom, with no browser. Two run in a real browser, on the application served by Vite and the daemon's answers from the Go tests' goldens:

- `web/src/a11y.test.ts` runs axe-core with the WCAG 2.0, 2.1, and 2.2 A and AA rules, in the light and the dark theme, at desktop and at phone width, and any violation fails.
- `web/src/keyboard.test.ts` reaches the control of every operation that changes something with the keyboard alone, from the default view, and asserts the request it sends. A path uses the command palette, the G keys, Tab, Enter, the arrows, and Esc, and a test fails if the suite uses a pointer.

Two more need no browser. `web/src/a11yCoverage.test.ts` fails for a Settings section, a work order tab, a kind of Needs you card, or a dialog that no axe state shows, so a view added without its state fails `make web-test` on any machine. The states are listed in `web/src/a11yStates.ts`. `web/src/api/parity.test.ts` and `web/src/app/paletteActions.test.tsx` fail for a route that changes something and has a control in the UI but no action in the command palette, and for an action the palette does not list. The actions are named in `web/src/app/paletteActions.ts`, and a new route needs a keyboard path in `keyboard.test.ts` too.

## The browser

Each browser suite launches the first of these that starts:

1. The executable named by `ROPESHIFT_TEST_CHROME`.
2. Google Chrome, through Playwright's `chrome` channel. Continuous integration's runners carry it.
3. `chromium` or `chromium-browser` on the `PATH`.
4. The Chromium Playwright installed for its own use. `make web-test` looks for it in `.scratch/playwright` in the checkout, which git ignores, and not in the cache every project on the machine shares, unless `PLAYWRIGHT_BROWSERS_PATH` already names another directory. [Screenshots](screenshots.md#run-it) keeps its browsers there too.

With none of them, a suite fails when `CI` is set and skips with a message that names `ROPESHIFT_TEST_CHROME` otherwise. To run them on a machine with no Chrome, point the variable at any Chrome or Chromium build:

```sh
ROPESHIFT_TEST_CHROME=/usr/bin/chromium npm --prefix web test -- src/a11y.test.ts src/keyboard.test.ts
```

The accessibility suite is slow, because each view is audited four times. `-t` narrows a run to the tests whose names match, such as `-t "desktop width"`, or `-t "dialogs of Projects"` for one test in all four combinations. For the keyboard suite, `-t "POST /v1/projects/"` runs the paths of the routes that match.

The last test of `a11y.test.ts`, which checks that each dialog in the list was open during an audit, reads what the tests before it saw, so it needs the whole file.
