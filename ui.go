package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

type mode int

const (
	modeTimeline mode = iota
	modeCompose
	modeHelp
	modeDetail
	modeSearch
)

var (
	nameStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "39"})
	faintStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "243"})
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "205", Dark: "213"})
	headerStyle   = lipgloss.NewStyle().Bold(true).Reverse(true)
	statusStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "22", Dark: "114"})
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "124", Dark: "203"})
	replyToStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "94", Dark: "179"})
	likedStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "162", Dark: "205"})
	linkStyle     = lipgloss.NewStyle().Underline(true).Foreground(lipgloss.AdaptiveColor{Light: "26", Dark: "45"})
	helpKeyStyle  = lipgloss.NewStyle().Bold(true).Width(12)
	composeBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

type (
	timelineMsg     []*nostr.Event
	olderMsg        []*nostr.Event
	liveEventMsg    *nostr.Event
	liveClosedMsg   struct{}
	profilesMsg     map[string]Profile
	publishedMsg    struct{ ev *nostr.Event }
	reactedMsg      struct{ id string }
	reactionsMsg    map[string]ReactionInfo
	reactionTickMsg struct{}
	errMsg          struct{ err error }
	clearStatusMsg  struct{ id int }

	openedMsg struct{ url string }

	sixelMsg     struct{ files []string }
	imageDoneMsg struct{ err error }

	searchMsg struct {
		query string
		evs   []*nostr.Event
	}

	detailEventMsg   struct{ ev *nostr.Event }
	detailProfileMsg struct {
		pubkey   string
		profiles map[string]Profile
		notes    []*nostr.Event
	}
)

// detailPage is one entry of the detail-view stack: either a single note
// (ev != nil) or a profile with its recent notes.
type detailPage struct {
	ev     *nostr.Event
	pubkey string
	notes  []*nostr.Event
	scroll int
}

// reactionRefreshInterval is how often reaction counts of loaded notes are
// re-fetched; reactions arrive from anyone at any time, so unlike notes they
// cannot be streamed with the timeline subscription's author filter.
const reactionRefreshInterval = 60 * time.Second

// reactionFetchMax bounds the number of note ids per reaction query so the
// filter stays acceptable to relays.
const reactionFetchMax = 200

type model struct {
	ctx    context.Context
	client *Client

	width  int
	height int

	mode    mode
	events  []*nostr.Event
	seen    map[string]bool
	cursor  int
	offset  int
	pending int // new events above the viewport

	profiles  map[string]Profile
	requested map[string]bool
	reactions map[string]ReactionInfo

	ta            textarea.Model
	replyTo       *nostr.Event
	composeReturn mode

	stack []*detailPage

	si          textinput.Model
	searching   bool // timeline shows search results
	searchQuery string
	savedEvents []*nostr.Event
	savedCursor int
	savedOffset int
	savedPend   int

	live <-chan *nostr.Event

	loading      bool
	loadingOlder bool
	noMoreOlder  bool
	status       string
	statusID     int
	isError      bool
}

func newModel(ctx context.Context, client *Client) *model {
	ta := textarea.New()
	ta.Placeholder = "What's on your mind?"
	ta.CharLimit = 0
	ta.ShowLineNumbers = false

	si := textinput.New()
	si.Prompt = "/"
	si.Placeholder = "search"

	profiles := map[string]Profile{}
	for k, v := range client.cfg.profiles {
		profiles[k] = v
	}

	return &model{
		ctx:       ctx,
		client:    client,
		mode:      modeTimeline,
		seen:      map[string]bool{},
		profiles:  profiles,
		requested: map[string]bool{},
		reactions: map[string]ReactionInfo{},
		ta:        ta,
		si:        si,
		loading:   true,
	}
}

func (m *model) Init() tea.Cmd {
	m.live = m.client.Subscribe(m.ctx)
	return tea.Batch(m.loadTimeline(), m.waitLive(), reactionTick())
}

func reactionTick() tea.Cmd {
	return tea.Tick(reactionRefreshInterval, func(time.Time) tea.Msg {
		return reactionTickMsg{}
	})
}

// commands

func (m *model) loadTimeline() tea.Cmd {
	return func() tea.Msg {
		evs, err := m.client.LoadTimeline(m.ctx)
		if err != nil {
			return errMsg{err}
		}
		return timelineMsg(evs)
	}
}

func (m *model) waitLive() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.live
		if !ok {
			return liveClosedMsg{}
		}
		return liveEventMsg(ev)
	}
}

