// Package provider calls model APIs directly for `seneca review`. It is a
// desktop adapter: only github.com/sglavoie/seneca/cmd imports it, and nothing bound for the phone
// may (mobile/senecabridge's dependency tests hold that). A provider returns
// the same raw bytes a CLI harness does, so prompt building, the schema, and
// validation stay where they are.
package provider

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// KeySource names where an API key lives. Keys are read from the environment,
// then from the macOS Keychain, and never from config.toml.
type KeySource struct {
	// EnvVar is the environment variable checked first, e.g. ANTHROPIC_API_KEY.
	EnvVar string
	// Service is the Keychain generic-password service checked second, e.g.
	// seneca-anthropic.
	Service string
}

// AnthropicKey is where the Anthropic API key is looked up.
var AnthropicKey = KeySource{EnvVar: "ANTHROPIC_API_KEY", Service: "seneca-anthropic"}

// OpenAIKey is where the OpenAI API key is looked up.
var OpenAIKey = KeySource{EnvVar: "OPENAI_API_KEY", Service: "seneca-openai"}

// KeychainReader returns the generic password stored for a service. It reports
// ErrKeychainItemNotFound when there is none, so a missing key and a Keychain
// that failed are told apart. It is a parameter so tests never reach the real
// Keychain.
type KeychainReader func(service string) (string, error)

// ErrKeychainItemNotFound is a KeychainReader's answer for a service with no
// stored password.
var ErrKeychainItemNotFound = errors.New("keychain item not found")

// MissingKeyError reports that neither source holds a key, and says how to
// provide one.
type MissingKeyError struct {
	Source KeySource
}

func (e *MissingKeyError) Error() string {
	return fmt.Sprintf("no API key found: set %s, or store one in the Keychain with `security add-generic-password -a \"$USER\" -s %s -w` (nothing was written)",
		e.Source.EnvVar, e.Source.Service)
}

// ResolveKey reads the key from the environment, then the Keychain, and
// returns it with a label naming the source in force. The label is safe to
// print; the key never is.
func ResolveKey(source KeySource, getenv func(string) string, keychain KeychainReader) (key string, label string, err error) {
	if value := strings.TrimSpace(getenv(source.EnvVar)); value != "" {
		return value, source.EnvVar, nil
	}
	if keychain != nil {
		value, err := keychain(source.Service)
		switch {
		case errors.Is(err, ErrKeychainItemNotFound):
		case err != nil:
			return "", "", fmt.Errorf("reading Keychain item %s: %w", source.Service, err)
		case strings.TrimSpace(value) != "":
			return strings.TrimSpace(value), "Keychain (" + source.Service + ")", nil
		}
	}
	return "", "", &MissingKeyError{Source: source}
}

// securityItemNotFound is the exit status `security find-generic-password`
// uses for a missing item (errSecItemNotFound).
const securityItemNotFound = 44

// SecurityKeychain reads a generic password through macOS's `security` tool.
// Where the tool does not exist, there is no Keychain to hold a key, which is
// reported as a missing item rather than a failure.
func SecurityKeychain(service string) (string, error) {
	binary, err := exec.LookPath("security")
	if err != nil {
		return "", ErrKeychainItemNotFound
	}
	var stdout, stderr bytes.Buffer
	command := exec.Command(binary, "find-generic-password", "-s", service, "-w")
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == securityItemNotFound {
			return "", ErrKeychainItemNotFound
		}
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return "", fmt.Errorf("%w: %s", err, message)
		}
		return "", err
	}
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}
