package lima

import "os/exec"

// execCommand wraps exec.Command. Tests replace this to mock external commands.
var execCommand = exec.Command

// execLookPath wraps exec.LookPath. Compatibility tests replace it without
// changing the process-wide PATH used by unrelated tests.
var execLookPath = exec.LookPath

// qemuExecCommand wraps exec.Command for the read-only QEMU identity probe.
var qemuExecCommand = exec.Command