func (m *model) runSearch(query string) tea.Cmd {
	return func() tea.Msg {
		evs, err := m.client.Search(m.ctx, query, timelineLimit)
		if err != nil {
			return errMsg{err}
		}
		return searchMsg{query, evs}
	}
}

// exitSearch restores the timeline stashed when search results were shown.
func (m *model) exitSearch() {
	m.searching = false
	m.searchQuery = ""
	m.events = m.savedEvents
	m.cursor = m.savedCursor
	m.offset = m.savedOffset
	m.pending = m.savedPend
	m.savedEvents = nil
}

func (m *model) loadOlder() tea.Cmd {
	oldest := m.events[len(m.events)-1].CreatedAt
	return func() tea.Msg {
		evs, err := m.client.LoadOlder(m.ctx, oldest)
		if err != nil {
			return errMsg{err}
		}
		return olderMsg(evs)
	}
}

func (m *model) fetchReactions(ids []string) tea.Cmd {
	if len(ids) > reactionFetchMax {
		ids = ids[:reactionFetchMax]
	}
	return func() tea.Msg {
		return reactionsMsg(m.client.FetchReactions(m.ctx, ids))
	}
}

func (m *model) loadedIDs() []string {
	ids := make([]string, 0, len(m.events))
	for _, ev := range m.events {
		ids = append(ids, ev.ID)
	}
	return ids
}

func (m *model) fetchProfiles(pubkeys []string) tea.Cmd {
	return func() tea.Msg {
		return profilesMsg(m.client.FetchProfiles(m.ctx, pubkeys))
	}
}

func (m *model) setStatus(s string, isError bool) tea.Cmd {
	m.status = s
	m.isError = isError
	m.statusID++
	id := m.statusID
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		return clearStatusMsg{id}
	})
}

// missingProfiles returns authors and mentioned users of evs whose profile
// is neither cached nor already requested, marking them as requested.
func (m *model) missingProfiles(evs []*nostr.Event) []string {
	var pubkeys []string
	add := func(pk string) {
		if _, ok := m.profiles[pk]; ok {
			return
		}
		if m.requested[pk] {
			return
		}
		m.requested[pk] = true
		pubkeys = append(pubkeys, pk)
	}
	for _, ev := range evs {
		add(ev.PubKey)
		for _, l := range extractLinks(ev.Content) {
			if l.pubkey != "" {
				add(l.pubkey)
			}
		}
	}
	return pubkeys
}

// linkify replaces nostr: links and URLs in content with numbered labels
// and returns the decorated content together with the links in numbering
// order (the same order extractLinks produces).
func (m *model) linkify(content string) (string, []nostrLink) {
	var links []nostrLink
	out := nostrLinkRe.ReplaceAllStringFunc(content, func(s string) string {
		if !strings.HasPrefix(s, "nostr:") {
			u := trimURL(s)
			if u == "" {
				return s
			}
			links = append(links, nostrLink{raw: u, prefix: "url", url: u})
			return linkStyle.Render(fmt.Sprintf("[%d]%s", len(links), u)) + s[len(u):]
		}
		l, ok := parseLink(strings.TrimPrefix(s, "nostr:"))
		if !ok {
			return s
		}
		links = append(links, l)
		return linkStyle.Render(fmt.Sprintf("[%d]%s", len(links), m.linkLabel(l)))
	})
	return out, links
}

func (m *model) linkLabel(l nostrLink) string {
	if l.pubkey != "" {
		return "@" + m.displayName(l.pubkey)
	}
	if len(l.raw) > 12 {
		return l.raw[:12] + "…"
	}
	return l.raw
}

// openLink resolves a link: nostr entities become a detail page, web URLs
// open in the browser.
func (m *model) openLink(l nostrLink) tea.Cmd {
	switch {
	case l.url != "":
		u := l.url
		return func() tea.Msg {
			if err := openBrowser(u); err != nil {
				return errMsg{err}
			}
			return openedMsg{u}
		}
	case l.id != "":
		return func() tea.Msg {
			ev, err := m.client.FetchEvent(m.ctx, l.id, l.relays)
			if err != nil {
				return errMsg{err}
			}
			return detailEventMsg{ev}
		}
	case l.pubkey != "":
		_, cached := m.profiles[l.pubkey]
		return func() tea.Msg {
			var profiles map[string]Profile
			if !cached {
				profiles = m.client.FetchProfiles(m.ctx, []string{l.pubkey})
			}
			notes := m.client.FetchNotesBy(m.ctx, l.pubkey, l.relays, 20)
			return detailProfileMsg{l.pubkey, profiles, notes}
		}
	}
	return nil
}

