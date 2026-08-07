package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

const timelineLimit = 100

// Client wraps a relay pool with the signing key.
type Client struct {
	cfg     *Config
	pool    *nostr.SimplePool
	sk      string
	pub     string
	follows []string
}

func newClient(cfg *Config) (*Client, error) {
	var sk string
	if _, s, err := nip19.Decode(cfg.PrivateKey); err == nil {
		sk = s.(string)
	} else {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	pub, err := nostr.GetPublicKey(sk)
	if err != nil {
		return nil, err
	}

	pool := nostr.NewSimplePool(context.Background(),
		nostr.WithAuthHandler(func(ctx context.Context, authEvent nostr.RelayEvent) error {
			return authEvent.Sign(sk)
		}),
	)

	var follows []string
	for _, f := range cfg.FollowList {
		if pk, ok := normalizeKey(f); ok {
			follows = append(follows, pk)
		}
	}

	return &Client{cfg: cfg, pool: pool, sk: sk, pub: pub, follows: follows}, nil
}

func (c *Client) readRelays() []string {
	var relays []string
	for k, v := range c.cfg.Relays {
		if v.Read {
			relays = append(relays, k)
		}
	}
	return relays
}

func (c *Client) writeRelays() []string {
	var relays []string
	for k, v := range c.cfg.Relays {
		if v.Write {
			relays = append(relays, k)
		}
	}
	return relays
}

func (c *Client) searchRelays() []string {
	var relays []string
	for k, v := range c.cfg.Relays {
		if v.Read && v.Search {
			relays = append(relays, k)
		}
	}
	if len(relays) == 0 {
		// no NIP-50 relay configured; fall back to a public search relay
		relays = append(relays, "wss://search.nos.today")
	}
	return relays
}

func (c *Client) timelineFilter() nostr.Filter {
	f := nostr.Filter{Kinds: []int{nostr.KindTextNote}}
	if len(c.follows) > 0 {
		f.Authors = c.follows
	}
	return f
}

// LoadTimeline fetches recent notes from follows, newest first.
func (c *Client) LoadTimeline(ctx context.Context) ([]*nostr.Event, error) {
	return c.loadNotes(ctx, nil)
}

// LoadOlder fetches notes created at or before until, newest first. The
// boundary is inclusive so same-second notes on other relays are not lost;
// the caller is expected to deduplicate.
func (c *Client) LoadOlder(ctx context.Context, until nostr.Timestamp) ([]*nostr.Event, error) {
	return c.loadNotes(ctx, &until)
}

func (c *Client) loadNotes(ctx context.Context, until *nostr.Timestamp) ([]*nostr.Event, error) {
	relays := c.readRelays()
	if len(relays) == 0 {
		return nil, errors.New("no read relays available")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	f := c.timelineFilter()
	f.Limit = timelineLimit
	f.Until = until

	seen := map[string]*nostr.Event{}
	for re := range c.pool.SubManyEose(ctx, relays, nostr.Filters{f}) {
		if re.Event == nil {
			continue
		}
		seen[re.Event.ID] = re.Event
	}
	evs := make([]*nostr.Event, 0, len(seen))
	for _, ev := range seen {
		evs = append(evs, ev)
	}
	sort.Slice(evs, func(i, j int) bool {
		return evs[i].CreatedAt > evs[j].CreatedAt
	})
	if len(evs) > timelineLimit {
		evs = evs[:timelineLimit]
	}
	return evs, nil
}

// Subscribe streams new notes from follows as they arrive.
func (c *Client) Subscribe(ctx context.Context) <-chan *nostr.Event {
	out := make(chan *nostr.Event)
	relays := c.readRelays()
	f := c.timelineFilter()
	since := nostr.Now()
	f.Since = &since

	go func() {
		defer close(out)
		for re := range c.pool.SubMany(ctx, relays, nostr.Filters{f}) {
			if re.Event == nil {
				continue
			}
			select {
			case out <- re.Event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// FetchProfiles fetches kind-0 metadata for the given authors.
func (c *Client) FetchProfiles(ctx context.Context, pubkeys []string) map[string]Profile {
	profiles := map[string]Profile{}
	if len(pubkeys) == 0 {
		return profiles
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	f := nostr.Filter{
		Kinds:   []int{nostr.KindProfileMetadata},
		Authors: pubkeys,
	}
	newest := map[string]*nostr.Event{}
	for re := range c.pool.SubManyEose(ctx, c.readRelays(), nostr.Filters{f}) {
		if re.Event == nil {
			continue
		}
		if cur, ok := newest[re.Event.PubKey]; !ok || re.Event.CreatedAt > cur.CreatedAt {
			newest[re.Event.PubKey] = re.Event
		}
	}
	for pk, ev := range newest {
		var p Profile
		if err := json.Unmarshal([]byte(ev.Content), &p); err == nil {
			profiles[pk] = p
		}
	}
	return profiles
}

// Search queries NIP-50 capable relays for notes matching q, newest first.
func (c *Client) Search(ctx context.Context, q string, limit int) ([]*nostr.Event, error) {
	relays := c.searchRelays()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	f := nostr.Filter{
		Kinds:  []int{nostr.KindTextNote},
		Search: q,
		Limit:  limit,
	}
	seen := map[string]*nostr.Event{}
	for re := range c.pool.SubManyEose(ctx, relays, nostr.Filters{f}) {
		if re.Event == nil {
			continue
		}
		seen[re.Event.ID] = re.Event
	}
	evs := make([]*nostr.Event, 0, len(seen))
	for _, ev := range seen {
		evs = append(evs, ev)
	}
	sort.Slice(evs, func(i, j int) bool {
		return evs[i].CreatedAt > evs[j].CreatedAt
	})
	if len(evs) > limit {
		evs = evs[:limit]
	}
	return evs, nil
}

// FetchEvent fetches a single event by id, trying relay hints in addition
// to the read relays.
func (c *Client) FetchEvent(ctx context.Context, id string, hints []string) (*nostr.Event, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	relays := append(c.readRelays(), hints...)
	f := nostr.Filter{IDs: []string{id}}
	for re := range c.pool.SubManyEose(ctx, relays, nostr.Filters{f}) {
		if re.Event != nil {
			return re.Event, nil
		}
	}
	return nil, errors.New("note not found on any relay")
}

// FetchNotesBy fetches the most recent notes of one author, newest first.
func (c *Client) FetchNotesBy(ctx context.Context, pubkey string, hints []string, limit int) []*nostr.Event {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	relays := append(c.readRelays(), hints...)
	f := nostr.Filter{
		Kinds:   []int{nostr.KindTextNote},
		Authors: []string{pubkey},
		Limit:   limit,
	}
	seen := map[string]*nostr.Event{}
	for re := range c.pool.SubManyEose(ctx, relays, nostr.Filters{f}) {
		if re.Event == nil {
			continue
		}
		seen[re.Event.ID] = re.Event
	}
	evs := make([]*nostr.Event, 0, len(seen))
	for _, ev := range seen {
		evs = append(evs, ev)
	}
	sort.Slice(evs, func(i, j int) bool {
		return evs[i].CreatedAt > evs[j].CreatedAt
	})
	if len(evs) > limit {
		evs = evs[:limit]
	}
	return evs
}

// ReactionInfo is the reaction summary for one note.
type ReactionInfo struct {
	Count int
	Mine  bool
}

// FetchReactions fetches NIP-25 reactions to the given note ids and counts
// unique reactors per note. Downvotes ("-") are ignored.
func (c *Client) FetchReactions(ctx context.Context, ids []string) map[string]ReactionInfo {
	result := map[string]ReactionInfo{}
	if len(ids) == 0 {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	f := nostr.Filter{
		Kinds: []int{nostr.KindReaction},
		Tags:  nostr.TagMap{"e": ids},
	}
	reactors := map[string]map[string]bool{}
	for re := range c.pool.SubManyEose(ctx, c.readRelays(), nostr.Filters{f}) {
		if re.Event == nil || re.Event.Content == "-" {
			continue
		}
		// per NIP-25 the last "e" tag is the note being reacted to
		id := ""
		for _, tag := range re.Event.Tags {
			if len(tag) >= 2 && tag[0] == "e" {
				id = tag[1]
			}
		}
		if !wanted[id] {
			continue
		}
		set := reactors[id]
		if set == nil {
			set = map[string]bool{}
			reactors[id] = set
		}
		set[re.Event.PubKey] = true
	}
	for id, set := range reactors {
		result[id] = ReactionInfo{Count: len(set), Mine: set[c.pub]}
	}
	return result
}

// Publish signs ev and sends it to all write relays. It succeeds when at
// least one relay accepts the event.
func (c *Client) Publish(ctx context.Context, ev *nostr.Event) error {
	relays := c.writeRelays()
	if len(relays) == 0 {
		return errors.New("no write relays available")
	}
	if err := ev.Sign(c.sk); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var lastErr error
	ok := 0
	for res := range c.pool.PublishMany(ctx, relays, *ev) {
		if res.Error != nil {
			lastErr = fmt.Errorf("%s: %w", res.RelayURL, res.Error)
		} else {
			ok++
		}
	}
	if ok == 0 {
		if lastErr != nil {
			return lastErr
		}
		return errors.New("publish failed")
	}
	return nil
}

// Post publishes a plain text note.
func (c *Client) Post(ctx context.Context, content string) (*nostr.Event, error) {
	ev := &nostr.Event{
		Kind:      nostr.KindTextNote,
		Content:   content,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{},
	}
	if err := c.Publish(ctx, ev); err != nil {
		return nil, err
	}
	return ev, nil
}

// Reply publishes a NIP-10 reply to parent.
func (c *Client) Reply(ctx context.Context, parent *nostr.Event, content string) (*nostr.Event, error) {
	tags := nostr.Tags{}
	if root := rootTag(parent); root != "" {
		tags = append(tags, nostr.Tag{"e", root, "", "root"})
		tags = append(tags, nostr.Tag{"e", parent.ID, "", "reply"})
	} else {
		tags = append(tags, nostr.Tag{"e", parent.ID, "", "root"})
	}
	tags = append(tags, nostr.Tag{"p", parent.PubKey})

	ev := &nostr.Event{
		Kind:      nostr.KindTextNote,
		Content:   content,
		CreatedAt: nostr.Now(),
		Tags:      tags,
	}
	if err := c.Publish(ctx, ev); err != nil {
		return nil, err
	}
	return ev, nil
}

// rootTag returns the id of the thread root that ev belongs to, or "" when
// ev is not a reply. Events without NIP-10 markers fall back to the first
// "e" tag, the positional convention.
func rootTag(ev *nostr.Event) string {
	first := ""
	for _, tag := range ev.Tags {
		if len(tag) < 2 || tag[0] != "e" {
			continue
		}
		if len(tag) >= 4 && tag[3] == "root" {
			return tag[1]
		}
		if first == "" {
			first = tag[1]
		}
	}
	return first
}

// React publishes a NIP-25 reaction ("+") to target.
func (c *Client) React(ctx context.Context, target *nostr.Event) error {
	ev := &nostr.Event{
		Kind:      nostr.KindReaction,
		Content:   "+",
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			nostr.Tag{"e", target.ID},
			nostr.Tag{"p", target.PubKey},
			nostr.Tag{"k", fmt.Sprint(target.Kind)},
		},
	}
	return c.Publish(ctx, ev)
}
