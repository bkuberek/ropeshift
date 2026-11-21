// Package worker is a fixture: the one line the no-shell rule exempts, and two more that hold a "-c" it must find.
package worker

// gitValueOptions is the exempt line, as it stands in the real source.
var gitValueOptions = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--config-env", "--attr-source"}

// run builds a command line for a shell, in the file that holds the exempt line.
var run = []string{shellPath, "-c", "line"}

// both holds the exempt line and more on one line, which is no longer the exempt line.
var both = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--config-env", "--attr-source"} // shellPath
