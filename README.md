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
| `--action` | by type | override the tool action (`create`, `speak`, `scene`, `animate`, …) |
| `--tier` | — | image quality: `draft` · `fine` · `photo` |
| `--format` | — | aspect/size: friendly names `square` · `portrait`/`9:16` · `landscape`/`16:9` · `3:4` · `4:3` · `shorts` · `reels` (case-insensitive), or a raw preset like `landscape_16_9`. Works on the actor path too. |
| `--actor` | — | route through an actor (`act_…`) |
| `--voice` | — | voice name for `audio` speak |
| `--shot` / `--multi-prompt` | — | image `animate`: one timeline shot `"prompt@duration"` (repeatable; see below) |
| `--shot-type` | — | image `animate`: how multi-prompt shots are stitched — `customize` (default) · `intelligent` |

`generate` submits the job and polls until it finishes, then prints the output
URL.

### Multi-shot animation (`image --action animate`)

Animate an image into a continuous clip built from several timed shots (Kling).
Pass each shot as `--shot "prompt@duration"` (or the longer `--multi-prompt`);
repeat the flag for more shots. The `@duration` suffix is optional — it defaults
to 5s and may carry a trailing `s` (`@4` and `@4s` are equivalent):

```sh
framehood generate --type image --action animate --image-url frame.jpg \
  --shot "wide establishing shot@3s" \
  --shot "push in on the face@4s" \
  --shot-type customize
```

Limits (validated locally before the call): at most **6 shots**, a combined
runtime **≤ 15s**, each shot **1–15s**. `--shot`/`--multi-prompt` is mutually
exclusive with the positional prompt — use one or the other. With `--actor` and
no `--image-url`, the actor supplies the source frame.

### Other one-shot commands

The CLI mirrors the full MCP/REST surface — every command renders human-readable
output (no raw JSON dumps):

| Command | What it does |
|---------|--------------|
| `framehood whoami` · `framehood balance` | your account / credit balance |
| `framehood billing <balance\|plan\|plans\|transactions>` | credits, plan, and the credit ledger |
| `framehood billing <preview\|change> <package>` · `billing cancel [--reactivate]` | owner-only subscription changes |
| `framehood billing extra-usage [flags]` | owner-only: view or configure premium overflow top-ups (see below) |
| `framehood jobs [list]` · `framehood jobs cancel <id>` | generation history; cancel a running job |
| `framehood files <list\|upload\|delete\|publish\|unpublish\|download>` | manage your storage (`download -o <path>` writes to disk) |
| `framehood project <list\|create\|update\|delete\|assign\|use\|current>` | group generations into projects |
| `framehood team …` · `framehood team accept-invite <token>` | your organization: members, spend, roles, invites |
| `framehood keys <list\|create\|delete>` | programmatic API keys (the secret is shown once on create) |
| `framehood models [kind]` · `framehood skill <kind>` · `framehood workflows [name]` | the model catalog, a model's prompt guide, and multi-step workflows |
| `framehood library …` | search past generations and manage the trash |

Run `framehood <command> --help` for the flags on each.

### Extra usage (`billing extra-usage`)

Extra usage automatically tops up credits at a premium overflow rate when your
balance runs low, charging the card on your subscription. It is owner-only.

```sh
framehood billing extra-usage                                  # view the current config
framehood billing extra-usage --enable --amount-eur 5 --trigger 200
framehood billing extra-usage --cap-eur 50
framehood billing extra-usage --disable
```

With no flags it prints the current config (enabled, amount per top-up and its
≈credit value, trigger balance, per-cycle cap and spend, card-on-file, and the
premium-rate note). With any flag it updates only the fields you set:

| Flag | Notes |
|------|-------|
| `--enable` / `--disable` | turn Extra usage on or off |
| `--amount-eur <n>` | euros charged per top-up — at least **€5**, in **€5** steps (validated locally) |
| `--trigger <credits>` | top up when the balance drops below this many credits |
| `--cap-eur <n>` | max euros of Extra usage allowed per billing cycle |

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
