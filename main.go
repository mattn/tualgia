package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nbd-wtf/go-nostr/nip19"
)

const name = "tualgia"

const version = "0.0.1"

var revision = "HEAD"

// Relay is
type Relay struct {
	Read     bool `json:"read"`
	Write    bool `json:"write"`
	Search   bool `json:"search"`
	Global   bool `json:"global"`
	DM       bool `json:"dm"`
	Bookmark bool `json:"bm"`
	Auth     bool `json:"auth"`
}

// Config is
type Config struct {
	Relays     map[string]Relay  `json:"relays"`
	FollowList []string          `json:"followList"`
	PrivateKey string            `json:"privatekey"`
	Updated    time.Time         `json:"updated"`
	Emojis     map[string]string `json:"emojis"`

	profiles map[string]Profile
}

// Profile is
type Profile struct {
	Website     string `json:"website"`
	Nip05       string `json:"nip05"`
	Picture     string `json:"picture"`
	Lud16       string `json:"lud16"`
	DisplayName string `json:"display_name"`
	About       string `json:"about"`
	Name        string `json:"name"`
	Bot         bool   `json:"bot"`
}

func configDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		dir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, ".config"), nil
	default:
		return os.UserConfigDir()
	}
}

// loadConfig loads the configuration for profile. It looks in the tualgia
// config directory first and falls back to algia's, so an existing algia
// setup works as-is.
func loadConfig(profile string) (*Config, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}

	cname := "config.json"
	pname := "profiles.json"
	if profile != "" {
		cname = "config-" + profile + ".json"
		pname = "profiles-" + profile + ".json"
	}

	var b []byte
	var appDir string
	for _, app := range []string{name, "algia"} {
		appDir = filepath.Join(dir, app)
		if b, err = os.ReadFile(filepath.Join(appDir, cname)); err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("cannot load %s: %w", cname, err)
	}

	var cfg Config
	if err = json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	if len(cfg.Relays) == 0 {
		cfg.Relays = map[string]Relay{
			"wss://relay.damus.io":   {Read: true, Write: true},
			"wss://search.nos.today": {Read: true, Search: true},
		}
	}

	cfg.profiles = map[string]Profile{}
	if pb, err := os.ReadFile(filepath.Join(appDir, pname)); err == nil {
		var m map[string]Profile
		if err := json.Unmarshal(pb, &m); err == nil {
			for k, v := range m {
				if pk, ok := normalizeKey(k); ok {
					cfg.profiles[pk] = v
				}
			}
		}
	}
	return &cfg, nil
}

// normalizeKey accepts a hex or npub public key and returns it as hex.
func normalizeKey(key string) (string, bool) {
	if prefix, decoded, err := nip19.Decode(key); err == nil && prefix == "npub" {
		if pk, ok := decoded.(string); ok {
			return pk, true
		}
	}
	if len(key) == 64 {
		if _, err := hex.DecodeString(key); err == nil {
			return key, true
		}
	}
	return "", false
}

func main() {
	var profile string
	var showVersion bool
	flag.StringVar(&profile, "a", "", "profile name")
	flag.BoolVar(&showVersion, "V", false, "show version")
	flag.Parse()

	if showVersion {
		fmt.Println(version)
		return
	}

	cfg, err := loadConfig(profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	theme, err := loadTheme(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to parse %s, using default theme: %v\n", themeFileName(profile), err)
	}
	applyTheme(theme)

	client, err := newClient(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := tea.NewProgram(newModel(ctx, client), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
