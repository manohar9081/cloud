// Package ui implements the Bubble Tea terminal UI: a k9s-style resource
// table with a command bar, live filter, sorting, drill-down, detail/help
// views, console links, clipboard and S3/GCS downloads.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"clouds/cloud"
	"clouds/providers"
)

const spinnerFrames = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"

type screen int

const (
	screenList screen = iota
	screenDetail
	screenHelp
)

type mode int

const (
	modeNormal mode = iota
	modeCommand
	modeFilter
)

type viewState struct {
	viewID      string
	title       string
	serviceName string
	fetch       cloud.FetchFunc
	resources   []cloud.Resource
	filter      string
	sortKey     string
	sortDesc    bool
}

// Msg types ---------------------------------------------------------------

type loadedMsg struct {
	viewID  string
	res     []cloud.Resource
	err     error
	elapsed time.Duration
}

type headerInfoMsg struct {
	infos []string
}

type statusMsg struct {
	msg   string
	isErr bool
}

type dlResult struct {
	path string
	err  error
}

type dlProgressMsg struct {
	text string
	next tea.Cmd
}

type dlDoneMsg struct {
	path string
	err  error
}

type spinnerTickMsg struct{}

// Model -------------------------------------------------------------------

type Model struct {
	impls    []providers.Impl
	catalogs []cloud.Provider
	provIdx  int

	stack []*viewState
	rows  []cloud.Resource

	table    table.Model
	input    textinput.Model
	viewport viewport.Model

	width, height int

	screen screen
	mode   mode
	detail cloud.Resource

	status    string
	statusErr bool
	loading   bool
	loadMsg   string
	spinIdx   int

	cloudInfo   []string
	lastService map[string]string
	opts        cloud.Options
	initCmd     tea.Cmd
}

// NewModel builds the model; providers must already be resolved.
func NewModel(opts cloud.Options, impls []providers.Impl) Model {
	m := Model{
		opts:        opts,
		impls:       impls,
		lastService: map[string]string{"aws": "ec2", "gcp": "gce"},
		status:      "starting…",
	}
	for _, impl := range impls {
		m.catalogs = append(m.catalogs, impl.Catalog())
		m.cloudInfo = append(m.cloudInfo, "")
	}
	ti := textinput.New()
	ti.CharLimit = 120
	m.input = ti
	m.viewport = viewport.New(80, 24)
	m.initCmd = m.openService("aws", m.lastService["aws"])
	return m
}

// Init implements tea.Model. State (incl. loading flag) was seeded in
// NewModel; Init only needs to kick off the first fetch.
func (m Model) Init() tea.Cmd {
	return m.initCmd
}

// helpers -----------------------------------------------------------------

func (m *Model) top() *viewState {
	if len(m.stack) == 0 {
		return nil
	}
	return m.stack[len(m.stack)-1]
}

func (m *Model) setStat(msg string, isErr bool) {
	m.status, m.statusErr = msg, isErr
}

func (m *Model) selected() (cloud.Resource, bool) {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.rows) {
		return cloud.Resource{}, false
	}
	return m.rows[i], true
}

// openService resets the stack to a top-level service view.
func (m *Model) openService(providerID, token string) tea.Cmd {
	for idx, cat := range m.catalogs {
		if cat.ID != providerID {
			continue
		}
		svc, ok := cat.Find(token)
		if !ok {
			m.setStat(fmt.Sprintf("unknown service: %s (press ? for help)", token), true)
			return nil
		}
		m.provIdx = idx
		m.lastService[providerID] = svc.ID
		m.screen = screenList
		m.mode = modeNormal
		m.stack = []*viewState{{
			viewID:      providerID + "/" + svc.ID,
			title:       providerID + "/" + svc.ID,
			serviceName: svc.Name,
			fetch:       svc.Fetch,
			sortKey:     "NAME",
		}}
		return m.startFetch()
	}
	return nil
}

func (m *Model) drillDown(r cloud.Resource) tea.Cmd {
	top := m.top()
	if top == nil {
		return nil
	}
	id := r.ID
	if id == "" {
		id = r.Name
	}
	m.stack = append(m.stack, &viewState{
		viewID:      top.viewID + "/" + id,
		title:       top.title + "/" + cloud.Trunc(r.Name, 24),
		serviceName: top.serviceName,
		fetch:       cloud.FetchFunc(r.Sub),
		sortKey:     "NAME",
	})
	return m.startFetch()
}

