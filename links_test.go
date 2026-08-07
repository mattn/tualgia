package main

import (
	"testing"

	"github.com/nbd-wtf/go-nostr/nip19"
)

func TestExtractLinks(t *testing.T) {
	npub, err := nip19.EncodePublicKey("3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d")
	if err != nil {
		t.Fatal(err)
	}
	note, err := nip19.EncodeNote("d1b9dd0d06b93a50f327af2f8e2c1d31792d4b6f9d2727abcadf8fef94243b17")
	if err != nil {
		t.Fatal(err)
	}
	content := "hello nostr:" + npub + " see https://example.com/foo). then nostr:" + note + " and broken nostr:nevent1xxxx"

	links := extractLinks(content)
	if len(links) != 3 {
		t.Fatalf("want 3 links, got %d: %+v", len(links), links)
	}
	if links[0].prefix != "npub" || links[0].pubkey == "" {
		t.Errorf("link 0: %+v", links[0])
	}
	if links[1].prefix != "url" || links[1].url != "https://example.com/foo" {
		t.Errorf("link 1: %+v", links[1])
	}
	if links[2].prefix != "note" || links[2].id == "" {
		t.Errorf("link 2: %+v", links[2])
	}
}

func TestExtractLinksNone(t *testing.T) {
	if links := extractLinks("no links here, just text and npub1 mention"); len(links) != 0 {
		t.Fatalf("want 0 links, got %+v", links)
	}
}

func TestExtractLinksURLOnly(t *testing.T) {
	links := extractLinks("画像です https://example.com/a.png。")
	if len(links) != 1 {
		t.Fatalf("want 1 link, got %+v", links)
	}
	if links[0].url != "https://example.com/a.png" {
		t.Errorf("url = %q", links[0].url)
	}
	if !isImageURL(links[0].url) {
		t.Errorf("should be an image URL: %q", links[0].url)
	}
}
