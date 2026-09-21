package ui

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"clouds/cloud"
	"clouds/providers"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// send delivers msg through Update and recovers the concrete Model.
func send(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// runCmd executes a tea.Cmd, folding returned messages through Update until
// the chain ends or the step limit is hit.
func runCmd(m Model, cmd tea.Cmd) Model {
	for i := 0; i < 200 && cmd != nil; i++ {
		msg := cmd()
		switch msg.(type) {
		case nil, tea.BatchMsg:
			return m // runtime-driven internals end the chain here
		}
		var next tea.Cmd
		m, next = send(m, msg)
		cmd = next
	}
	return m
}

func newDemoModel(t *testing.T) Model {
	t.Helper()
	opts := cloud.Options{Demo: true, DownloadDir: t.TempDir()}
	m := NewModel(opts, providers.BuildAll(opts))
	// drive startup: resize, then run the initial fetch
	m, _ = send(m, tea.WindowSizeMsg{Width: 160, Height: 40})
	cmd := m.Init()
	return runCmd(m, cmd)
}

func TestDemoEC2Flow(t *testing.T) {
	m := newDemoModel(t)
	if m.loading {
		t.Fatal("loading should be false after fetch")
	}
	if len(m.rows) != 6 {
		t.Fatalf("expected 6 demo ec2 rows, got %d", len(m.rows))
	}
	if m.rows[0].Name != "bastion" { // default NAME sort
		t.Fatalf("first row = %s, want bastion", m.rows[0].Name)
	}
}

func TestSortKeys(t *testing.T) {
	m := newDemoModel(t)
	m, cmd := send(m, tea.WindowSizeMsg{Width: 160, Height: 40})
	m = runCmd(m, cmd)
	vs := m.top()

	m, _ = send(m, key("s")) // NAME -> ID
	if vs.sortKey != "ID" || vs.sortDesc {
		t.Fatalf("sortKey = %s desc=%v", vs.sortKey, vs.sortDesc)
	}
	m, _ = send(m, key("S"))
	if !vs.sortDesc {
		t.Fatal("S should flip descending")
	}
	m, _ = send(m, key("S"))
	if vs.sortDesc {
		t.Fatal("S should flip back to ascending")
	}
	m, _ = send(m, key("s")) // ID -> STATE -> ... through the field keys ...
	for i := 0; i < 10; i++ {
		m, _ = send(m, key("s"))
		if vs.sortKey == "NAME" {
			break // wraps around after all columns
		}
	}
	if vs.sortKey != "NAME" {
		t.Fatalf("sortKey = %s, want NAME after wrap", vs.sortKey)
	}
}

func TestCommandFilterAndDetail(t *testing.T) {
	m := newDemoModel(t)
	m, cmd := send(m, tea.WindowSizeMsg{Width: 160, Height: 40})
	m = runCmd(m, cmd)

	// :s3 -> buckets
	m, _ = send(m, key(":"))
	m, _ = send(m, key("s"))
	m, _ = send(m, key("3"))
	cmd = m.execCommand("s3")
	m = runCmd(m, cmd)
	if len(m.rows) != 3 {
		t.Fatalf("expected 3 buckets, got %d", len(m.rows))
	}

	// live filter narrows rows
	m, _ = send(m, key("/"))
	for _, r := range "logs" {
		m, _ = send(m, key(string(r)))
	}
	if vs := m.top(); vs.filter != "logs" {
		t.Fatalf("filter = %q", vs.filter)
	}
	if len(m.rows) != 1 {
		t.Fatalf("filtered rows = %d, want 1", len(m.rows))
	}
	m, _ = send(m, key("esc"))
	if len(m.rows) != 3 {
		t.Fatalf("esc should clear the filter, rows = %d", len(m.rows))
	}

	// enter on first bucket -> drill into objects
	m, cmd = send(m, key("enter"))
	m = runCmd(m, cmd)
	if len(m.rows) != 6 {
		t.Fatalf("expected 6 demo objects, got %d", len(m.rows))
	}

	// enter -> detail screen renders; esc returns
	m, cmd = send(m, key("enter"))
	if m.screen != screenDetail {
		t.Fatalf("screen = %v, want detail", m.screen)
	}
	m, _ = send(m, key("esc"))
	if m.screen != screenList {
		t.Fatalf("screen = %v, want list", m.screen)
	}
}

func TestDownloadEndToEnd(t *testing.T) {
	m := newDemoModel(t)
	m, cmd := send(m, tea.WindowSizeMsg{Width: 160, Height: 40})
	m = runCmd(m, cmd)

	// :s3, drill into demo-assets
	cmd = m.execCommand("s3")
	m = runCmd(m, cmd)
	m, cmd = send(m, key("enter"))
	m = runCmd(m, cmd)
	if len(m.rows) != 6 {
		t.Fatalf("expected 6 objects, got %d", len(m.rows))
	}

	m, _ = send(m, key("down")) // first real object
	m, cmd = send(m, key("g"))  // download
	if cmd == nil {
		t.Fatal("expected download cmd")
	}
	m = runCmd(m, cmd)

	entries, err := os.ReadDir(m.opts.DownloadDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("downloaded file should exist under %s (err=%v)", m.opts.DownloadDir, err)
	}
	if !strings.Contains(m.status, "downloaded") {
		t.Logf("status = %q", m.status)
	}
}

func TestHelpAndProviderSwitch(t *testing.T) {
	m := newDemoModel(t)
	m, cmd := send(m, tea.WindowSizeMsg{Width: 160, Height: 40})
	m = runCmd(m, cmd)

	m, _ = send(m, key("?"))
	if m.screen != screenHelp {
		t.Fatalf("screen = %v, want help", m.screen)
	}
	m, _ = send(m, key("esc"))
	if m.screen != screenList {
		t.Fatalf("screen = %v, want list", m.screen)
	}

	m, cmd = send(m, key("p")) // switch to gcp
	m = runCmd(m, cmd)
	if m.catalogs[m.provIdx].ID != "gcp" {
		t.Fatalf("provider = %s, want gcp", m.catalogs[m.provIdx].ID)
	}
	if len(m.rows) != 4 {
		t.Fatalf("expected 4 demo gce rows, got %d", len(m.rows))
	}
}

func TestFetchTimeoutRespected(t *testing.T) {
	// fetchCmd must derive from a context with a deadline; the network layer
	// honors it. (The 90s budget itself isn't waited out here.)
	f := func(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("fetch context should carry a deadline")
		}
		return []cloud.Resource{{Kind: "k", Name: "x"}}, nil
	}
	msg := fetchCmd("v", f, cloud.Options{})().(loadedMsg)
	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}
	if len(msg.res) != 1 {
		t.Fatalf("res = %d", len(msg.res))
	}
}