func (m *Model) startFetch() tea.Cmd {
	top := m.top()
	if top == nil {
		return nil
	}
	m.loading = true
	m.loadMsg = fmt.Sprintf("loading %s …", top.serviceName)
	return fetchCmd(top.viewID, top.fetch, m.opts)
}

func fetchCmd(viewID string, f cloud.FetchFunc, opts cloud.Options) tea.Cmd {
	return func() tea.Msg {
		t0 := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		res, err := f(ctx, opts)
		return loadedMsg{viewID: viewID, res: res, err: err, elapsed: time.Since(t0)}
	}
}

// Update --------------------------------------------------------------------

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.rebuildTable()
		return m, nil

	case loadedMsg:
		top := m.top()
		if top == nil || top.viewID != msg.viewID {
			return m, nil // user navigated away while loading
		}
		m.loading = false
		if msg.err != nil {
			top.resources = nil
			m.rows = nil
			m.rebuildTable()
			m.setStat(fmt.Sprintf("error: %v", msg.err), true)
			return m, nil
		}
		top.resources = msg.res
		m.rebuildTable()
		m.setStat(fmt.Sprintf("loaded %d %s in %.1fs", len(m.rows), top.serviceName, msg.elapsed.Seconds()), false)
		return m, nil

	case headerInfoMsg:
		copy(m.cloudInfo, msg.infos)
		return m, nil

	case statusMsg:
		m.setStat(msg.msg, msg.isErr)
		return m, nil

	case dlProgressMsg:
		m.setStat("⬇ "+msg.text, false)
		return m, msg.next

	case dlDoneMsg:
		if msg.err != nil {
			m.setStat(fmt.Sprintf("download failed: %v", msg.err), true)
		} else {
			m.setStat(fmt.Sprintf("✓ downloaded → %s", msg.path), false)
		}
		return m, nil

	case spinnerTickMsg:
		if m.loading {
			m.spinIdx++
			return m, spinnerCmd()
		}
		return m, nil

	case tea.MouseMsg:
		if m.mode != modeNormal {
			return m, nil
		}
		if m.screen == screenList {
			return m, m.forwardTable(msg)
		}
		return m, m.forwardViewport(msg)

	case tea.KeyMsg:
		if m.mode != modeNormal {
			return m, m.updateInput(msg)
		}
		switch m.screen {
		case screenDetail:
			return m, m.updateDetail(msg)
		case screenHelp:
			return m, m.updateHelp(msg)
		}
		return m, m.updateList(msg)
	}
	return m, nil
}

func spinnerCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

func (m *Model) forwardTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return cmd
}

func (m *Model) forwardViewport(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return cmd
}

func (m *Model) updateList(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit
	case "esc":
		return m.back()
	case "q":
		if len(m.stack) > 1 {
			return m.back()
		}
		return tea.Quit
	case ":":
		m.mode = modeCommand
		m.input.Prompt = ": "
		m.input.Placeholder = "service (e.g. ec2, s3, gce) — q quits, ? help"
		m.input.SetValue("")
		return m.input.Focus()
	case "/":
		m.mode = modeFilter
		m.input.Prompt = "/ "
		m.input.Placeholder = "filter (substring, esc to clear)"
		m.input.SetValue("")
		return m.input.Focus()
	case "?":
		m.screen = screenHelp
		m.setViewportContent(helpContent())
		return nil
	case "enter":
		r, ok := m.selected()
		if !ok {
			return nil
		}
		if r.Sub != nil {
			return m.drillDown(r)
		}
		return m.openDetail(r)
	case "d":
		r, ok := m.selected()
		if !ok {
			return nil
		}
		return consoleCmd(r)
	case "y":
		r, ok := m.selected()
		if !ok {
			return nil
		}
		return copyCmd(r.Name)
	case "g":
		r, ok := m.selected()
		if !ok {
			return nil
		}
		return m.startDownload(r)
	case "s":
		return m.actionSort()
	case "S":
		return m.actionSortReverse()
	case "r":
		return m.startFetch()
	case "p":
		next := (m.provIdx + 1) % len(m.catalogs)
		cat := m.catalogs[next]
		return m.openService(cat.ID, m.lastService[cat.ID])
	case "1":
		return m.openService("aws", m.lastService["aws"])
	case "2":
		return m.openService("gcp", m.lastService["gcp"])
	default:
		return m.forwardTable(msg)
	}
}

