package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func writeTheme(t *testing.T, dir, fname, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fname), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadTheme(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	// missing file: defaults, no error
	theme, err := loadTheme("")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if theme != defaultTheme() {
		t.Fatalf("want defaults, got %+v", theme)
	}

	// numbers and strings both work; omitted keys/sides keep defaults
	writeTheme(t, filepath.Join(xdg, "tualgia"), "theme.json",
		`{"name": {"light": "#0f4c81", "dark": "#7aa2f7"}, "liked": {"dark": 99}}`)
	theme, err = loadTheme("")
	if err != nil {
		t.Fatal(err)
	}
	if theme.Name.Light != "#0f4c81" || theme.Name.Dark != "#7aa2f7" {
		t.Errorf("name = %+v", theme.Name)
	}
	if theme.Liked.Light != "162" || theme.Liked.Dark != "99" {
		t.Errorf("liked = %+v", theme.Liked)
	}
	if theme.Error != defaultTheme().Error {
		t.Errorf("error = %+v", theme.Error)
	}

	// malformed json: defaults plus an error
	writeTheme(t, filepath.Join(xdg, "tualgia"), "theme.json", "{oops")
	theme, err = loadTheme("")
	if err == nil {
		t.Fatal("want error for malformed theme.json")
	}
	if theme != defaultTheme() {
		t.Fatalf("want defaults after parse error, got %+v", theme)
	}
}

func TestLoadThemeAlgiaFallback(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	writeTheme(t, filepath.Join(xdg, "algia"), "theme.json", `{"cursor": {"light": "1", "dark": "2"}}`)
	theme, err := loadTheme("")
	if err != nil {
		t.Fatal(err)
	}
	if theme.Cursor.Light != "1" || theme.Cursor.Dark != "2" {
		t.Errorf("cursor = %+v", theme.Cursor)
	}

	// tualgia's dir wins over algia's
	writeTheme(t, filepath.Join(xdg, "tualgia"), "theme.json", `{"cursor": {"light": "3", "dark": "4"}}`)
	theme, err = loadTheme("")
	if err != nil {
		t.Fatal(err)
	}
	if theme.Cursor.Light != "3" || theme.Cursor.Dark != "4" {
		t.Errorf("cursor = %+v", theme.Cursor)
	}
}

func TestLoadThemeProfile(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	writeTheme(t, filepath.Join(xdg, "tualgia"), "theme-work.json", `{"link": {"light": "5", "dark": "6"}}`)
	theme, err := loadTheme("work")
	if err != nil {
		t.Fatal(err)
	}
	if theme.Link.Light != "5" || theme.Link.Dark != "6" {
		t.Errorf("link = %+v", theme.Link)
	}
}

func TestApplyTheme(t *testing.T) {
	theme := defaultTheme()
	theme.Name = ColorPair{Light: "#111111", Dark: "#222222"}
	applyTheme(theme)
	defer applyTheme(defaultTheme())

	fg, ok := nameStyle.GetForeground().(lipgloss.AdaptiveColor)
	if !ok || fg.Light != "#111111" || fg.Dark != "#222222" {
		t.Fatalf("nameStyle fg = %#v", nameStyle.GetForeground())
	}
}

func TestProfileFileName(t *testing.T) {
	if got := profileFileName("theme", ""); got != "theme.json" {
		t.Errorf("got %q", got)
	}
	if got := profileFileName("config", "work"); got != "config-work.json" {
		t.Errorf("got %q", got)
	}
}
