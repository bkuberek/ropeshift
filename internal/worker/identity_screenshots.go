//go:build screenshots

package worker

// fixedWorkerID is the ID of every worker the screenshots build starts, so the images of the web UI name
// the same worker in every capture (docs/development/screenshots.md). Each capture runs one worker in an
// installation of its own, so no two ever meet. A normal build holds none of this, which test/rules checks.
const fixedWorkerID = "3f9a2c7e5b1d4086a4e2c8f06d9b1e57"

func newWorkerID() (string, error) { return fixedWorkerID, nil }
