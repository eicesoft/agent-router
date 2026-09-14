//go:build windows

package envcfg

import (
	"errors"
	"os/exec"
	"strings"
)

// Windows stores the persistent per-user environment in the registry
// (HKCU\Environment). New processes inherit it; already-running shells need a
// restart to see the change. Read and write both go through the registry so
// the value survives reboots and logins.

// windowsUserEnv reads AGENT_ROUTER_API_KEY from the persistent per-user
// environment.
func windowsUserEnv() (string, bool) {
	out, err := exec.Command("reg", "query", `HKCU\Environment`, "/v", EnvVar).Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		// REG_SZ row: "<name>  REG_SZ  <value>"
		if len(fields) >= 3 && fields[0] == EnvVar && fields[1] == "REG_SZ" {
			return strings.Join(fields[2:], " "), true
		}
	}
	return "", false
}

// windowsSetUserEnv writes the key into the persistent per-user environment.
// setx alone is enough for new processes to inherit the value; reg add mirrors
// it so the registry stays the source of truth.
func windowsSetUserEnv(key string) error {
	if key == "" {
		return errors.New("api key is required")
	}
	if err := exec.Command("reg", "add", `HKCU\Environment`, "/v", EnvVar, "/t", "REG_SZ", "/d", key, "/f").Run(); err != nil {
		return err
	}
	_ = exec.Command("setx", EnvVar, key).Run()
	return nil
}
