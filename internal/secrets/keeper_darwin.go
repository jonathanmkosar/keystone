//go:build darwin

package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// On macOS the install master key is a generic password in the login
// keychain. Keystone reaches it through /usr/bin/security, which every Mac
// has, so no outside module and no C compiler is needed.
const (
	securityTool    = "/usr/bin/security"
	keychainService = "keystone"
	keychainAccount = "install-master-key"
	// security exits with this when the keychain has no such item.
	keychainNotFound = 44
)

// System returns the operating system's keeper. On macOS that is the login
// keychain.
func System() (Keeper, error) {
	return &keyKeeper{name: "keychain", find: keychainFind, add: keychainAdd}, nil
}

func keychainFind() (string, bool, error) {
	cmd := exec.Command(securityTool, "find-generic-password", "-s", keychainService, "-a", keychainAccount, "-w")
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == keychainNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("%w: %s", err, strings.TrimSpace(errs.String()))
	}
	return out.String(), true, nil
}

// keychainAdd stores the key. The key reaches security on standard input,
// in its interactive mode, so it is never an argument that another process
// could read. What security prints is dropped: the caller reads the key
// back to learn whether it was stored.
func keychainAdd(hexKey string) error {
	cmd := exec.Command(securityTool, "-i")
	cmd.Stdin = strings.NewReader("add-generic-password -s " + keychainService + " -a " + keychainAccount + " -w " + hexKey + "\n")
	return cmd.Run()
}
