# chaaga CLI

Deploy static sites to [Chaaga](https://chaaga.com) from your terminal.

```
chaaga deploy [appname]   # deploy current directory
chaaga pull <appname>     # download deployed files
```

---

## Installation

### macOS / Linux

```sh
curl -fsSL https://raw.githubusercontent.com/chaaga-world/chaaga-cli/main/install.sh | sh
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/chaaga-world/chaaga-cli/main/install.ps1 | iex
```

### Homebrew (macOS / Linux)

```sh
brew install chaaga-world/tap/chaaga
```

### Manual

Download the binary for your platform from the [releases page](https://github.com/chaaga-world/chaaga-cli/releases) and place it anywhere on your `PATH`.

---

## Usage

### `chaaga deploy [appname]`

Upload every file in the current directory to Chaaga. If `appname` is omitted, the current directory name is used.

```
$ cd my-portfolio
$ chaaga deploy
Scanning /Users/me/my-portfolio...
  12 file(s) found
Ensuring app 'my-portfolio' exists...
  using existing app 'my-portfolio'
Requesting presigned URLs...
Uploading 12 file(s) (parallelism=8)...
  ok   index.html
  ok   styles/main.css
  ok   scripts/app.js
  ...
Finalizing...

  Deployed to https://pages.chaaga.com/u/my-portfolio
```

**Skipped automatically:**
- Files larger than 5 MB
- Dotfiles and hidden directories (`.git`, `.DS_Store`, etc.)

**Options (environment variables):**

| Variable | Default | Description |
|----------|---------|-------------|
| `CHAAGA_API` | `https://chaaga-api.fly.dev` | API base URL |
| `CHAAGA_WEB` | `https://auth.chaaga.com` | Web base URL (auth) |
| `CHAAGA_TOKEN` | `~/.config/chaaga/token` | Override token file path |
| `PARALLEL` | `8` | Number of parallel uploads |
| `DRY_RUN=1` | — | Print manifest JSON, skip upload |

**Dry run:**

```sh
DRY_RUN=1 chaaga deploy my-app
```

---

### `chaaga pull <appname>`

Download all deployed files for an app into the current directory.

```
$ mkdir my-portfolio && cd my-portfolio
$ chaaga pull my-portfolio
Listing files for 'my-portfolio'...
  12 remote file(s)
  ok   index.html
  ok   styles/main.css
  ok   scripts/app.js
  ...

  Pulled 12 file(s) to /Users/me/my-portfolio
```

**Flags:**

| Flag | Description |
|------|-------------|
| `-o, --output <dir>` | Write files to this directory instead of cwd |
| `-f, --force` | Overwrite files that already exist locally |

---

## Authentication

On the first run, `chaaga` opens your browser to complete a device-code login.

```
  Open this URL in your browser:
    https://auth.chaaga.com/device?code=ABCD-1234

  Or go to https://auth.chaaga.com/device and enter the code:
    ABCD-1234
```

The token is saved to `~/.config/chaaga/token` (mode `0600`) and reused on subsequent runs. No login is needed again until the token expires.

---

## Building from source

Requires Go 1.22+.

```sh
git clone https://github.com/chaaga-world/chaaga-cli
cd chaaga-cli
go build -o chaaga .
```

Cross-compile for all platforms:

```sh
GOOS=linux   GOARCH=amd64  go build -o chaaga-linux-amd64 .
GOOS=linux   GOARCH=arm64  go build -o chaaga-linux-arm64 .
GOOS=darwin  GOARCH=arm64  go build -o chaaga-darwin-arm64 .
GOOS=windows GOARCH=amd64  go build -o chaaga-windows-amd64.exe .
```

Release builds use [GoReleaser](https://goreleaser.com):

```sh
goreleaser release --snapshot --skip-publish
```

---

## License

MIT
