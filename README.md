# tualgia

TUI client for [nostr](https://nostr.com/), fully keyboard driven.

![](https://img.shields.io/badge/status-experimental-orange)

## Usage

```
tualgia [-a profile]
```

tualgia reads the configuration from `$XDG_CONFIG_HOME/tualgia/config.json`,
falling back to [algia](https://github.com/mattn/algia)'s
`$XDG_CONFIG_HOME/algia/config.json`. If you already use algia, no setup is
required.

### Keybindings

| Key        | Action                     |
|------------|----------------------------|
| `j` / `k`  | move down / up             |
| `g` / `G`  | go to top / bottom (bottom loads older notes) |
| `ctrl+d/u` | move faster                |
| `n`        | compose a new note         |
| `r`        | reply to the selected note |
| `f` / `+`  | like the selected note     |
| `o` / `1`-`9` | follow a link (`nostr:` in app, URL in browser) |
| `i`        | show images of the note (sixel) |
| `enter` / `l` | open the selected note in a detail view |
| `esc` / `q` | go back from a detail view / search |
| `/`        | search notes (NIP-50)      |
| `R`        | reload timeline            |
| `?`        | help                       |
| `q`        | quit                       |

In the composer, `ctrl+s` sends the note and `esc` cancels.

Reaction counts are shown as `♥n` next to each note and refreshed
periodically; notes you have liked are highlighted.

Search uses relays configured with `"search": true`; when none is
configured it falls back to `wss://search.nos.today`. Search results are
browsable like the timeline, and `esc` brings the timeline back.

`i` renders the images referenced by the selected note with
[sixel](https://en.wikipedia.org/wiki/Sixel); it requires a sixel capable
terminal. Any key returns to the timeline. Web URLs open with `wslview` /
`rundll32.exe` on WSL, `xdg-open` on Linux and `open` on macOS.

## Installation

```
go install github.com/mattn/tualgia@latest
```

## License

MIT

## Author

Yasuhiro Matsumoto (a.k.a. mattn)
