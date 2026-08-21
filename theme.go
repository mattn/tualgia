package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
)

// Color is a terminal color in any form lipgloss accepts: an ANSI256 index
// ("205") or a hex code ("#ff00ff"). JSON numbers are also accepted for
// ANSI indexes, so {"light": 25} and {"light": "25"} are equivalent.
type Color string

func (c *Color) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*c = Color(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*c = Color(n.String())
		return nil
	}
	return fmt.Errorf("invalid color %s", string(b))
}

// ColorPair is a Light/Dark color pair for lipgloss.AdaptiveColor. A side
// set to "" explicitly keeps the terminal's default foreground; omitted
// keys keep the built-in colors because loadTheme unmarshals over
// defaultTheme.
type ColorPair struct {
	Light Color `json:"light"`
	Dark  Color `json:"dark"`
}

func (c ColorPair) adaptive() lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: string(c.Light), Dark: string(c.Dark)}
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

// loadTheme reads theme.json (or theme-<profile>.json) with the same lookup
// order as loadConfig: the tualgia config dir first, then algia's, so the
// theme can live next to whichever config is in use. A missing file is not
// an error; any other failure returns defaults along with the error so the
// caller can warn.
func loadTheme(profile string) (Theme, error) {
	theme := defaultTheme()

	dir, err := configDir()
	if err != nil {
		return theme, err
	}
	fname := profileFileName("theme", profile)

	var b []byte
	readErr := error(fs.ErrNotExist)
	for _, app := range []string{name, "algia"} {
		if b, readErr = os.ReadFile(filepath.Join(dir, app, fname)); readErr == nil {
			break
		}
	}
	if readErr != nil {
		if errors.Is(readErr, fs.ErrNotExist) {
			return theme, nil
		}
		return theme, readErr
	}

	if err := json.Unmarshal(b, &theme); err != nil {
		return defaultTheme(), err
	}
	return theme, nil
}

// applyTheme overwrites the package-level style variables in ui.go with the
// colors from theme. Must be called once at startup before the bubbletea
// program starts rendering.
func applyTheme(theme Theme) {
	nameStyle = nameStyle.Foreground(theme.Name.adaptive())
	faintStyle = faintStyle.Foreground(theme.Faint.adaptive())
	cursorStyle = cursorStyle.Foreground(theme.Cursor.adaptive())
	statusStyle = statusStyle.Foreground(theme.Status.adaptive())
	errorStyle = errorStyle.Foreground(theme.Error.adaptive())
	replyToStyle = replyToStyle.Foreground(theme.ReplyTo.adaptive())
	likedStyle = likedStyle.Foreground(theme.Liked.adaptive())
	linkStyle = linkStyle.Foreground(theme.Link.adaptive())
}
