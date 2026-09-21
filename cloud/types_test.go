package cloud

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShort(t *testing.T) {
	if got := Short("projects/p/locations/us/fn"); got != "fn" {
		t.Errorf("Short = %q", got)
	}
	if got := Short("plain"); got != "plain" {
		t.Errorf("Short = %q", got)
	}
}

func TestTrunc(t *testing.T) {
	if got := Trunc("abcdef", 10); got != "abcdef" {
		t.Errorf("Trunc = %q", got)
	}
	if got := Trunc("abcdef", 4); len([]rune(got)) != 4 || !strings.HasSuffix(got, "…") {
		t.Errorf("Trunc = %q", got)
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		512:             "512 B",
		2048:            "2.0 KiB",
		5 * 1024 * 1024: "5.0 MiB",
		-1:              "-",
	}
	for in, want := range cases {
		if got := HumanSize(in); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestAgo(t *testing.T) {
	if got := Ago(time.Time{}); got != "-" {
		t.Errorf("Ago(zero) = %q", got)
	}
	now := time.Now()
	if got := Ago(now.Add(-5 * time.Minute)); got != "5m" {
		t.Errorf("Ago = %q", got)
	}
	if got := Ago(now.Add(-2 * time.Hour)); got != "2h" {
		t.Errorf("Ago = %q", got)
	}
	if got := Ago(now.Add(-3*24*time.Hour - 10*time.Second)); got != "3d" {
		t.Errorf("Ago = %q", got)
	}
	if got := Ago(now.Add(time.Hour)); got != "now" {
		t.Errorf("Ago(future) = %q", got)
	}
}

func TestSafePath(t *testing.T) {
	p := SafePath("dl", "bkt", "../../etc/passwd")
	if strings.Contains(p, "..") {
		t.Errorf("SafePath traversal: %q", p)
	}
	if want := filepath.Join("dl", "bkt", "etc", "passwd"); p != want {
		t.Errorf("SafePath = %q, want %q", p, want)
	}
}

func TestFilterResources(t *testing.T) {
	rs := []Resource{
		{Kind: "k", Name: "web-1", ID: "i-1", Region: "us-east-1", Fields: []Field{{Key: "STATE", Value: "running"}}},
		{Kind: "k", Name: "db-1", ID: "i-2", Region: "eu-west-1", Fields: []Field{{Key: "STATE", Value: "stopped"}}},
	}
	if got := len(FilterResources(rs, "")); got != 2 {
		t.Errorf("empty filter = %d", got)
	}
	if got := FilterResources(rs, "web"); len(got) != 1 || got[0].Name != "web-1" {
		t.Errorf("filter web = %+v", got)
	}
	if got := FilterResources(rs, "RUNNING"); len(got) != 1 || got[0].Name != "web-1" {
		t.Errorf("filter RUNNING = %+v", got)
	}
	if got := FilterResources(rs, "zzz"); len(got) != 0 {
		t.Errorf("filter zzz = %+v", got)
	}
}

func TestSortResources(t *testing.T) {
	rs := []Resource{
		{Kind: "k", Name: "web-1", ID: "i-2", Fields: []Field{{Key: "STATE", Value: "running"}}},
		{Kind: "k", Name: "bastion", ID: "i-1", Fields: []Field{{Key: "STATE", Value: "stopped"}}},
	}
	byName := SortResources(append([]Resource(nil), rs...), "NAME", false)
	if byName[0].Name != "bastion" || byName[1].Name != "web-1" {
		t.Errorf("sort NAME = %s,%s", byName[0].Name, byName[1].Name)
	}
	byNameDesc := SortResources(append([]Resource(nil), rs...), "NAME", true)
	if byNameDesc[0].Name != "web-1" {
		t.Errorf("sort NAME desc = %s", byNameDesc[0].Name)
	}
	byField := SortResources(append([]Resource(nil), rs...), "STATE", false)
	if byField[0].Name != "web-1" { // running < stopped
		t.Errorf("sort STATE = %s", byField[0].Name)
	}
	missing := SortResources(append([]Resource(nil), rs...), "NOPE", false)
	if missing[0].Name != "bastion" {
		t.Errorf("sort missing key should fall back to name")
	}
}

func TestProviderFind(t *testing.T) {
	p := Provider{
		ID: "aws", Name: "AWS",
		Services: []Service{
			{ID: "dynamodb", Name: "DynamoDB", Alias: "ddb"},
			{ID: "ec2", Name: "EC2"},
		},
	}
	if s, ok := p.Find("ddb"); !ok || s.ID != "dynamodb" {
		t.Errorf("Find(ddb) = %+v %v", s, ok)
	}
	if s, ok := p.Find("ec2"); !ok || s.ID != "ec2" {
		t.Errorf("Find(ec2) = %+v %v", s, ok)
	}
	if _, ok := p.Find("gce"); ok {
		t.Error("Find(gce) should miss")
	}
}
