package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
)

// ColorPair is a Light/Dark color pair for lipgloss.AdaptiveColor.
type ColorPair struct {
	Light string `json:"light"`
	Dark  string `json:"dark"`
}

// Theme holds all user-customizable colors. Fields correspond 1:1 to the
// style variables defined in ui.go.
type Theme struct {
	Name    ColorPair `json:"name"`
	Faint   ColorPair `json:"faint"`
	Cursor  ColorPair `json:"cursor"`
	Status  ColorPair `json:"status"`
	Error   ColorPair `json:"error"`
	ReplyTo ColorPair `json:"replyTo"`
	Liked   ColorPair `json:"liked"`
	Link    ColorPair `json:"link"`
}

// defaultTheme reproduces the colors that were previously hardcoded in ui.go.
func defaultTheme() Theme {
	return Theme{
		Name:    ColorPair{Light: "25", Dark: "39"},
		Faint:   ColorPair{Light: "245", Dark: "243"},
		Cursor:  ColorPair{Light: "205", Dark: "213"},
		Status:  ColorPair{Light: "22", Dark: "114"},
		Error:   ColorPair{Light: "124", Dark: "203"},
		ReplyTo: ColorPair{Light: "94", Dark: "179"},
		Liked:   ColorPair{Light: "162", Dark: "205"},
		Link:    ColorPair{Light: "26", Dark: "45"},
	}
}

// themeFileName returns the theme.json path for the given profile, mirroring
// the config-<profile>.json naming used by loadConfig in main.go.
func themeFileName(profile string) string {
	if profile != "" {
		return "theme-" + profile + ".json"
	}
	return "theme.json"
}

// loadTheme reads theme.json from the tualgia config dir (falling back to
// algia's dir, same as loadConfig). Missing file: returns defaultTheme with
// no error. Malformed file: returns defaultTheme and the parse error, so the
// caller can decide whether to warn the user.
func loadTheme(profile string) (Theme, error) {
	theme := defaultTheme()

	dir, err := configDir()
	if err != nil {
		return theme, err
	}

	fname := themeFileName(profile)

	b, readErr := os.ReadFile(filepath.Join(dir, name, fname))
	if readErr != nil {
		// No theme.json: use defaults, this is not an error condition.
		return theme, nil
	}

	if err := json.Unmarshal(b, &theme); err != nil {
		return defaultTheme(), err
	}
	return theme, nil
}

// pair converts a ColorPair to lipgloss.AdaptiveColor, falling back to the
// default value for any side left empty in theme.json.
func (c ColorPair) pair(fallback ColorPair) lipgloss.AdaptiveColor {
	light, dark := c.Light, c.Dark
	if light == "" {
		light = fallback.Light
	}
	if dark == "" {
		dark = fallback.Dark
	}
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}

// applyTheme overwrites the package-level style variables in ui.go with the
// colors from theme. Must be called once at startup before the bubbletea
// program starts rendering.
func applyTheme(theme Theme) {
	def := defaultTheme()

	nameStyle = nameStyle.Foreground(theme.Name.pair(def.Name))
	faintStyle = faintStyle.Foreground(theme.Faint.pair(def.Faint))
	cursorStyle = cursorStyle.Foreground(theme.Cursor.pair(def.Cursor))
	statusStyle = statusStyle.Foreground(theme.Status.pair(def.Status))
	errorStyle = errorStyle.Foreground(theme.Error.pair(def.Error))
	replyToStyle = replyToStyle.Foreground(theme.ReplyTo.pair(def.ReplyTo))
	likedStyle = likedStyle.Foreground(theme.Liked.pair(def.Liked))
	linkStyle = linkStyle.Foreground(theme.Link.pair(def.Link))
}