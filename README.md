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

## Find your app's ID and address

In Chaaga, on your phone, open the app you want to sync and tap the **Expert mode** icon in the header.
That screen shows two things you need:

- **App ID** — a small number (the app's `shortId`).
- **Address** — your phone's address on the network, like `192.168.1.23`.

## Use it

```sh
./chaaga-cli sync <folder> -a <app-id> -h <address>
```

Example:

```sh
./chaaga-cli sync ./my-app -a 3 -h 192.168.1.23
```

- `<folder>` — the folder on your computer to sync (created if it doesn't
  exist).
- `-a` — the **App ID** from the API tab.
- `-h` — the **Address** from the API tab. Just the address is fine (it uses
  port `8787`); add `:port` only if the API tab shows a different one.

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
- **One direction at a time.** `chaaga-cli` never merges the two sides. The
  side you didn't pick as source of truth just gets overwritten to match.
- **It exits if the app stops responding.** If your phone drops off Wi-Fi or
  you close Chaaga, `chaaga-cli` stops rather than waiting forever. Just run
  it again once the app is reachable.
- **Changes aren't instant.** Edits are picked up within a second or two —
  it checks on a timer. Press `R` if you don't want to wait.

---

Building or releasing `chaaga-cli` itself? See [`src/README.md`](src/README.md).
