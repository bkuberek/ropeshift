# Web tests

`make web-test` runs the web UI's type check and its test suites from `web/`. Most run in happy-dom, with no browser. `web/src/a11y.test.ts` runs in a real browser, on the application served by Vite and the daemon's answers from the Go tests' goldens. It runs axe-core with the WCAG 2.0, 2.1, and 2.2 A and AA rules, in the light and the dark theme, at desktop and at phone width, and any violation fails.

`web/src/a11yCoverage.test.ts` needs no browser. It fails for a Settings section, a work order tab, a kind of Needs you card, or a dialog that no axe state shows, so a view added without its state fails `make web-test` on any machine. The states are listed in `web/src/a11yStates.ts`.

## The browser

The browser suite launches the first of these that starts:

1. The executable named by `ROPESHIFT_TEST_CHROME`.
2. Google Chrome, through Playwright's `chrome` channel. Continuous integration's runners carry it.
3. `chromium` or `chromium-browser` on the `PATH`.
4. The Chromium Playwright installed for its own use.

With none of them, the suite fails when `CI` is set and skips with a message that names `ROPESHIFT_TEST_CHROME` otherwise. To run it on a machine with no Chrome, point the variable at any Chrome or Chromium build:

```sh
ROPESHIFT_TEST_CHROME=/usr/bin/chromium npm --prefix web test -- src/a11y.test.ts
```

The suite is slow, because each view is audited four times. `-t` narrows a run to the tests whose names match, such as `-t "desktop width"`, or `-t "dialogs of Projects"` for one test in all four combinations.

The last test of `a11y.test.ts`, which checks that each dialog in the list was open during an audit, reads what the tests before it saw, so it needs the whole file.