// showImages downloads the images referenced by ev and prepares sixel data
// for display.
func (m *model) showImages(ev *nostr.Event) tea.Cmd {
	var urls []string
	for _, l := range extractLinks(ev.Content) {
		if l.url != "" && isImageURL(l.url) {
			urls = append(urls, l.url)
		}
	}
	if len(urls) == 0 {
		return m.setStatus("no images in this note", true)
	}
	if len(urls) > 4 {
		urls = urls[:4]
	}
	return tea.Batch(m.setStatus("loading images...", false), func() tea.Msg {
		var files []string
		var lastErr error
		for _, u := range urls {
			b, err := fetchImageSixel(u, imageMaxWidth, imageMaxHeight)
			if err != nil {
				lastErr = err
				continue
			}
			f, err := os.CreateTemp("", "tualgia-*.six")
			if err != nil {
				lastErr = err
				continue
			}
			_, werr := f.Write(b)
			f.Close()
			if werr != nil {
				os.Remove(f.Name())
				lastErr = werr
				continue
			}
			files = append(files, f.Name())
		}
		if len(files) == 0 {
			if lastErr == nil {
				lastErr = errors.New("failed to load images")
			}
			return errMsg{lastErr}
		}
		return sixelMsg{files}
	})
}

// followLink opens the n-th (0-based) link of ev, if any.
func (m *model) followLink(ev *nostr.Event, n int) tea.Cmd {
	links := extractLinks(ev.Content)
	if len(links) == 0 {
		return m.setStatus("no links in this note", true)
	}
	if n >= len(links) {
		return m.setStatus(fmt.Sprintf("only %d link(s) in this note", len(links)), true)
	}
	return tea.Batch(m.openLink(links[n]), m.setStatus("opening "+links[n].prefix+"...", false))
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ta.SetWidth(m.width - 6)
		m.ta.SetHeight(min(8, max(3, m.height-8)))
		m.si.Width = max(m.width-6, 10)
		return m, nil

	case timelineMsg:
		m.loading = false
		target := &m.events
		if m.searching {
			// search results are on screen; the real timeline is stashed
			target = &m.savedEvents
		}
		selID := ""
		if !m.searching {
			if ev := m.selected(); ev != nil {
				selID = ev.ID
			}
		}
		for _, ev := range msg {
			if !m.seen[ev.ID] {
				m.seen[ev.ID] = true
				*target = append(*target, ev)
			}
		}
		sort.SliceStable(*target, func(i, j int) bool {
			return (*target)[i].CreatedAt > (*target)[j].CreatedAt
		})
		if selID != "" {
			for i, ev := range m.events {
				if ev.ID == selID {
					m.cursor = i
					break
				}
			}
		}
		ids := make([]string, 0, len(msg))
		for _, ev := range msg {
			ids = append(ids, ev.ID)
		}
		cmds := []tea.Cmd{m.fetchReactions(ids)}
		if pubkeys := m.missingProfiles(msg); len(pubkeys) > 0 {
			cmds = append(cmds, m.fetchProfiles(pubkeys))
		}
		return m, tea.Batch(cmds...)

	case olderMsg:
		m.loadingOlder = false
		selID := ""
		if ev := m.selected(); ev != nil {
			selID = ev.ID
		}
		var fresh []*nostr.Event
		for _, ev := range msg {
			if !m.seen[ev.ID] {
				m.seen[ev.ID] = true
				m.events = append(m.events, ev)
				fresh = append(fresh, ev)
			}
		}
		if len(fresh) == 0 {
			m.noMoreOlder = true
			return m, m.setStatus("no older notes", false)
		}
		sort.SliceStable(m.events, func(i, j int) bool {
			return m.events[i].CreatedAt > m.events[j].CreatedAt
		})
		// keep the selection on the same note after re-sorting
		for i, ev := range m.events {
			if ev.ID == selID {
				m.cursor = i
				break
			}
		}
		ids := make([]string, 0, len(fresh))
		for _, ev := range fresh {
			ids = append(ids, ev.ID)
		}
		cmds := []tea.Cmd{
			m.fetchReactions(ids),
			m.setStatus(fmt.Sprintf("loaded %d older notes", len(fresh)), false),
		}
		if pubkeys := m.missingProfiles(fresh); len(pubkeys) > 0 {
			cmds = append(cmds, m.fetchProfiles(pubkeys))
		}
		return m, tea.Batch(cmds...)

	case liveEventMsg:
		ev := (*nostr.Event)(msg)
		cmds := []tea.Cmd{m.waitLive()}
		if !m.seen[ev.ID] {
			m.seen[ev.ID] = true
			if m.searching {
				// the timeline is stashed while search results are shown
				m.savedEvents = append([]*nostr.Event{ev}, m.savedEvents...)
				if m.savedCursor > 0 || m.savedOffset > 0 {
					m.savedCursor++
					m.savedOffset++
					m.savedPend++
				}
			} else {
				m.events = append([]*nostr.Event{ev}, m.events...)
				if m.cursor > 0 || m.offset > 0 {
					// keep the selection on the same note
					m.cursor++
					m.offset++
					m.pending++
				}
			}
			if pubkeys := m.missingProfiles([]*nostr.Event{ev}); len(pubkeys) > 0 {
				cmds = append(cmds, m.fetchProfiles(pubkeys))
			}
		}
		return m, tea.Batch(cmds...)

	case searchMsg:
		if !m.searching {
			m.searching = true
			m.savedEvents = m.events
			m.savedCursor = m.cursor
			m.savedOffset = m.offset
			m.savedPend = m.pending
		}
		m.searchQuery = msg.query
		m.events = msg.evs
		m.cursor = 0
		m.offset = 0
		m.pending = 0
		ids := make([]string, 0, len(msg.evs))
		for _, ev := range msg.evs {
			ids = append(ids, ev.ID)
		}
		cmds := []tea.Cmd{
			m.fetchReactions(ids),
			m.setStatus(fmt.Sprintf("%d results for %q", len(msg.evs), msg.query), false),
		}
		if pubkeys := m.missingProfiles(msg.evs); len(pubkeys) > 0 {
			cmds = append(cmds, m.fetchProfiles(pubkeys))
		}
		return m, tea.Batch(cmds...)

	case liveClosedMsg:
		return m, nil

	case profilesMsg:
		for k, v := range msg {
			m.profiles[k] = v
		}
		return m, nil

	case publishedMsg:
		return m, m.setStatus("posted ✓", false)

	case reactedMsg:
		info := m.reactions[msg.id]
		if !info.Mine {
			info.Mine = true
			info.Count++
			m.reactions[msg.id] = info
		}
		return m, m.setStatus("liked ✓", false)

	case reactionsMsg:
		for id, info := range msg {
			m.reactions[id] = info
		}
		return m, nil

	case reactionTickMsg:
		// refresh reactions around the viewport, not just the newest notes
		ids := m.loadedIDs()
		if m.offset < len(ids) {
			ids = ids[m.offset:]
		}
		return m, tea.Batch(m.fetchReactions(ids), reactionTick())

	case openedMsg:
		return m, m.setStatus("opened in browser: "+msg.url, false)

	case sixelMsg:
		files := msg.files
		script := `for f in "$@"; do clear; cat "$f"; printf '\n[press any key]'; read -rsn1; done; clear`
		args := append([]string{"-c", script, "tualgia"}, files...)
		return m, tea.ExecProcess(exec.Command("bash", args...), func(err error) tea.Msg {
			for _, f := range files {
				os.Remove(f)
			}
			return imageDoneMsg{err}
		})

	case imageDoneMsg:
		if msg.err != nil {
			return m, m.setStatus(msg.err.Error(), true)
		}
		m.status = ""
		return m, nil

	case detailEventMsg:
		m.stack = append(m.stack, &detailPage{ev: msg.ev})
		m.mode = modeDetail
		cmds := []tea.Cmd{m.fetchReactions([]string{msg.ev.ID})}
		if pubkeys := m.missingProfiles([]*nostr.Event{msg.ev}); len(pubkeys) > 0 {
			cmds = append(cmds, m.fetchProfiles(pubkeys))
		}
		return m, tea.Batch(cmds...)

	case detailProfileMsg:
		for k, v := range msg.profiles {
			m.profiles[k] = v
		}
		m.stack = append(m.stack, &detailPage{pubkey: msg.pubkey, notes: msg.notes})
		m.mode = modeDetail
		ids := make([]string, 0, len(msg.notes))
		for _, ev := range msg.notes {
			ids = append(ids, ev.ID)
		}
		cmds := []tea.Cmd{m.fetchReactions(ids)}
		if pubkeys := m.missingProfiles(msg.notes); len(pubkeys) > 0 {
			cmds = append(cmds, m.fetchProfiles(pubkeys))
		}
		return m, tea.Batch(cmds...)

	case errMsg:
		m.loading = false
		m.loadingOlder = false
		return m, m.setStatus(msg.err.Error(), true)

	case clearStatusMsg:
		if msg.id == m.statusID {
			m.status = ""
		}
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modeCompose:
			return m.updateCompose(msg)
		case modeHelp:
			m.mode = modeTimeline
			return m, nil
		case modeDetail:
			return m.updateDetail(msg)
		case modeSearch:
			return m.updateSearchInput(msg)
		default:
			return m.updateTimeline(msg)
		}
	}
	return m, nil
}

func (m *model) updateSearchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit

	case "esc":
		m.mode = modeTimeline
		m.si.Blur()
		return m, nil

	case "enter":
		query := strings.TrimSpace(m.si.Value())
		m.mode = modeTimeline
		m.si.Blur()
		if query == "" {
			return m, nil
		}
		return m, tea.Batch(m.runSearch(query), m.setStatus("searching "+query+"...", false))
	}

	var cmd tea.Cmd
	m.si, cmd = m.si.Update(msg)
	return m, cmd
}

func (m *model) updateTimeline(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit

	case "q":
		if m.searching {
			m.exitSearch()
			return m, nil
		}
		return m, tea.Quit

	case "esc":
		if m.searching {
			m.exitSearch()
		}
		return m, nil

	case "/":
		m.si.SetValue("")
		m.mode = modeSearch
		return m, m.si.Focus()

	case "j", "down":
		if m.cursor < len(m.events)-1 {
			m.cursor++
		}

	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}

	case "g", "home":
		m.cursor = 0
		m.offset = 0
		m.pending = 0

	case "G", "end":
		if len(m.events) > 0 {
			m.cursor = len(m.events) - 1
			m.offset = m.cursor
		}

	case "ctrl+d", "pgdown":
		m.cursor = min(m.cursor+5, max(len(m.events)-1, 0))

	case "ctrl+u", "pgup":
		m.cursor = max(m.cursor-5, 0)

	case "n":
		m.replyTo = nil
		m.ta.Reset()
		m.ta.Placeholder = "What's on your mind?"
		m.mode = modeCompose
		m.composeReturn = modeTimeline
		return m, m.ta.Focus()

	case "r":
		if ev := m.selected(); ev != nil {
			m.replyTo = ev
			m.ta.Reset()
			m.ta.Placeholder = "Reply to " + m.displayName(ev.PubKey)
			m.mode = modeCompose
			m.composeReturn = modeTimeline
			return m, m.ta.Focus()
		}

	case "enter", "l":
		if ev := m.selected(); ev != nil {
			m.stack = append(m.stack, &detailPage{ev: ev})
			m.mode = modeDetail
		}

	case "o":
		if ev := m.selected(); ev != nil {
			return m, m.followLink(ev, 0)
		}

	case "i":
		if ev := m.selected(); ev != nil {
			return m, m.showImages(ev)
		}

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if ev := m.selected(); ev != nil {
			return m, m.followLink(ev, int(msg.String()[0]-'1'))
		}

	case "f", "+":
		if ev := m.selected(); ev != nil {
			return m, func() tea.Msg {
				if err := m.client.React(m.ctx, ev); err != nil {
					return errMsg{err}
				}
				return reactedMsg{ev.ID}
			}
		}

	case "R":
		if m.searching {
			return m, tea.Batch(m.runSearch(m.searchQuery), m.setStatus("searching again...", false))
		}
		m.loading = true
		m.noMoreOlder = false
		return m, tea.Batch(m.loadTimeline(), m.setStatus("reloading...", false))

	case "?":
		m.mode = modeHelp
	}

	if m.cursor <= m.offset {
		m.pending = 0
	}
	// reached the bottom: pull older notes (not for search results)
	if !m.searching && len(m.events) > 0 && m.cursor >= len(m.events)-1 && !m.loadingOlder && !m.noMoreOlder {
		m.loadingOlder = true
		return m, tea.Batch(m.loadOlder(), m.setStatus("loading older notes...", false))
	}
	return m, nil
}

func (m *model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	page := m.stack[len(m.stack)-1]
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit

	case "q", "esc", "backspace", "h", "left":
		m.stack = m.stack[:len(m.stack)-1]
		if len(m.stack) == 0 {
			m.mode = modeTimeline
		}

	case "j", "down":
		page.scroll++

	case "k", "up":
		if page.scroll > 0 {
			page.scroll--
		}

	case "g", "home":
		page.scroll = 0

	case "r":
		if page.ev != nil {
			m.replyTo = page.ev
			m.ta.Reset()
			m.ta.Placeholder = "Reply to " + m.displayName(page.ev.PubKey)
			m.mode = modeCompose
			m.composeReturn = modeDetail
			return m, m.ta.Focus()
		}

	case "f", "+":
		if page.ev != nil {
			ev := page.ev
			return m, func() tea.Msg {
				if err := m.client.React(m.ctx, ev); err != nil {
					return errMsg{err}
				}
				return reactedMsg{ev.ID}
			}
		}

	case "o":
		if page.ev != nil {
			return m, m.followLink(page.ev, 0)
		}
		// on a profile page, o opens the website
		if p, ok := m.profiles[page.pubkey]; ok && p.Website != "" {
			return m, m.openLink(nostrLink{prefix: "url", url: trimURL(p.Website)})
		}

	case "i":
		if page.ev != nil {
			return m, m.showImages(page.ev)
		}

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		n := int(msg.String()[0] - '1')
		if page.ev != nil {
			return m, m.followLink(page.ev, n)
		}
		// on a profile page, numbers open the listed notes
		if n < len(page.notes) {
			m.stack = append(m.stack, &detailPage{ev: page.notes[n]})
		}
	}
	return m, nil
}

func (m *model) updateCompose(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = m.composeReturn
		m.ta.Blur()
		return m, nil

	case "ctrl+s":
		content := strings.TrimSpace(m.ta.Value())
		if content == "" {
			return m, m.setStatus("empty note not sent", true)
		}
		replyTo := m.replyTo
		m.mode = m.composeReturn
		m.ta.Blur()
		m.ta.Reset()
		cmd := func() tea.Msg {
			var ev *nostr.Event
			var err error
			if replyTo != nil {
				ev, err = m.client.Reply(m.ctx, replyTo, content)
			} else {
				ev, err = m.client.Post(m.ctx, content)
			}
			if err != nil {
				return errMsg{err}
			}
			return publishedMsg{ev}
		}
		return m, tea.Batch(cmd, m.setStatus("sending...", false))
	}

	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m *model) selected() *nostr.Event {
	if m.cursor >= 0 && m.cursor < len(m.events) {
		return m.events[m.cursor]
	}
	return nil
}

// helpers for names and times

func (m *model) displayName(pubkey string) string {
	if p, ok := m.profiles[pubkey]; ok {
		if p.DisplayName != "" {
			return p.DisplayName
		}
		if p.Name != "" {
			return p.Name
		}
	}
	return shortNpub(pubkey)
}

func shortNpub(pubkey string) string {
	if npub, err := nip19.EncodePublicKey(pubkey); err == nil && len(npub) > 12 {
		return npub[:12] + "…"
	}
	if len(pubkey) > 8 {
		return pubkey[:8]
	}
	return pubkey
}

func formatTime(t time.Time) string {
	t = t.Local()
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("01/02 15:04")
}

// view

func (m *model) View() string {
	if m.width == 0 {
		return "loading..."
	}
	switch m.mode {
	case modeCompose:
		return m.viewCompose()
	case modeHelp:
		return m.viewHelp()
	case modeDetail:
		return m.viewDetail()
	}
	return m.viewTimeline() // modeTimeline and modeSearch
}

func (m *model) viewDetail() string {
	page := m.stack[len(m.stack)-1]
	var body string
	if page.ev != nil {
		body = m.renderDetailEvent(page.ev)
	} else {
		body = m.renderDetailProfile(page)
	}

	lines := strings.Split(body, "\n")
	avail := m.height - 2
	if page.scroll > len(lines)-avail {
		page.scroll = max(len(lines)-avail, 0)
	}
	end := min(page.scroll+avail, len(lines))
	window := strings.Join(lines[page.scroll:end], "\n")

	var b strings.Builder
	b.WriteString(m.viewHeader())
	b.WriteString("\n")
	b.WriteString(window)
	if n := avail - (end - page.scroll); n > 0 {
		b.WriteString(strings.Repeat("\n", n))
	}
	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(m.viewStatus())
	} else {
		hint := "j/k:scroll  o/1-9:link  i:image  r:reply  f:like  esc:back"
		if page.ev == nil {
			hint = "j/k:scroll  1-9:open note  esc:back"
		}
		b.WriteString(faintStyle.Render(truncate(hint, m.width)))
	}
	return b.String()
}

