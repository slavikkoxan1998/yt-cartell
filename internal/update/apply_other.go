//go:build !windows && !darwin

package update

func installable() error {
	return errManual("На цій системі студія не оновлюється сама.")
}

// apply: there is no published build for this system to swap in place.
func apply(string) (Restart, error) {
	return nil, errManual("На цій системі студія не оновлюється сама.")
}
