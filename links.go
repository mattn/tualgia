package main

import (
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

// nostrLinkRe matches both NIP-21 references and plain http(s) URLs so that
// link numbering follows the order of appearance in the content.
var nostrLinkRe = regexp.MustCompile(`nostr:(n(?:pub|profile|ote|event|addr)1[a-z0-9]+)|https?://[^\s"'<>]+`)

// nostrLink is a followable reference found in note content: a NIP-21
// entity or a web URL.
type nostrLink struct {
	raw    string   // bech32 entity without the nostr: prefix
	prefix string   // npub, nprofile, note, nevent, url
	id     string   // event id for note/nevent
	pubkey string   // public key for npub/nprofile
	relays []string // relay hints
	url    string   // web URL
}

// trailingJunk is punctuation that commonly follows a URL in prose but is
// not part of it.
const trailingJunk = ")]}>.,;:!?'\"。、」』）"

func trimURL(s string) string {
	return strings.TrimRight(s, trailingJunk)
}

func parseLink(bech string) (nostrLink, bool) {
	prefix, data, err := nip19.Decode(bech)
	if err != nil {
		return nostrLink{}, false
	}
	l := nostrLink{raw: bech, prefix: prefix}
	switch prefix {
	case "npub":
		l.pubkey = data.(string)
	case "nprofile":
		p := data.(nostr.ProfilePointer)
		l.pubkey = p.PublicKey
		l.relays = p.Relays
	case "note":
		l.id = data.(string)
	case "nevent":
		p := data.(nostr.EventPointer)
		l.id = p.ID
		l.relays = p.Relays
	default: // naddr etc. are not supported yet
		return nostrLink{}, false
	}
	return l, true
}

// openBrowser opens url with the platform's default browser.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		if isWSL() {
			if p, err := exec.LookPath("wslview"); err == nil {
				return exec.Command(p, url).Start()
			}
			return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start()
		}
		return exec.Command("xdg-open", url).Start()
	}
}

func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// extractLinks returns the followable links in content, in order of
// appearance. The order matches the numbering produced by linkify.
func extractLinks(content string) []nostrLink {
	var links []nostrLink
	for _, mm := range nostrLinkRe.FindAllStringSubmatch(content, -1) {
		if mm[1] == "" {
			if u := trimURL(mm[0]); u != "" {
				links = append(links, nostrLink{raw: u, prefix: "url", url: u})
			}
			continue
		}
		if l, ok := parseLink(mm[1]); ok {
			links = append(links, l)
		}
	}
	return links
}
