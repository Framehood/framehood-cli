# Framehood CLI

Generate images, video, and audio from your terminal. The CLI is a first-class
[MCP](https://modelcontextprotocol.io) client — it speaks the same interface as
every other Framehood integration.

Two modes:

- **One-shot** — `framehood generate "…"` runs a single job and prints the
  result URL. Great for scripts and pipelines.
- **Studio** — `framehood` with no arguments opens an interactive terminal UI.

## Install

### Homebrew (macOS / Linux)

```sh
brew install framehood/tap/framehood
```

### npm

```sh
npm install -g framehood
```

### go install

```sh
go install github.com/Framehood/framehood-cli@latest
```

This installs the binary as `framehood-cli` (Go names it after the module). For
the `framehood` command, prefer Homebrew or npm above, or `mv` it after install.

### Prebuilt binaries

Download for your platform from the [latest release](https://github.com/Framehood/framehood-cli/releases/latest),
extract, and put `framehood` on your `PATH`.

### Build from source

```sh
git clone https://github.com/Framehood/framehood-cli
cd framehood-cli
go build -o framehood .
```

## Sign in

```sh
framehood login
```

Opens your browser (OAuth 2.1 + PKCE, loopback redirect). The token is stored at
`~/.framehood/credentials.json` (`0600`) and refreshed automatically. Sign out
with `framehood logout`. You'll need a [Framehood account](https://framehood.ai).

## One-shot generation

```sh
framehood generate "a red fox in the snow"
framehood generate --type audio --voice Rachel "welcome to Framehood"
framehood generate --type video "a drone shot over a coastline"
```

| Flag | Default | Notes |
|------|---------|-------|
| `--type, -t` | `image` | `image` · `video` · `audio` |
| `--out, -o` | by type | output filename |
| `--action` | by type | override the tool action (`create`, `speak`, `scene`, …) |
| `--tier` | — | image quality: `draft` · `fine` · `photo` |
| `--format` | — | size preset, e.g. `landscape_16_9`, `square` |
| `--actor` | — | route through an actor (`act_…`) |
| `--voice` | — | voice name for `audio` speak |

`generate` submits the job and polls until it finishes, then prints the output
URL.

### Other one-shot commands

The CLI mirrors the full MCP/REST surface — every command renders human-readable
output (no raw JSON dumps):

| Command | What it does |
|---------|--------------|
| `framehood whoami` · `framehood balance` | your account / credit balance |
| `framehood billing <balance\|plan\|plans\|transactions>` | credits, plan, and the credit ledger |
| `framehood billing <preview\|change> <package>` · `billing cancel [--reactivate]` | owner-only subscription changes |
| `framehood jobs [list]` · `framehood jobs cancel <id>` | generation history; cancel a running job |
| `framehood files <list\|upload\|delete\|publish\|unpublish\|download>` | manage your storage (`download -o <path>` writes to disk) |
| `framehood project <list\|create\|update\|delete\|assign\|use\|current>` | group generations into projects |
| `framehood team …` · `framehood team accept-invite <token>` | your organization: members, spend, roles, invites |
| `framehood keys <list\|create\|delete>` | programmatic API keys (the secret is shown once on create) |
| `framehood models [kind]` · `framehood skill <kind>` · `framehood workflows [name]` | the model catalog, a model's prompt guide, and multi-step workflows |
| `framehood library …` | search past generations and manage the trash |

Run `framehood <command> --help` for the flags on each.

## Studio (interactive)

```sh
framehood
```

- `⇥` / `←` `→` — switch between image / video / audio
- type a prompt, `enter` — generate (live status while it runs)
- `o` — open the result in your browser
- `esc` — quit

## Configuration

| Env var | Default | Purpose |
|---------|---------|---------|
| `FRAMEHOOD_MCP_BASE` | `https://mcp.framehood.ai` | MCP + OAuth origin |
| `FRAMEHOOD_API_BASE` | (the MCP base) | REST origin for the `/v1/…` read endpoints (`models`, `workflows`) |
| `FRAMEHOOD_CONFIG_DIR` | `~/.framehood` | credentials/state directory |

## Docs

Full documentation: <https://docs.framehood.ai>

## License

Framehood is a proprietary, source-available product. See [LICENSE](LICENSE).
