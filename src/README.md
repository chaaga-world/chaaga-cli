# chaaga-cli — source

The Go module behind the `chaaga-cli` CLI. End-user docs (install, usage)
live one level up in [`../README.md`](../README.md); this file is for building,
testing, and releasing.

> This directory is meant to graduate into its own repository. Keep it
> self-contained — no imports from the rest of the `chaaga` monorepo.

## Layout

| File / dir       | What it is                                                        |
| ---------------- | ---------------------------------------------------------------- |
| `main.go`        | CLI entry point, arg dispatch, `version` subcommand, build vars  |
| `sync.go`        | `runSync` — flag parsing, source-of-truth prompt, the poll loop  |
| `client.go`      | HTTP client for the Chaaga app's local-network API               |
| `mirror.go`      | Directory ↔ app diffing, push/pull of file sets                  |
| `filename.go`    | Filename validation / sanitisation shared by both directions     |
| `*_test.go`      | Unit tests (run against a fake in-process API server)            |
| `apps/`          | Throwaway sample sub-apps to point `sync` at while developing    |
| `publish.sh`     | Cross-compile + cut a GitHub release                             |

The wire protocol is the Chaaga app's LAN API. Its spec currently lives in
the main repo at `app/specs/api.md` — copy the relevant parts here when this
becomes a standalone repo.

## Build

```sh
go build -o chaaga-cli .
```

## Run without building

`go run .` compiles and runs in one step — handy while iterating, and no
stray binary to clean up:

```sh
go run . sync ./apps/zombies -a 3 -h 192.168.1.23
```

## Test

```sh
go vet ./...
go test ./...
```

No device or running app required — the tests spin up a fake API server
in-process.

## Release

`./publish.sh <version> [flags]` cross-compiles for macOS / Linux / Windows,
writes a `SHA256SUMS`, and creates a GitHub release. The version is
**required** — a bare `./publish.sh` publishes nothing; it just prints the
versions already released and exits. For a throwaway local build, use
`go build` (above) instead.

```sh
./publish.sh 1.4.0                       # -> release tagged v1.4.0
./publish.sh 1.5.0-rc1 --draft --notes "…"
```

- **Tag scheme:** `v<version>`. `../install.sh` resolves the newest release
  from GitHub, so anything published here is what users get.
- **Target:** the release points at the current commit if it's already on
  `origin`, otherwise at the current branch's remote tip (with a warning) —
  an unpushed commit is rejected by GitHub's API.
- **Version stamping:** `-ldflags` sets `main.version`, `main.commit`,
  `main.buildDate`; `chaaga-cli version` prints them back.
- **Flags:** `--skip-tests`, `--draft`, `--prerelease` (implied for
  `-rc`/`-alpha`/`-beta` versions), `--notes "…"`.

Requires the [GitHub CLI](https://cli.github.com) authenticated with
`gh auth login`. Re-running with an existing tag re-uploads the assets
(`--clobber`) instead of failing.
