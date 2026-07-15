package opencodeadapter

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

type AttachOptions struct {
	URL        string
	ProjectDir string
	Password   string
	Continue   bool
	Session    string
}

func BuildServerArgv(prefix []string, port int) ([]string, error) {
	if err := validatePrefix(prefix); err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid OpenCode port %d", port)
	}
	argv := append([]string(nil), prefix...)
	return append(argv, "--pure", "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port)), nil
}

func BuildAttachArgv(prefix []string, options AttachOptions) ([]string, error) {
	if err := validatePrefix(prefix); err != nil {
		return nil, err
	}
	if err := validateLoopbackURL(options.URL); err != nil {
		return nil, err
	}
	if options.ProjectDir == "" || !filepath.IsAbs(options.ProjectDir) || filepath.Clean(options.ProjectDir) != options.ProjectDir {
		return nil, errors.New("OpenCode project directory must be a clean absolute path")
	}
	if options.Password == "" || strings.ContainsAny(options.Password, "\x00\r\n") {
		return nil, errors.New("OpenCode password is empty or invalid")
	}
	if options.Continue && options.Session != "" {
		return nil, errors.New("OpenCode continue and exact session are mutually exclusive")
	}
	if options.Session != "" && (!validIdentifier(options.Session) || len(options.Session) > 256) {
		return nil, errors.New("invalid OpenCode session identifier")
	}

	argv := append([]string(nil), prefix...)
	argv = append(argv, "--pure", "attach", options.URL,
		"--dir", options.ProjectDir, "--password", options.Password)
	if options.Continue {
		argv = append(argv, "--continue")
	} else if options.Session != "" {
		argv = append(argv, "--session", options.Session)
	}
	return argv, nil
}

func validatePrefix(prefix []string) error {
	if len(prefix) == 0 || prefix[0] == "" || !filepath.IsAbs(prefix[0]) || filepath.Clean(prefix[0]) != prefix[0] {
		return errors.New("OpenCode executable must be a clean absolute path")
	}
	if len(prefix) == 1 {
		return nil
	}
	if len(prefix) != 3 || filepath.Base(prefix[0]) != "npx" {
		return errors.New("unsupported OpenCode command prefix")
	}
	if prefix[1] == "--no-install" && prefix[2] == "opencode-ai" {
		return nil
	}
	if prefix[1] == "--prefer-offline" && prefix[2] == "opencode-ai@latest" {
		return nil
	}
	return errors.New("unsupported OpenCode npx prefix")
}

func validateLoopbackURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("OpenCode server URL must be an unadorned HTTP loopback URL")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 || net.JoinHostPort(parsed.Hostname(), parsed.Port()) != parsed.Host {
		return errors.New("OpenCode server URL has an invalid port")
	}
	return nil
}

func validIdentifier(value string) bool {
	return value != "" && !strings.ContainsAny(value, "\x00\t\r\n")
}
