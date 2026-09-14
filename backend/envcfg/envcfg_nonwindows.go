//go:build !windows

package envcfg

// windowsUserEnv and windowsSetUserEnv are only reachable on Windows (see
// FindKey and WriteKey) but must compile everywhere. On macOS/Linux the rc-file
// path is used instead, so these return the zero value.
func windowsUserEnv() (string, bool) { return "", false }

func windowsSetUserEnv(string) error { return nil }
