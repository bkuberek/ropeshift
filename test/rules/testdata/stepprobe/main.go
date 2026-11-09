// Command stepprobe links the Store's write path, so a test can read what a build
// holds.
package main

import (
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ports"
)

func main() {
	store, err := sqlite.OpenMemory(sqlstore.Options{})
	if err != nil {
		panic(err)
	}
	var port ports.Store = store
	err = port.Write(context.Background(), "probe", "probe", func(ports.WriteTx) error { return nil })
	fmt.Println(err, port.Close())
}
