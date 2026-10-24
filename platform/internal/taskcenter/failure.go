package taskcenter

import "errors"

// PermanentTaskError marks an immutable business input failure. Retrying the
// same scheduled task cannot fix it; infrastructure failures remain retryable.
type PermanentTaskError struct {
	Err error
}

func (e *PermanentTaskError) Error() string {
	if e == nil || e.Err == nil {
		return "permanent task failure"
	}
	return e.Err.Error()
}

func (e *PermanentTaskError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func PermanentFailure(err error) error {
	if err == nil || IsPermanentTaskError(err) {
		return err
	}
	return &PermanentTaskError{Err: err}
}

func IsPermanentTaskError(err error) bool {
	var permanent *PermanentTaskError
	return errors.As(err, &permanent)
}
