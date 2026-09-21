// Command clouds is a k9s-style terminal UI for browsing AWS and GCP
// resources — a single static binary with no runtime dependencies.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"clouds/cloud"
	"clouds/providers"
	"clouds/ui"
)

func main() {
	profile := flag.String("profile", "", "AWS shared-config profile (default: AWS default chain)")
	region := flag.String("region", "", "AWS region override")
	project := flag.String("project", "", "GCP project override")
	downloadDir := flag.String("download-dir", "", "download target dir (default: ~/Downloads/clouds)")
	demo := flag.Bool("demo", false, "explore built-in sample data (no credentials needed)")
	showVersion := flag.Bool("version", false, "print version and exit")
	listServices := flag.Bool("list-services", false, "list providers/services and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("clouds %s\n", ui.Version)
		return
	}

	opts := cloud.Options{
		Profile:     *profile,
		Region:      *region,
		Project:     *project,
		DownloadDir: *downloadDir,
		Demo:        *demo,
	}

	impls := providers.BuildAll(opts)

	if *listServices {
		for _, impl := range impls {
			cat := impl.Catalog()
			fmt.Printf("\n%s  (%s)\n", cat.ID, cat.Name)
			for _, s := range cat.Services {
				alias := ""
				if s.Alias != "" && s.Alias != s.ID {
					alias = fmt.Sprintf("   alias: :%s", s.Alias)
				}
				fmt.Printf("  :%-10s %s%s\n", s.ID, s.Name, alias)
			}
		}
		return
	}

	p := tea.NewProgram(ui.NewModel(opts, impls), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "clouds:", err)
		os.Exit(1)
	}
}