func (m *Model) back() tea.Cmd {
	if len(m.stack) > 1 {
		m.stack = m.stack[:len(m.stack)-1]
		m.rebuildTable()
		if top := m.top(); top != nil {
			m.setStat(fmt.Sprintf("back to %s", top.title), false)
		}
	}
	return nil
}

func (m *Model) openDetail(r cloud.Resource) tea.Cmd {
	m.detail = r
	m.screen = screenDetail
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", r.Name)
	fmt.Fprintf(&b, "%s\n", r.Kind)
	meta := strings.Join(nonEmpty("id: "+r.ID, "region: "+r.Region), "  ·  ")
	if meta != "" {
		fmt.Fprintf(&b, "%s\n", meta)
	}
	if len(r.Fields) > 0 {
		width := 0
		for _, f := range r.Fields {
			if len(f.Key) > width {
				width = len(f.Key)
			}
		}
		b.WriteString("\n")
		for _, f := range r.Fields {
			fmt.Fprintf(&b, "%-*s  %s\n", width, f.Key, orDash(f.Value))
		}
	}
	if r.Console != "" {
		fmt.Fprintf(&b, "\nconsole:  %s\n", r.Console)
	}
	if r.Detail != "" {
		fmt.Fprintf(&b, "\n%s\n", r.Detail)
	}
	m.setViewportContent(b.String())
	return nil
}

func (m *Model) updateDetail(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit
	case "esc", "q":
		m.screen = screenList
		return nil
	case "d":
		return consoleCmd(m.detail)
	case "y":
		return copyCmd(m.detail.Name)
	case "g":
		return m.startDownload(m.detail)
	default:
		return m.forwardViewport(msg)
	}
}

func (m *Model) updateHelp(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit
	case "esc", "q":
		m.screen = screenList
		return nil
	default:
		return m.forwardViewport(msg)
	}
}

func (m *Model) updateInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit
	case "esc":
		wasFilter := m.mode == modeFilter
		m.mode = modeNormal
		m.input.Blur()
		if wasFilter {
			if vs := m.top(); vs != nil {
				vs.filter = ""
			}
			m.rebuildTable()
		}
		return nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		wasFilter := m.mode == modeFilter
		m.mode = modeNormal
		m.input.Blur()
		if wasFilter {
			if vs := m.top(); vs != nil {
				vs.filter = text
			}
			m.rebuildTable()
			return nil
		}
		return m.execCommand(text)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.mode == modeFilter {
		if vs := m.top(); vs != nil && vs.filter != m.input.Value() {
			vs.filter = m.input.Value()
			m.rebuildTable()
		}
	}
	return cmd
}

func (m *Model) execCommand(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	cmd := strings.ToLower(strings.Fields(text)[0])
	switch cmd {
	case "q", "quit", "exit":
		return tea.Quit
	case "?", "help":
		m.screen = screenHelp
		m.setViewportContent(helpContent())
		return nil
	case "aws":
		return m.openService("aws", m.lastService["aws"])
	case "gcp":
		return m.openService("gcp", m.lastService["gcp"])
	}
	if _, ok := m.catalogs[m.provIdx].Find(cmd); ok {
		return m.openService(m.catalogs[m.provIdx].ID, cmd)
	}
	for i, cat := range m.catalogs {
		if i != m.provIdx {
			if _, ok := cat.Find(cmd); ok {
				return m.openService(cat.ID, cmd)
			}
		}
	}
	m.setStat(fmt.Sprintf("unknown command: %s — press ? for help", cmd), true)
	return nil
}

func (m *Model) actionSort() tea.Cmd {
	vs := m.top()
	if vs == nil {
		return nil
	}
	keys := []string{"NAME", "ID"}
	for _, r := range m.rows {
		if len(keys) >= 8 {
			break
		}
		for _, f := range r.Fields {
			if !contains(keys, f.Key) {
				keys = append(keys, f.Key)
			}
		}
	}
	if !contains(keys, vs.sortKey) {
		vs.sortKey, vs.sortDesc = "NAME", false
	}
	vs.sortKey = keys[(index(keys, vs.sortKey)+1)%len(keys)]
	vs.sortDesc = false
	m.rebuildTable()
	m.setStat(fmt.Sprintf("sorted by %s ↑", vs.sortKey), false)
	return nil
}

