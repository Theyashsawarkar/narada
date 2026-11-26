// narada-pipe: what `narada mcp` becomes when the daemon runs. It copies
// the agent's stdin and stdout to and from the daemon's socket and holds
// nothing else, so each agent costs a few MB. narada execs it when it sits
// next to the narada binary; on its own it does the same.
//
// With no daemon it hands over to `narada mcp --in-process` (the narada
// next to it), which serves the tools itself.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Theyashsawarkar/narada/internal/pipe"
)

func main() {
	sock := pipe.Socket()
	ok, err := pipe.Run(sock, os.Stdin, os.Stdout, pipe.Env())
	if ok {
		if err != nil {
			fmt.Fprintln(os.Stderr, "narada:", err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "narada: no daemon on %s (%v); serving the tools in-process\n", sock, err)
	exe, xerr := os.Executable()
	if xerr == nil {
		exe, _ = filepath.EvalSymlinks(exe)
	}
	narada := filepath.Join(filepath.Dir(exe), "narada")
	xerr = syscall.Exec(narada, []string{"narada", "mcp", "--in-process"}, os.Environ())
	fmt.Fprintf(os.Stderr, "narada: can't start %s: %v\n", narada, xerr)
	os.Exit(1)
}
