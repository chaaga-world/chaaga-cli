# chaaga-cli

Sync your Chaaga apps between your phone and your computer.

`chaaga-cli` keeps a folder on your computer in sync with a pp running
in the Chaaga app on your phone, straight over your Wi-Fi network. No cloud,
no account, no sign-in — your phone and your computer just need to be on the
same network.

Pick a direction once (phone → computer, or computer → phone) and then just
keep editing on that side; every change copies across automatically until you
stop it.

## Requirements

- Your phone and your computer on the **same Wi-Fi network**.
- The **Chaaga app open** on your phone, with the app you want to sync
  open too.

## Install

**macOS / Linux** — one command:

```sh
curl -fsSL https://raw.githubusercontent.com/chaaga-world/chaaga-cli/main/install.sh | bash
```

It picks the right build for your machine, checks it against the release's
`SHA256SUMS`, and drops `chaaga-cli` into `/usr/local/bin` (or
`~/.local/bin` if that isn't writable). Re-run it any time to update.

<details>
<summary>Do it by hand instead</summary>

1. Open the latest release on the
   [Releases page](https://github.com/chaaga-world/chaaga-cli/releases).
2. Download the file for your system:

   | Your computer            | File                            |
   | ------------------------ | ------------------------------- |
   | Mac (Apple Silicon)      | `chaaga-cli-darwin-arm64`       |
   | Mac (Intel)              | `chaaga-cli-darwin-amd64`       |
   | Linux                    | `chaaga-cli-linux-amd64`        |
   | Linux (ARM)              | `chaaga-cli-linux-arm64`        |
   | Windows                  | `chaaga-cli-windows-amd64.exe`  |

3. Rename it to `chaaga-cli` (keep the `.exe` on Windows), then
   `chmod +x chaaga-cli` and move it somewhere on your `PATH`.

On macOS the first run may be blocked ("developer cannot be verified") —
clear that once with `xattr -d com.apple.quarantine chaaga-cli`.

</details>

**Windows** — download `chaaga-cli-windows-amd64.exe` from the
[Releases page](https://github.com/chaaga-world/chaaga-cli/releases) and
rename it to `chaaga-cli.exe`.

## Connect to your phone (once)

In Chaaga, on your phone, open any app and tap the **Expert mode** icon in
the header. The API tab shows your phone's **address** on the network, like
`192.168.1.23`, and the app's **ID**, a small number.

```sh
chaaga-cli connect 192.168.1.23
```

The first time, Chaaga asks you on the phone to allow the connection;
approve it. The address is saved, so no other command needs it. If your
phone's address changes, run `connect` again.

## Use it

```sh
chaaga-cli apps                       # list the apps on your phone
chaaga-cli link ./my-app 3            # link a folder to app 3 (no files copied yet)
chaaga-cli pull ./my-app              # copy the app's files into the folder
# ...edit in your editor...
chaaga-cli status ./my-app            # see what changed on each side
chaaga-cli push ./my-app              # send your changes to the phone
```

| Command | What it does |
| --- | --- |
| `connect [<address>]` | Saves the phone's address, or shows the saved one. |
| `apps [--json]` | Lists the apps on the phone. |
| `new <folder> <name> [<emoji>]` | Creates a new app, links the folder to it and uploads the folder's files. |
| `link <folder> <app-id>` | Links a folder to an existing app. Copies no files. |
| `pull <folder>` | Copies the app's files into the folder. Local files the app doesn't have are deleted. |
| `status <folder>` | Shows what changed locally and on the phone since the last pull/push. |
| `push <folder> [--force]` | Copies the folder to the phone. Refuses if the app also changed on the phone (say, through the in-app chat) unless you add `--force`. |
| `rename <folder> <new-name> [<emoji>]` | Renames the app. |
| `sync <folder>` | Keeps syncing live in one direction until you stop it. See below. |

Quote names with spaces: `chaaga-cli new ./game "My Game" 🎮`.

A linked folder holds two small files: `.chaaga.yaml` (which app it belongs
to) and `.chaaga.state` (what each file looked like at the last sync, so
`push` can spot changes made on the phone). Neither is ever uploaded.

Exit codes: `0` ok, `1` error, `3` conflict (`status`/`push`), `4` the
folder's app was deleted from the phone. In that case run `chaaga-cli apps`
and `chaaga-cli link <folder> <app-id>` to relink.

### Live sync

```sh
chaaga-cli sync ./my-app
```

The folder must be linked first (`link` or `new`).

### Choose a direction

On startup you're asked:

```
Source of truth — [a]pp or [l]ocal folder?
```

- **`a` (app)** — copy everything **from your phone into `<folder>`**, then
  keep pulling further changes from the phone. Local files the app doesn't
  have are deleted.
- **`l` (local folder)** — copy everything **from `<folder>` to your phone**,
  then keep pushing further changes. Remote files not in your folder are
  deleted.

Whichever side you pick is the one you then edit — changes flow one way only,
so there's never a conflict to sort out.

### While it's running

- Press **`R`** (no Enter) to force an immediate full re-sync instead of
  waiting for the next check.
- Press **Ctrl+C** to stop.

## Good to know

- **The first connection can take up to 2 minutes.** The very first time a
  new computer connects, Chaaga shows an "Allow API connection?" prompt on
  your phone — approve it. After that, that computer is remembered for 24
  hours.
- **Flat files only.** A app is `index.html` plus files sitting next to
  it (CSS, JS, images). Subfolders inside `<folder>` are skipped, not synced.
- **Same network only.** No syncing over the internet — phone and computer
  must share the Wi-Fi network. There's no encryption, so use it on networks
  you trust.
- **One direction at a time.** `chaaga-cli` never merges the two sides. With
  `sync`, the side you didn't pick as source of truth gets overwritten to
  match; `push` and `pull` overwrite the other side too, but `push` first
  checks that the phone hasn't changed since your last sync.
- **A folder remembers its app.** If you delete an app on the phone and its
  number gets reused by another app, the CLI stops (exit `4`) instead of
  overwriting the wrong one.
- **It exits if the app stops responding.** If your phone drops off Wi-Fi or
  you close Chaaga, `chaaga-cli` stops rather than waiting forever. Just run
  it again once the app is reachable.
- **Changes aren't instant.** Edits are picked up within a second or two —
  it checks on a timer. Press `R` if you don't want to wait.

## Use it with Claude Code

Install the Chaaga plugin, then ask Claude to work on your app, for example
"pull Chaaga app 3 into ./my-app and add a dark mode":

```
/plugin marketplace add chaaga-world/chaaga-cli
/plugin install chaaga@chaaga
```

The plugin teaches Claude to use the commands above. It asks before
installing `chaaga-cli` or overwriting anything. Not using plugins? Copy
[`skills/chaaga-app/`](skills/chaaga-app/) into `~/.claude/skills/`.

---

Building or releasing `chaaga-cli` itself? See [`src/README.md`](src/README.md).