func (m *Model) actionSortReverse() tea.Cmd {
	vs := m.top()
	if vs == nil {
		return nil
	}
	vs.sortDesc = !vs.sortDesc
	m.rebuildTable()
	arrow := "↑"
	if vs.sortDesc {
		arrow = "↓"
	}
	m.setStat(fmt.Sprintf("sorted by %s %s", vs.sortKey, arrow), false)
	return nil
}

func (m *Model) startDownload(r cloud.Resource) tea.Cmd {
	if r.Download == nil {
		m.setStat("nothing to download here — g works on S3/GCS objects (press enter to browse)", true)
		return nil
	}
	m.setStat(fmt.Sprintf("downloading %s: %s …", r.Download.Label, cloud.Trunc(r.Name, 44)), false)
	opts := m.opts
	prog := make(chan string, 64)
	res := make(chan dlResult, 1)
	go func() {
		defer close(prog)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		p, err := r.Download.Run(ctx, opts, func(s string) { prog <- s })
		res <- dlResult{path: p, err: err}
	}()
	var listen tea.Cmd
	listen = func() tea.Msg {
		select {
		case s, ok := <-prog:
			if !ok {
				r := <-res
				return dlDoneMsg{path: r.path, err: r.err}
			}
			return dlProgressMsg{text: s, next: listen}
		case r := <-res:
			return dlDoneMsg{path: r.path, err: r.err}
		}
	}
	return listen
}

func consoleCmd(r cloud.Resource) tea.Cmd {
	return func() tea.Msg {
		if r.Console == "" {
			return statusMsg{msg: "no console link for this resource", isErr: true}
		}
		if err := openBrowser(r.Console); err != nil {
			return statusMsg{msg: fmt.Sprintf("open failed: %v", err), isErr: true}
		}
		return statusMsg{msg: fmt.Sprintf("opening console for %s …", cloud.Trunc(r.Name, 30))}
	}
}

func copyCmd(s string) tea.Cmd {
	return func() tea.Msg {
		if err := copyText(s); err != nil {
			return statusMsg{msg: fmt.Sprintf("copy failed: %v", err), isErr: true}
		}
		return statusMsg{msg: fmt.Sprintf("copied: %s", cloud.Trunc(s, 40))}
	}
}

// table ----------------------------------------------------------------

func (m *Model) rebuildTable() {
	if m.width == 0 {
		m.width = 120
	}
	vs := m.top()
	if vs == nil {
		m.rows = nil
		return
	}
	rows := cloud.FilterResources(vs.resources, vs.filter)
	rows = cloud.SortResources(rows, vs.sortKey, vs.sortDesc)
	m.rows = rows

	height := m.height - 5
	height = clamp(height, 3, 200)

	keys := unionKeys(rows, 6)
	tr := table.New(
		table.WithColumns(columnsFor(keys, m.width)),
		table.WithRows(rowsFor(rows, keys)),
		table.WithFocused(true),
		table.WithHeight(height),
	)
	tr.SetWidth(m.width)
	tr.SetStyles(tableStyles())
	m.table = tr
}

func unionKeys(rows []cloud.Resource, cap int) []string {
	keys := make([]string, 0, cap)
	for i, r := range rows {
		if i >= 20 {
			break
		}
		for _, f := range r.Fields {
			if len(keys) >= cap {
				return keys
			}
			if !contains(keys, f.Key) {
				keys = append(keys, f.Key)
			}
		}
	}
	return keys
}

func columnsFor(keys []string, total int) []table.Column {
	nameW := clamp(total*2/5, 12, 36)
	idW := clamp(total/4, 10, 30)
	rest := total - nameW - idW - len(keys)*2
	extraW := 12
	if len(keys) > 0 {
		extraW = clamp(rest/len(keys), 8, 28)
	}
	cols := []table.Column{{Title: "NAME", Width: nameW}, {Title: "ID", Width: idW}}
	for _, k := range keys {
		cols = append(cols, table.Column{Title: k, Width: extraW})
	}
	return cols
}

func rowsFor(rows []cloud.Resource, keys []string) []table.Row {
	out := make([]table.Row, 0, len(rows))
	for _, r := range rows {
		vals := make(map[string]string, len(r.Fields))
		for _, f := range r.Fields {
			vals[f.Key] = f.Value
		}
		cells := []string{cloud.Trunc(r.Name, 48), cloud.Trunc(r.ID, 32)}
		for _, k := range keys {
			cells = append(cells, cloud.Trunc(orDash(vals[k]), 28))
		}
		out = append(out, table.Row(cells))
	}
	return out
}

func tableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.BorderStyle(lipgloss.NormalBorder()).BorderBottom(true).Bold(true)
	s.Selected = s.Selected.Bold(false).Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57"))
	return s
}

// view ------------------------------------------------------------------

func (m Model) View() string {
	if m.width == 0 {
		return "starting…"
	}
	switch m.screen {
	case screenDetail:
		return m.detailView()
	case screenHelp:
		return m.helpView()
	}
	return m.listView()
}

func (m Model) listView() string {
	accent := accentFor(m.catalogs[m.provIdx].ID)
	rule := lipgloss.NewStyle().Foreground(accent).Render(strings.Repeat("─", m.width))
	return lipgloss.JoinVertical(lipgloss.Left,
		m.topbarView(accent),
		rule,
		m.table.View(),
		m.bottomView(),
	)
}

func (m Model) topbarView(accent lipgloss.Color) string {
	accentStyle := lipgloss.NewStyle().Bold(true).Foreground(accent)
	l1 := accentStyle.Render(fmt.Sprintf(" ☁ clouds v%s", Version)) + dimStyle.Render("  ")
	for i, cat := range m.catalogs {
		c := accentFor(cat.ID)
		if i == m.provIdx {
			l1 += lipgloss.NewStyle().Bold(true).Foreground(c).Render("● " + cat.ID)
		} else {
			l1 += dimStyle.Foreground(c).Render("○ " + cat.ID)
		}
		if m.cloudInfo[i] != "" {
			l1 += dimStyle.Render("  " + m.cloudInfo[i] + " ")
		}
	}
	vs := m.top()
	l2 := ""
	if vs != nil {
		l2 += accentStyle.Render(vs.title) + dimStyle.Render("  ")
		shown := len(m.rows)
		count := fmt.Sprintf("%d", shown)
		if shown != len(vs.resources) {
			count = fmt.Sprintf("%d/%d", shown, len(vs.resources))
		}
		l2 += dimStyle.Render(fmt.Sprintf("%s resources", count))
		if vs.filter != "" {
			l2 += filterStyle.Render(fmt.Sprintf("   filter: %s", vs.filter))
		}
		if vs.sortKey != "NAME" || vs.sortDesc {
			arrow := "↑"
			if vs.sortDesc {
				arrow = "↓"
			}
			l2 += sortStyle.Render(fmt.Sprintf("   sort: %s %s", vs.sortKey, arrow))
		}
	}
	return l1 + "\n" + l2
}

func (m Model) bottomView() string {
	if m.mode != modeNormal {
		return m.input.View()
	}
	msg := m.status
	style := lipgloss.NewStyle().Bold(true)
	if m.loading {
		frame := string(spinnerFrames[m.spinIdx%len(spinnerFrames)])
		msg = frame + " " + m.loadMsg
		style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	} else if m.statusErr {
		style = errStyle
	}
	hints := ":cmd /filter ⏎detail s sort g download d console y copy r refresh p provider ? help q quit"
	return style.Render(" "+msg+" ") + dimStyle.Render("│ "+hints)
}

func (m Model) detailView() string {
	accent := accentFor(m.catalogs[m.provIdx].ID)
	rule := lipgloss.NewStyle().Foreground(accent).Render(strings.Repeat("─", m.width))
	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("51")).Render(cloud.Trunc(m.detail.Name, maxInt(4, m.width-20))) +
		dimStyle.Render("  "+m.detail.Kind)
	return lipgloss.JoinVertical(lipgloss.Left, header, rule, m.viewport.View(), m.bottomView())
}

func (m Model) helpView() string {
	accent := accentFor(m.catalogs[m.provIdx].ID)
	rule := lipgloss.NewStyle().Foreground(accent).Render(strings.Repeat("─", m.width))
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf(" clouds v%s — help (esc to go back)", Version)),
		rule,
		m.viewport.View(),
		m.bottomView(),
	)
}

func (m *Model) setViewportContent(s string) {
	m.viewport.Width = m.width
	m.viewport.Height = clamp(m.height-3, 3, 200)
	m.viewport.SetContent(s)
	m.viewport.GotoTop()
}

// small helpers ---------------------------------------------------------

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func nonEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func index(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
