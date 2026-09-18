package cli

import "fmt"

// ExitError asks main to exit with a specific status and print nothing.
//
// It carries a child process's status out of a command that ran one. An
// interactive `claude` that exits 1, or a session ended with Ctrl-C (130), is
// not a cspace failure to report — it is cspace's own exit code, and printing
// "Error: exit status 130" over a terminal the user has finished with is
// noise.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }
