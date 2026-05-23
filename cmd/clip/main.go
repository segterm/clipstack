package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"clipstack/internal/proto"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	danger    = lipgloss.AdaptiveColor{Light: "#D7515A", Dark: "#FF6B6B"}

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(highlight).
			Padding(0, 2)

	styleSelected = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(highlight)

	styleNormal = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#DDDDDD"})

	styleDim = lipgloss.NewStyle().
			Foreground(subtle)

	styleHelp = lipgloss.NewStyle().
			Foreground(subtle).
			MarginTop(1)

	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(highlight).
			Padding(0, 1)

	stylePreviewBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(highlight).
				Padding(0, 1)

	styleStatus = lipgloss.NewStyle().
			Italic(true).
			Foreground(special)

	styleErr = lipgloss.NewStyle().
			Foreground(danger)

	styleNote = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#666666", Dark: "#AAAAAA"}).
			Italic(true)

	styleHidden = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#888888"})

	styleGold = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFD700"))
)

// ── Client (thread-safe connection to daemon) ─────────────────────────────────

type client struct {
	mu     sync.Mutex
	conn   net.Conn
	reader *bufio.Reader
}

func (c *client) send(req proto.Request) ([]proto.Item, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	b, err := proto.Encode(req)
	if err != nil {
		return nil, err
	}
	c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.conn.Write(b); err != nil {
		return nil, err
	}
	c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp proto.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	if resp.Type == proto.MsgErr {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return resp.Items, nil
}

func (c *client) close() {
	c.conn.Close()
}

// ── Mode ──────────────────────────────────────────────────────────────────────

type mode int

const (
	modeList mode = iota
	modeSearch
	modePreview
	modeNote
)

// ── Messages ──────────────────────────────────────────────────────────────────

type itemsMsg struct{ items []proto.Item }
type errMsg struct{ err error }
type statusMsg struct{ text string }
type clearStatusMsg struct{}
type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

// ── Key normalization (Cyrillic + case) ───────────────────────────────────────

// Maps Russian QWERTY positions to Latin equivalents for the keys we use.
var cyrillicToLatin = map[rune]rune{
	'й': 'q', 'Й': 'Q',
	'о': 'j', 'О': 'J',
	'л': 'k', 'Л': 'K',
	'п': 'g', 'П': 'G',
	'м': 'v', 'М': 'V',
	'з': 'p', 'З': 'P',
	'в': 'd', 'В': 'D',
	'н': 'n', 'Н': 'N',
	'р': 'h', 'Р': 'H',
}

func normalizeKey(msg tea.KeyMsg) string {
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		if latin, ok := cyrillicToLatin[msg.Runes[0]]; ok {
			return string(latin)
		}
	}
	return msg.String()
}

// ── Model ─────────────────────────────────────────────────────────────────────

type model struct {
	cli       *client
	items     []proto.Item
	cursor    int
	mode      mode
	prevMode  mode
	search    textinput.Model
	noteInput textinput.Model
	status    string
	isErr     bool
	width     int
	height    int
	tab       int
}

func initialModel() (model, error) {
	conn, err := net.DialTimeout("unix", "/tmp/clipstack.sock", 2*time.Second)
	if err != nil {
		return model{}, fmt.Errorf("cannot connect to clipd daemon.\nStart it with: clipd &")
	}

	ti := textinput.New()
	ti.Placeholder = "Search..."
	ti.CharLimit = 256

	ni := textinput.New()
	ni.Placeholder = "Add a note..."
	ni.CharLimit = 512

	return model{
		cli:       &client{conn: conn, reader: bufio.NewReader(conn)},
		search:    ti,
		noteInput: ni,
	}, nil
}

func (m model) Init() tea.Cmd {
	return tea.Batch(fetchList(m.cli), tickCmd())
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case itemsMsg:
		m.items = msg.items
		if m.cursor >= len(m.items) {
			m.cursor = max(0, len(m.items)-1)
		}
		return m, nil

	case errMsg:
		m.status = msg.err.Error()
		m.isErr = true
		return m, nil

	case statusMsg:
		m.status = msg.text
		m.isErr = false
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearStatusMsg{} })

	case clearStatusMsg:
		m.status = ""
		m.isErr = false
		return m, nil

	case tickMsg:
		var cmd tea.Cmd
		switch m.mode {
		case modeSearch:
			cmd = fetchSearch(m.cli, m.search.Value())
		default:
			if m.tab == 1 {
				cmd = fetchPinned(m.cli)
			} else {
				cmd = fetchList(m.cli)
			}
		}
		return m, tea.Batch(cmd, tickCmd())

	case tea.KeyMsg:
		switch m.mode {
		case modeList:
			return m.updateList(msg)
		case modeSearch:
			return m.updateSearch(msg)
		case modePreview:
			return m.updatePreview(msg)
		case modeNote:
			return m.updateNote(msg)
		}
	}
	return m, nil
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := normalizeKey(msg)
	switch key {
	case "q", "Q", "ctrl+c", "esc":
		m.cli.close()
		return m, tea.Quit

	case "j", "J", "down":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}

	case "k", "K", "up":
		if m.cursor > 0 {
			m.cursor--
		}

	case "g":
		m.cursor = 0

	case "G":
		if len(m.items) > 0 {
			m.cursor = len(m.items) - 1
		}

	case "/":
		m.mode = modeSearch
		m.search.Focus()
		m.search.SetValue("")
		return m, textinput.Blink

	case "tab":
		m.tab = 1 - m.tab
		if m.tab == 1 {
			return m, fetchPinned(m.cli)
		}
		return m, fetchList(m.cli)

	case "enter", " ":
		if len(m.items) == 0 {
			return m, nil
		}
		return m, sendCopy(m.cli, m.items[m.cursor].ID)

	case "p", "P":
		if len(m.items) == 0 {
			return m, nil
		}
		item := m.items[m.cursor]
		if item.Pinned {
			return m, sendUnpin(m.cli, item.ID)
		}
		return m, sendPin(m.cli, item.ID)

	case "d", "D", "delete":
		if len(m.items) == 0 {
			return m, nil
		}
		return m, sendDelete(m.cli, m.items[m.cursor].ID)

	case "v", "V":
		if len(m.items) > 0 {
			m.mode = modePreview
		}

	case "n", "N":
		if len(m.items) > 0 {
			m.prevMode = modeList
			m.mode = modeNote
			m.noteInput.SetValue(m.items[m.cursor].Note)
			m.noteInput.CursorEnd()
			m.noteInput.Focus()
			return m, textinput.Blink
		}

	case "h", "H":
		if len(m.items) == 0 {
			return m, nil
		}
		item := m.items[m.cursor]
		if item.Hidden {
			return m, sendUnhide(m.cli, item.ID)
		}
		return m, sendHide(m.cli, item.ID)
	}
	return m, nil
}

func (m model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := normalizeKey(msg)
	switch key {
	case "esc", "ctrl+c":
		m.mode = modeList
		m.search.Blur()
		m.search.SetValue("")
		return m, fetchList(m.cli)

	case "enter":
		m.mode = modeList
		m.search.Blur()
		return m, nil

	case "j", "J", "down":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
		return m, nil

	case "k", "K", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	default:
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		return m, tea.Batch(cmd, fetchSearch(m.cli, m.search.Value()))
	}
}

func (m model) updatePreview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := normalizeKey(msg)
	switch key {
	case "esc", "q", "Q", "v", "V":
		m.mode = modeList

	case "enter", " ":
		if len(m.items) > 0 {
			return m, sendCopy(m.cli, m.items[m.cursor].ID)
		}

	case "n", "N":
		if len(m.items) > 0 {
			m.prevMode = modePreview
			m.mode = modeNote
			m.noteInput.SetValue(m.items[m.cursor].Note)
			m.noteInput.CursorEnd()
			m.noteInput.Focus()
			return m, textinput.Blink
		}
	}
	return m, nil
}

func (m model) updateNote(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.mode = m.prevMode
		m.noteInput.Blur()
		return m, nil
	case tea.KeyEnter:
		if len(m.items) == 0 {
			m.mode = m.prevMode
			m.noteInput.Blur()
			return m, nil
		}
		id := m.items[m.cursor].ID
		note := strings.TrimSpace(m.noteInput.Value())
		m.mode = m.prevMode
		m.noteInput.Blur()
		return m, sendNote(m.cli, id, note)
	default:
		var cmd tea.Cmd
		m.noteInput, cmd = m.noteInput.Update(msg)
		return m, cmd
	}
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m model) View() string {
	if m.mode == modePreview || (m.mode == modeNote && m.prevMode == modePreview) {
		return m.viewPreview()
	}
	return m.viewList()
}

func (m model) viewList() string {
	var b strings.Builder

	// Tab bar
	tab0 := "  All  "
	tab1 := "  ○ Pinned  "
	if m.tab == 0 {
		b.WriteString(styleHeader.Render(tab0))
		b.WriteString(styleDim.Render(tab1))
	} else {
		b.WriteString(styleDim.Render(tab0))
		b.WriteString(styleHeader.Render(tab1))
	}
	b.WriteString("\n\n")

	// listHeight: total height minus fixed chrome (7 lines: 2 tab + 1 sep + 1 note + 1 status + 2 help)
	listHeight := m.height - 7
	if m.mode == modeSearch {
		listHeight -= 4
		box := styleBorder.Width(m.width - 4).Render(m.search.View())
		b.WriteString(box)
		b.WriteString("\n\n")
	}
	if listHeight < 1 {
		listHeight = 1
	}

	// Item list
	if len(m.items) == 0 {
		b.WriteString(styleDim.Render("  (empty)"))
		b.WriteString("\n")
	} else {
		// numWidth: consistent column width for position numbers (e.g. 3 for 100-199 items)
		numWidth := len(fmt.Sprintf("%d", len(m.items)))
		// contentWidth: total minus "│ " prefix, number, space after number, and "○ " circle+space
		contentWidth := m.width - 2 - numWidth - 1
		start, end := visibleWindow(m.cursor, len(m.items), listHeight)
		for i := start; i < end; i++ {
			item := m.items[i]
			body, ts := formatLine(item, contentWidth)
			runes := []rune(body)
			circle := string(runes[:1])
			rest := string(runes[1:])

			num := fmt.Sprintf("%*d", numWidth, i+1)

			var barRendered, numRendered, tsRendered string
			if i == m.cursor {
				barRendered = lipgloss.NewStyle().Foreground(highlight).Render("│")
				numRendered = lipgloss.NewStyle().Foreground(highlight).Render(num)
				tsRendered = lipgloss.NewStyle().Foreground(highlight).Render(ts)
			} else {
				barRendered = styleDim.Render("│")
				numRendered = styleDim.Render(num)
				tsRendered = styleDim.Render(ts)
			}

			var circleRendered string
			if item.Pinned {
				circleRendered = styleGold.Render(circle)
			} else {
				circleRendered = styleDim.Render(circle)
			}

			var restRendered string
			if item.Hidden {
				restRendered = styleHidden.Render(rest)
			} else {
				restRendered = styleNormal.Render(rest)
			}

			b.WriteString(numRendered + " " + barRendered + " " + circleRendered + restRendered + tsRendered + "\n")
		}
	}

	// Note panel — always 1 line, never scrolls
	b.WriteString(styleDim.Render(strings.Repeat("─", m.width)) + "\n")
	if m.mode == modeNote && len(m.items) > 0 {
		b.WriteString(styleDim.Render(fmt.Sprintf("  Note %d:", m.cursor+1)) + " " + m.noteInput.View() + "\n")
	} else if len(m.items) > 0 {
		item := m.items[m.cursor]
		if item.Hidden {
			b.WriteString(styleDim.Render("  Content is hidden · press v to preview") + "\n")
		} else if item.Note != "" {
			note := item.Note
			maxW := m.width - 10
			if utf8.RuneCountInString(note) > maxW {
				runes := []rune(note)
				note = string(runes[:maxW-1]) + "…"
			}
			b.WriteString(styleDim.Render("  Note: ") + styleNote.Render(note) + "\n")
		} else {
			b.WriteString("\n")
		}
	} else {
		b.WriteString("\n")
	}

	// Status
	if m.status != "" {
		if m.isErr {
			b.WriteString(styleErr.Render(m.status))
		} else {
			b.WriteString(styleStatus.Render(m.status))
		}
	}
	b.WriteString("\n")

	// Help (contextual)
	var help string
	if m.mode == modeNote {
		help = "enter save  esc cancel"
	} else {
		help = "j/k navigate  enter copy  n note  h hide  p pin  d delete  v preview  / search  tab switch  q quit"
	}
	b.WriteString(styleHelp.Render(help))

	return b.String()
}

