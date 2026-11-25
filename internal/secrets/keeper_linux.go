//go:build linux

package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// System returns the operating system's keeper. On Linux that is the
// Secret Service (GNOME Keyring, KWallet), reached through secret-tool, so
// no outside module is needed. A machine without secret-tool has no keeper.
func System() (Keeper, error) {
	tool, err := exec.LookPath("secret-tool")
	if err != nil {
		return nil, fmt.Errorf("%w (secret-tool is not installed)", ErrNoKeeper)
	}
	item := []string{"service", "keystone", "account", "install-master-key"}
	return &keyKeeper{
		name: "secret-service",
		find: func() (string, bool, error) {
			cmd := exec.Command(tool, append([]string{"lookup"}, item...)...)
			var out, errs bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errs
			err := cmd.Run()
			var exit *exec.ExitError
			if errors.As(err, &exit) && errs.Len() == 0 {
				return "", false, nil // lookup fails and says nothing when there is no such item
			}
			if err != nil {
				return "", false, fmt.Errorf("%w: %s", err, strings.TrimSpace(errs.String()))
			}
			return out.String(), out.Len() > 0, nil
		},
		// secret-tool reads the key from standard input, so it is never
		// an argument that another process could read.
		add: func(hexKey string) error {
			cmd := exec.Command(tool, append([]string{"store", "--label=Keystone install master key"}, item...)...)
			cmd.Stdin = strings.NewReader(hexKey)
			var errs bytes.Buffer
			cmd.Stderr = &errs
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("%w: %s", err, strings.TrimSpace(errs.String()))
			}
			return nil
		},
	}, nil
}
