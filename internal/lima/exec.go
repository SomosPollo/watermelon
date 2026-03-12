package lima

import "os/exec"

// execCommand wraps exec.Command. Tests replace this to mock external commands.
var execCommand = exec.Command
