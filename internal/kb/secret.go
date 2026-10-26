package kb

import (
	"fmt"
	"os"
	"strings"
)

// LoadSecret returns a secret from an environment variable or, if that is
// empty, from a file. The file must not be readable or writable by group or
// others. Errors name the variable or file, never the value.
func LoadSecret(env func(string) string, envName, file string) (string, error) {
	if envName != "" {
		if v := strings.TrimSpace(env(envName)); v != "" {
			return v, nil
		}
	}
	if file == "" {
		return "", fmt.Errorf("%s is not set and no key file is configured", envName)
	}
	st, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", file)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s has mode %v; run chmod 600 %s", file, st.Mode().Perm(), file)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "", fmt.Errorf("%s is empty", file)
	}
	return v, nil
}

// redact removes every secret from s. Used on error text from HTTP calls,
// as a second line of defense; the clients never put keys into errors.
func redact(s string, secrets ...string) string {
	for _, k := range secrets {
		if len(k) >= 8 {
			s = strings.ReplaceAll(s, k, "[redacted]")
		}
	}
	return s
}