func (m model) viewPreview() string {
	if len(m.items) == 0 {
		return ""
	}
	item := m.items[m.cursor]

	t, _ := time.Parse(time.RFC3339, item.CreatedAt)
	header := fmt.Sprintf("Preview %d", m.cursor+1)
	if item.Hidden {
		header += " · hidden"
	}
	header += " — " + t.Local().Format("Jan 2 15:04")

	boxWidth := m.width - 4
	if boxWidth < 10 {
		boxWidth = 10
	}

	maxLines := m.height - 6
	if maxLines < 1 {
		maxLines = 1
	}

	lines := strings.Split(item.Content, "\n")
	var rendered []string
	truncated := false
	for i, l := range lines {
		if i >= maxLines {
			truncated = true
			break
		}
		if utf8.RuneCountInString(l) > boxWidth {
			runes := []rune(l)
			l = string(runes[:boxWidth-1]) + "…"
		}
		rendered = append(rendered, l)
	}
	if truncated {
		rendered = append(rendered, "… (truncated)")
	}

	content := strings.Join(rendered, "\n")
	box := stylePreviewBorder.Width(boxWidth).Render(content)

	var b strings.Builder
	b.WriteString(styleHeader.Render(header))
	b.WriteString("\n\n")
	b.WriteString(box)
	b.WriteString("\n\n")
	if m.mode == modeNote {
		b.WriteString(styleDim.Render(fmt.Sprintf("  Note %d:", m.cursor+1)) + " " + m.noteInput.View() + "\n")
		b.WriteString("\n")
		b.WriteString(styleHelp.Render("enter save  esc cancel"))
	} else {
		if item.Note != "" {
			b.WriteString(styleDim.Render("  Note: ") + styleNote.Render(item.Note) + "\n")
		} else {
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(styleHelp.Render("esc/v close  n note  enter/space copy  q quit"))
	}
	return b.String()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func formatLine(item proto.Item, width int) (body, ts string) {
	var circle string
	if item.Note != "" {
		circle = "●"
	} else {
		circle = "○"
	}
	const indicatorWidth = 2 // circle + space

	ts = formatTime(item.CreatedAt)
	tsWidth := utf8.RuneCountInString(ts) + 2

	available := width - indicatorWidth - tsWidth - 1
	if available < 1 {
		available = 1
	}

	var content string
	if item.Hidden {
		if item.Note != "" {
			content = item.Note
		} else {
			content = "•••"
		}
	} else {
		content = item.Content
		content = strings.ReplaceAll(content, "\n", "↵ ")
		content = strings.ReplaceAll(content, "\t", "→")
	}

	if utf8.RuneCountInString(content) > available {
		runes := []rune(content)
		content = string(runes[:available-1]) + "…"
	}

	pad := width - indicatorWidth - utf8.RuneCountInString(content) - tsWidth
	if pad < 1 {
		pad = 1
	}

	body = circle + " " + content + strings.Repeat(" ", pad)
	return body, ts
}

func formatTime(raw string) string {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	diff := time.Since(t)
	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	default:
		return t.Local().Format("Jan 2")
	}
}

func visibleWindow(cursor, total, height int) (start, end int) {
	if total <= height {
		return 0, total
	}
	half := height / 2
	start = cursor - half
	if start < 0 {
		start = 0
	}
	end = start + height
	if end > total {
		end = total
		start = end - height
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ── Commands ──────────────────────────────────────────────────────────────────

func fetchList(c *client) tea.Cmd {
	return func() tea.Msg {
		items, err := c.send(proto.Request{Type: proto.MsgList, Limit: 200})
		if err != nil {
			return errMsg{err}
		}
		return itemsMsg{items}
	}
}

func fetchPinned(c *client) tea.Cmd {
	return func() tea.Msg {
		items, err := c.send(proto.Request{Type: proto.MsgList, Limit: 200})
		if err != nil {
			return errMsg{err}
		}
		var pinned []proto.Item
		for _, it := range items {
			if it.Pinned {
				pinned = append(pinned, it)
			}
		}
		return itemsMsg{pinned}
	}
}

func fetchSearch(c *client, query string) tea.Cmd {
	return func() tea.Msg {
		var req proto.Request
		if query == "" {
			req = proto.Request{Type: proto.MsgList, Limit: 200}
		} else {
			req = proto.Request{Type: proto.MsgSearch, Query: query, Limit: 200}
		}
		items, err := c.send(req)
		if err != nil {
			return errMsg{err}
		}
		return itemsMsg{items}
	}
}

func sendCopy(c *client, id int64) tea.Cmd {
	return func() tea.Msg {
		if _, err := c.send(proto.Request{Type: proto.MsgCopy, ID: id}); err != nil {
			return errMsg{err}
		}
		return statusMsg{"Copied to clipboard!"}
	}
}

func sendPin(c *client, id int64) tea.Cmd {
	return func() tea.Msg {
		if _, err := c.send(proto.Request{Type: proto.MsgPin, ID: id}); err != nil {
			return errMsg{err}
		}
		items, err := c.send(proto.Request{Type: proto.MsgList, Limit: 200})
		if err != nil {
			return errMsg{err}
		}
		return itemsMsg{items}
	}
}

func sendUnpin(c *client, id int64) tea.Cmd {
	return func() tea.Msg {
		if _, err := c.send(proto.Request{Type: proto.MsgUnpin, ID: id}); err != nil {
			return errMsg{err}
		}
		items, err := c.send(proto.Request{Type: proto.MsgList, Limit: 200})
		if err != nil {
			return errMsg{err}
		}
		return itemsMsg{items}
	}
}

func sendHide(c *client, id int64) tea.Cmd {
	return func() tea.Msg {
		if _, err := c.send(proto.Request{Type: proto.MsgHide, ID: id}); err != nil {
			return errMsg{err}
		}
		items, err := c.send(proto.Request{Type: proto.MsgList, Limit: 200})
		if err != nil {
			return errMsg{err}
		}
		return itemsMsg{items}
	}
}

func sendUnhide(c *client, id int64) tea.Cmd {
	return func() tea.Msg {
		if _, err := c.send(proto.Request{Type: proto.MsgUnhide, ID: id}); err != nil {
			return errMsg{err}
		}
		items, err := c.send(proto.Request{Type: proto.MsgList, Limit: 200})
		if err != nil {
			return errMsg{err}
		}
		return itemsMsg{items}
	}
}

func sendNote(c *client, id int64, note string) tea.Cmd {
	return func() tea.Msg {
		if _, err := c.send(proto.Request{Type: proto.MsgNote, ID: id, Note: note}); err != nil {
			return errMsg{err}
		}
		items, err := c.send(proto.Request{Type: proto.MsgList, Limit: 200})
		if err != nil {
			return errMsg{err}
		}
		return itemsMsg{items}
	}
}

func sendDelete(c *client, id int64) tea.Cmd {
	return func() tea.Msg {
		if _, err := c.send(proto.Request{Type: proto.MsgDelete, ID: id}); err != nil {
			return errMsg{err}
		}
		items, err := c.send(proto.Request{Type: proto.MsgList, Limit: 200})
		if err != nil {
			return errMsg{err}
		}
		return itemsMsg{items}
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	m, err := initialModel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clip: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "clip: %v\n", err)
		os.Exit(1)
	}
}
