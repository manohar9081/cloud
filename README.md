# clouds ☁

**A k9s-style terminal UI for AWS and GCP — written in Go, shipped as a single
static binary.** Like [k9s](https://k9scli.io/) does for Kubernetes, `clouds`
gives you a fast, keyboard-driven view into your cloud accounts: jump between
services with `:commands`, filter and sort live, drill into child resources,
download S3/GCS objects, and open the web console — without leaving the
terminal.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2) and
[google.golang.org/api](https://github.com/googleapis/google-api-go-client).
**No venv, no pip, no runtime dependencies — build once, run anywhere.**

![clouds — EC2 instances view](screenshots/01-ec2-instances.png)

## Screenshots

| | |
|---|---|
| ![S3 objects drill-down](screenshots/02-s3-objects.png) | ![Downloading an object with g](screenshots/03-download.png) |
| ![Object detail view](screenshots/04-object-detail.png) | ![Help screen](screenshots/05-help.png) |
| ![GCP view — blue accent theme](screenshots/06-gcp-gce.png) | |

The accent color follows the active cloud (AWS orange, GCP blue).

## Install & run

> Prerequisite: **Go 1.22+** (one-time, only needed to *build* — the resulting
> binary runs with zero dependencies).

### ⬛ Windows

```powershell
# 1. Install Go (pick one):
winget install GoLang.Go          # or download the MSI from https://go.dev/dl

# 2. Build (from the project folder):
cd C:\Users\you\Desktop\Project\Cloud
go build -trimpath -ldflags "-s -w" -o clouds.exe .

# 3. Run:
.\clouds.exe                      # or: .\clouds.exe --demo
```

Shortcut: double-click `scripts\run.bat` — it **checks whether `clouds.exe`
exists and builds it automatically on first run**, then launches.
Use **Windows Terminal** for the best rendering.

### 🐧 Linux

```bash
# 1. Install Go (pick one):
sudo apt install golang-go        # Debian/Ubuntu
sudo dnf install golang           # Fedora/RHEL
# or: download the tarball from https://go.dev/dl

# 2. Build:
cd ~/Project/Cloud
go build -trimpath -ldflags "-s -w" -o clouds .

# 3. Run:
./clouds                          # or: ./clouds --demo
```

Shortcut: `./scripts/run.sh` — builds automatically if the binary is missing.
Clipboard (`y` key) needs `wl-copy` (Wayland) or `xclip` (X11).

### 🍎 macOS

```bash
# 1. Install Go:
brew install go

# 2. Build:
cd ~/Project/Cloud
go build -trimpath -ldflags "-s -w" -o clouds .

# 3. Run:
./clouds                          # or: ./clouds --demo
```

Shortcut: `./scripts/run.sh` (auto-builds on first run). Clipboard uses the
built-in `pbcopy`.

### Anywhere

```bash
clouds --demo            # instant tour with sample data — no credentials, no network
clouds --version
clouds --list-services   # print every provider/service shortcut
clouds --profile myprofile --region us-west-2 --project my-gcp-project
clouds --download-dir D:\s3-dl
```

The binary is fully static: copy `clouds.exe` / `clouds` to any machine (or a
server) and it just runs. Cross-compile from one machine with e.g.
`GOOS=linux GOARCH=amd64 go build -o clouds .`.

## Auth

| Cloud | Credentials | Project / region |
|-------|-------------|------------------|
| AWS | Standard credential chain: `aws configure`, `aws sso login`, env vars, or instance role | `--region` / `--profile` flags or `AWS_PROFILE` |
| GCP | Application Default Credentials: `gcloud auth application-default login` | `--project` flag or `GOOGLE_CLOUD_PROJECT` |

## Keybindings

| Key | Action |
|-----|--------|
| `:` | command mode — `:ec2`, `:s3`, `:gce`, `:gcp`, `:q`, … |
| `/` | live substring filter over name/id/fields |
| `s` / `S` | sort — `s` cycles the sort column (NAME → ID → fields…), `S` flips ascending ⇅ descending |
| `enter` | detail view, or **drill down** (S3 bucket → objects, ECS cluster → services, log group → recent events) |
| `esc` / `q` | back (`q` at top level quits) |
| `p` | toggle provider aws ⇄ gcp (`1`/`2` jump directly) |
| `r` | refresh current view |
| `d` | open selected resource in the web console |
| `g` | **download** the selected S3/GCS object to disk (see below) |
| `y` | copy selected resource name to clipboard |
| `?` | help |
| arrows / pgup / pgdn / mouse | navigate & scroll |

## Browsable services

| AWS (aws-sdk-go-v2) | GCP (google.golang.org/api) |
|-------------|---------------------|
| `:ec2` instances | `:gce` Compute Engine VMs |
| `:s3` buckets → objects (downloadable) | `:gcs` buckets → objects (downloadable) |
| `:lambda` functions | `:gke` clusters |
| `:rds` databases | `:run` Cloud Run services |
| `:logs` log groups → recent events | `:fn` Cloud Functions |
| `:ecs` clusters → services | `:bq` BigQuery datasets |
| `:eks` clusters | `:pubsub` Pub/Sub topics |
| `:sqs` queues (with counts) | `:sql` Cloud SQL instances |
| `:dynamodb` (`:ddb`) tables | `:glogs` recent log entries (6h) |

## Downloading objects

Press `g` on any **S3 object** (or **GCS object**) row to download it:

- **Single objects** stream to disk with live progress in the status bar
  (`⬇ 4.2 MiB / 12.4 MiB  logs/app.log`).
- **Folder markers** (keys ending in `/`, as created by the S3 console)
  download *recursively* — everything under that prefix, up to 200 files.
- Files land in `~/Downloads/clouds/<bucket>/<key>` by default, preserving the
  bucket's key structure; override with `--download-dir`.
- Object keys are sanitized (`../` segments stripped), so a hostile key can
  never write outside the download directory.
- Works in `--demo` mode too — it writes small sample files so you can try the
  whole flow without a cloud account.

## Demo mode

`clouds --demo` runs the entire UI against built-in sample data
(`providers/demo.go`) — EC2 instances, S3 buckets with downloadable
objects, GCE VMs, log entries, and more. No credentials, no network calls.

## Architecture

```
Cloud/
├── main.go                   flags, --list-services, launches the TUI
├── cloud/      (was internal/cloud)
│   ├── cloud/types.go        data model: Resource/Service/Provider + helpers
│   ├── providers/
│   │   ├── aws.go            aws-sdk-go-v2 services (pagination, bounded parallelism, S3 downloads)
│   │   ├── gcp.go            google.golang.org/api services (incl. GCS downloads)
│   │   └── demo.go           built-in sample data for --demo and tests
│   └── ui/
│       ├── model.go          Bubble Tea model: table, filter, command bar, drill-down stack
│       ├── styles.go         lipgloss theme (provider accents)
│       ├── clipboard.go      platform clipboard + browser helpers
│       └── help.go           help text
├── scripts/
│   ├── run.bat / run.sh      auto-build wrapper (builds the binary if missing)
│   └── build.bat / build.sh  explicit build
└── python/                   previous Python/Textual implementation (deprecated, kept for reference)
```

Design principles (mirroring k9s):

- **Everything is a `Resource` table** — services return rows; the UI is generic.
- **Drill-down is just another fetch** — `Resource.Sub` lazily loads children onto the nav stack.
- **API calls never block the UI** — every fetch runs on a goroutine; stale results are dropped.
- **Providers are pluggable** — add a service by appending a `cloud.Service` to a catalog.

Build & test:

```bash
go build ./...    # compile
go vet ./...      # static analysis
go test ./...     # unit + headless UI tests (demo data, downloads, navigation)
```

## Roadmap ideas

- SSH/SSM into EC2 and `gcloud compute ssh` from the resource view
- Log tailing (live follow) for CloudWatch / Cloud Logging
- Resource mutation actions (start/stop EC2, restart Cloud Run)
- Azure provider
- Saved views / bookmarks, YAML export

## Troubleshooting

- **AWS: "Unable to locate credentials"** → run `aws configure` or `aws sso login` first.
- **GCP: "no GCP project"** → pass `--project` or set `GOOGLE_CLOUD_PROJECT`.
- **GCP auth errors** → `gcloud auth application-default login`, then retry.
- **Rendering issues** → use Windows Terminal (or any modern terminal with 256-color support).
- **`go` not found** → install from https://go.dev/dl or via winget/apt/brew (see per-OS sections).
