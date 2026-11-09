package server_test

import (
	"os"
	"testing"
	"time"

	"ropeshift.com/ropeshift/web"
)

// envBrowserCheck names a file. With it set, the test below serves a populated ledger on the web
// listener, writes the address to that file, and holds until the file goes. envBrowserEveryState asks
// for the ledger that holds each state of both tables instead.
const (
	envBrowserCheck      = "ROPESHIFT_BROWSER_CHECK"
	envBrowserEveryState = "ROPESHIFT_BROWSER_EVERY_STATE"
)

// TestServeAWorldForABrowser is how a person or a browser script reads the real page on the real API,
// with one of everything waiting. It binds loopback, starts no session, and is skipped in every suite.
func TestServeAWorldForABrowser(t *testing.T) {
	file := os.Getenv(envBrowserCheck)
	if file == "" {
		t.Skip(envBrowserCheck + " names no file")
	}
	d := aDetailedWorld(t)
	if os.Getenv(envBrowserEveryState) != "" {
		d = aWorldInEveryState(t)
	}
	assets, err := web.Assets()
	if err != nil {
		t.Fatal(err)
	}
	site := d.ServeWeb(t, assets, time.Second)
	if err := os.WriteFile(file, []byte(site.URL("/")), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(file); err != nil {
			return
		}
		select {
		case <-t.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