func (m *model) renderDetailEvent(ev *nostr.Event) string {
	width := max(m.width-4, 10)

	nm := m.displayName(ev.PubKey)
	npub, _ := nip19.EncodePublicKey(ev.PubKey)
	head := nameStyle.Render(nm) + "  " + faintStyle.Render(formatTime(ev.CreatedAt.Time()))
	if info, ok := m.reactions[ev.ID]; ok && info.Count > 0 {
		like := fmt.Sprintf("♥%d", info.Count)
		if info.Mine {
			head += "  " + likedStyle.Render(like)
		} else {
			head += "  " + faintStyle.Render(like)
		}
	}

	content, _ := m.linkify(strings.TrimRight(ev.Content, "\n"))
	content = wrap.String(content, width)

	var b strings.Builder
	b.WriteString("  " + head + "\n")
	b.WriteString("  " + faintStyle.Render(npub) + "\n\n")
	for _, line := range strings.Split(content, "\n") {
		b.WriteString("  " + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *model) renderDetailProfile(page *detailPage) string {
	width := max(m.width-4, 10)
	p := m.profiles[page.pubkey]
	npub, _ := nip19.EncodePublicKey(page.pubkey)

	var b strings.Builder
	title := m.displayName(page.pubkey)
	if p.Name != "" && p.DisplayName != "" && p.Name != p.DisplayName {
		title += "  " + faintStyle.Render("@"+p.Name)
	}
	b.WriteString("  " + nameStyle.Render(title) + "\n")
	b.WriteString("  " + faintStyle.Render(npub) + "\n")
	if p.Nip05 != "" {
		b.WriteString("  " + faintStyle.Render(p.Nip05) + "\n")
	}
	if p.Website != "" {
		b.WriteString("  " + linkStyle.Render(p.Website) + "\n")
	}
	if p.About != "" {
		b.WriteString("\n")
		for _, line := range strings.Split(wrap.String(p.About, width), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}

	if len(page.notes) > 0 {
		b.WriteString("\n  " + faintStyle.Render("── recent notes ──") + "\n")
		for i, ev := range page.notes {
			label := "   "
			if i < 9 {
				label = faintStyle.Render(fmt.Sprintf("[%d]", i+1))
			}
			meta := faintStyle.Render(formatTime(ev.CreatedAt.Time()))
			content, _ := m.linkify(strings.TrimRight(ev.Content, "\n"))
			b.WriteString("\n  " + label + " " + meta + "\n")
			for _, line := range strings.Split(wrap.String(content, width-4), "\n") {
				b.WriteString("      " + line + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *model) viewTimeline() string {
	var b strings.Builder
	b.WriteString(m.viewHeader())
	b.WriteString("\n")

	avail := m.height - 2
	body := m.viewItems(avail)
	b.WriteString(body)
	if n := avail - lipgloss.Height(body); n > 0 {
		b.WriteString(strings.Repeat("\n", n))
	}
	b.WriteString("\n")
	if m.mode == modeSearch {
		b.WriteString(m.si.View())
	} else {
		b.WriteString(m.viewStatus())
	}
	return b.String()
}

func (m *model) viewHeader() string {
	title := fmt.Sprintf(" %s ", name)
	if m.searching {
		title = fmt.Sprintf(" %s /%s ", name, m.searchQuery)
	}
	info := fmt.Sprintf(" %d notes ", len(m.events))
	if m.pending > 0 {
		info = fmt.Sprintf(" %d new ↑ /%s", m.pending, info)
	}
	if m.loading {
		info = " loading... " + info
	}
	pad := m.width - lipgloss.Width(title) - lipgloss.Width(info)
	if pad < 0 {
		pad = 0
	}
	return headerStyle.Render(title + strings.Repeat(" ", pad) + info)
}

func (m *model) viewStatus() string {
	if m.status != "" {
		style := statusStyle
		if m.isError {
			style = errorStyle
		}
		return style.Render(truncate(m.status, m.width))
	}
	hint := "j/k:move  n:post  r:reply  f:like  o/1-9:link  i:image  enter:detail  /:search  R:reload  ?:help  q:quit"
	if m.searching {
		hint = "j/k:move  r:reply  f:like  o/1-9:link  i:image  enter:detail  /:new search  esc/q:back to timeline"
	}
	return faintStyle.Render(truncate(hint, m.width))
}

// viewItems renders events from m.offset, keeping the cursor visible by
// nudging the offset before rendering.
func (m *model) viewItems(avail int) string {
	if len(m.events) == 0 {
		if m.loading {
			return faintStyle.Render("  loading timeline...")
		}
		return faintStyle.Render("  no notes")
	}

	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor-m.offset > avail { // far jump: put the cursor at the top
		m.offset = m.cursor
	}
	for {
		if m.offset >= m.cursor {
			break
		}
		h := 0
		last := m.offset
		for i := m.offset; i < len(m.events); i++ {
			bh := lipgloss.Height(m.renderItem(i))
			if h+bh > avail && i > m.offset {
				break
			}
			h += bh
			last = i
			if h >= avail {
				break
			}
		}
		if m.cursor <= last {
			break
		}
		m.offset++
	}

	var blocks []string
	h := 0
	for i := m.offset; i < len(m.events) && h < avail; i++ {
		block := m.renderItem(i)
		bh := lipgloss.Height(block)
		if h+bh > avail {
			lines := strings.Split(block, "\n")
			block = strings.Join(lines[:avail-h], "\n")
			bh = avail - h
		}
		blocks = append(blocks, block)
		h += bh
	}
	return strings.Join(blocks, "\n")
}

func (m *model) renderItem(i int) string {
	ev := m.events[i]
	selected := i == m.cursor

	bar := "  "
	if selected {
		bar = cursorStyle.Render("▌ ")
	}

	nm := m.displayName(ev.PubKey)
	meta := shortNpub(ev.PubKey) + "  " + formatTime(ev.CreatedAt.Time())
	if rootTag(ev) != "" {
		meta = "↩ " + meta
	}
	head := nameStyle.Render(nm) + "  " + faintStyle.Render(meta)
	if info, ok := m.reactions[ev.ID]; ok && info.Count > 0 {
		like := fmt.Sprintf("♥%d", info.Count)
		if info.Mine {
			head += "  " + likedStyle.Render(like)
		} else {
			head += "  " + faintStyle.Render(like)
		}
	}

	width := max(m.width-4, 10)
	content, _ := m.linkify(strings.TrimRight(ev.Content, "\n"))
	content = wrap.String(content, width)

	var b strings.Builder
	b.WriteString(bar + truncate(head, width) + "\n")
	for _, line := range strings.Split(content, "\n") {
		b.WriteString(bar + line + "\n")
	}
	b.WriteString(strings.TrimRight(bar, " "))
	return b.String()
}

func (m *model) viewCompose() string {
	title := "New note"
	var lines []string
	if m.replyTo != nil {
		title = "Reply"
		quoted := m.displayName(m.replyTo.PubKey) + ": " + strings.ReplaceAll(m.replyTo.Content, "\n", " ")
		lines = append(lines, replyToStyle.Render(truncate("↩ "+quoted, m.width-4)))
	}

	box := composeBorder.Width(m.width - 4).Render(m.ta.View())
	hint := faintStyle.Render("ctrl+s:send  esc:cancel")

	parts := []string{m.viewHeader(), "", " " + nameStyle.Render(title)}
	parts = append(parts, lines...)
	parts = append(parts, box, " "+hint)
	return strings.Join(parts, "\n")
}

func (m *model) viewHelp() string {
	rows := [][2]string{
		{"j / ↓", "move down"},
		{"k / ↑", "move up"},
		{"g / G", "go to top / bottom"},
		{"ctrl+d/u", "move faster"},
		{"n", "compose a new note"},
		{"r", "reply to the selected note"},
		{"f / +", "like the selected note"},
		{"o / 1-9", "follow a link (nostr: in app, URL in browser)"},
		{"i", "show images of the note (sixel)"},
		{"enter / l", "open the selected note"},
		{"esc / q", "go back from a detail view / search"},
		{"/", "search notes (NIP-50 relays)"},
		{"R", "reload timeline"},
		{"?", "this help"},
		{"q", "quit"},
	}
	var b strings.Builder
	b.WriteString(m.viewHeader() + "\n\n")
	b.WriteString(" " + nameStyle.Render("Keybindings") + "\n\n")
	for _, r := range rows {
		b.WriteString("  " + helpKeyStyle.Render(r[0]) + r[1] + "\n")
	}
	b.WriteString("\n" + faintStyle.Render("  press any key to close"))
	return b.String()
}

func truncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	lines := strings.Split(wrap.String(s, max(width-1, 1)), "\n")
	return lines[0] + "…"
}
