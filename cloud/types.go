// Package cloud defines the data model shared by the providers and the TUI:
// Resource/Service/Provider, configuration options, and helpers.
package cloud

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxRows caps the rows fetched for any one view, to keep the TUI responsive.
const MaxRows = 1000

// Options carries configuration resolved once at startup.
type Options struct {
	Profile     string // AWS shared-config profile ("" -> default chain)
	Region      string // AWS region override ("" -> config default)
	Project     string // GCP project ("" -> env / metadata)
	DownloadDir string // where `g` downloads go ("" -> ~/Downloads/clouds)
	Demo        bool   // browse built-in sample data (no credentials)
}

// Field is one extra key/value column rendered next to Name and ID.
type Field struct{ Key, Value string }

// DownloadFunc fetches a resource's payload to local disk. progress receives
// short status lines ("42%  key", "[3/17] key"); it returns the saved path.
type DownloadFunc func(ctx context.Context, opts Options, progress func(string)) (string, error)

// Download marks a resource as downloadable with the `g` key.
type Download struct {
	Label string
	Run   DownloadFunc
}

// SubFunc lazily loads the resources nested under one (S3 objects, ECS
// services, log events, ...). The TUI drills down with Enter.
type SubFunc func(ctx context.Context, opts Options) ([]Resource, error)

// Resource is a single cloud object rendered as one table row.
type Resource struct {
	Kind     string // e.g. "ec2/instance"
	Name     string
	ID       string
	Region   string
	Fields   []Field
	Console  string // deep link into the cloud web console
	Detail   string // extra text shown on Enter
	Sub      SubFunc
	Download *Download
}

// Field returns the value for key, or "" if absent.
func (r Resource) Field(key string) string {
	for _, f := range r.Fields {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}

// FetchFunc loads a list of resources for a view.
type FetchFunc func(ctx context.Context, opts Options) ([]Resource, error)

// Service is one browsable resource type ("ec2", "s3", ...).
type Service struct {
	ID    string
	Name  string
	Alias string // alternate ":command" ("" -> id only)
	Fetch FetchFunc
}

// Matches reports whether token is the service's id or alias.
func (s Service) Matches(token string) bool {
	return token == s.ID || (s.Alias != "" && token == s.Alias)
}

// Provider is one cloud ("aws", "gcp") with its browsable services.
type Provider struct {
	ID       string
	Name     string
	Services []Service
}

// Find looks up a service by id or alias.
func (p Provider) Find(token string) (Service, bool) {
	for _, s := range p.Services {
		if s.Matches(token) {
			return s, true
		}
	}
	return Service{}, false
}

// FilterResources keeps rows whose name/id/region/fields contain text
// (case-insensitive). Empty text returns everything.
func FilterResources(rs []Resource, text string) []Resource {
	if text == "" {
		return rs
	}
	needle := strings.ToLower(text)
	out := make([]Resource, 0, len(rs))
	for _, r := range rs {
		var hay strings.Builder
		hay.WriteString(r.Name)
		hay.WriteByte(' ')
		hay.WriteString(r.ID)
		hay.WriteByte(' ')
		hay.WriteString(r.Region)
		for _, f := range r.Fields {
			hay.WriteByte(' ')
			hay.WriteString(f.Value)
		}
		if strings.Contains(strings.ToLower(hay.String()), needle) {
			out = append(out, r)
		}
	}
	return out
}

// SortResources sorts rows by "NAME", "ID" or a field key
// (case-insensitive, ties broken by name).
func SortResources(rs []Resource, key string, desc bool) []Resource {
	keyfn := func(r Resource) (string, string) {
		var v string
		switch key {
		case "NAME":
			v = r.Name
		case "ID":
			v = r.ID
		default:
			v = r.Field(key)
		}
		return strings.ToLower(v), strings.ToLower(r.Name)
	}
	sort.SliceStable(rs, func(i, j int) bool {
		a1, a2 := keyfn(rs[i])
		b1, b2 := keyfn(rs[j])
		if a1 != b1 {
			if desc {
				return a1 > b1
			}
			return a1 < b1
		}
		if desc {
			return a2 > b2
		}
		return a2 < b2
	})
	return rs
}

// Short returns the last path segment of fully-qualified names.
func Short(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Trunc shortens s to n characters with an ellipsis.
func Trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// HumanSize renders a byte count compactly ("512 B", "2.0 KiB").
func HumanSize(n int64) string {
	if n < 0 {
		return "-"
	}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB", "PiB"} {
		v /= 1024
		if v < 1024 || unit == "PiB" {
			return fmt.Sprintf("%.1f %s", v, unit)
		}
	}
	return "-"
}

// Ago renders a compact relative age ("3d"); zero time renders "-".
func Ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < 0 {
		return "now"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// FmtTS renders t as "2006-01-02 15:04:05" local time.
func FmtTS(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
}

// SafePath defensively joins base/bucket/key into a local download target,
// stripping ".", "..", empty segments and anything with a drive separator so
// a hostile object key can never escape the download directory.
func SafePath(base, bucket, key string) string {
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, "Downloads", "clouds")
	}
	key = strings.ReplaceAll(key, "\\", "/")
	out := filepath.Join(base, bucket)
	for _, p := range strings.Split(key, "/") {
		if p == "" || p == "." || p == ".." || strings.Contains(p, ":") {
			continue
		}
		out = filepath.Join(out, p)
	}
	return out
}

// ParallelRange runs fn(i) for i in [0,n) on `workers` goroutines.
// fn is responsible for its own error handling (best-effort batches).
func ParallelRange(n, workers int, fn func(i int)) {
	if n <= 0 {
		return
	}
	if workers < 1 {
		workers = 1
	}
	if workers > n {
		workers = n
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}
