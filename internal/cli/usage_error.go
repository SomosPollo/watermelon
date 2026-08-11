package cli

import (
	"errors"
)

// usageError marks an error caused by invalid command-line input. The marker
// lets the top-level command print command usage for invocation errors without
// adding usage noise to runtime, configuration, or policy failures.
type usageError struct {
	err error
}

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// NewUsageError marks err as an invocation error for which command usage is
// helpful. It preserves nil and avoids wrapping an already marked error.
func NewUsageError(err error) error {
	if err == nil || IsUsageError(err) {
		return err
	}
	return &usageError{err: err}
}

// IsUsageError reports whether err contains an invocation-error marker.
func IsUsageError(err error) bool {
	var target *usageError
	return errors.As(err, &target)
}
