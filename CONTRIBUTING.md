# Contributing

Thanks for poking around. This is a small project; conventions are loose
but a few things will make your patches easier to merge.

## Dev setup

```sh
git clone https://github.com/hmderdoc/avatar_chat_universal
cd avatar_chat_universal
go test ./...           # should be green from a fresh clone
make build              # produces ./avatar_chat_universal + ./avatar_chat_server
```

You need Go 1.22+. Standalone mode (`./avatar_chat_universal -user
alice`) lets you test the door against the public chat server without
spinning up Synchronet — useful for everything except the BBS-drop-file
codepaths.

## What to file an issue for

- **Bugs in the door's behavior** — including specific terminal /
  client combinations that misrender. Include your client name +
  version, the relevant `avatar_chat.ini` section, and a screenshot if
  applicable.
- **Bugs in the server** — include reproducible steps, server log
  output (run with `-addr :10088` and check stderr).
- **Feature requests** — keep them small and specific. "Add a /poke
  command that..." is better than "make it more like IRC."
- **Doc gaps** — "the install guide didn't mention that ENiGMA½ wants
  args formatted as..." is gold.

## What to PR

Fixes for clearly-broken behavior, doc improvements, and small features
land easily. For larger changes, file an issue first to discuss scope.

## Code style

- Standard Go conventions: `gofmt`, `go vet`, no warnings from
  `go build ./...`. CI runs all three.
- Comments explain *why*, not *what*. The codebase is full of these —
  match the tone. If a comment is just restating what the code does,
  delete it.
- No unnecessary helpers / abstractions. If a thing is used in one
  place, inline it.
- Test coverage where it matters: protocol parsers (Zmodem, JSON-RPC,
  ANSI loader), validation rules (avatar.Validate), drop-file parsers.
  UI code is tested by running it.

## Commit messages

Imperative mood, present tense, scope prefix when it helps:

```
ansi: parse SAUCE TInfo1 as little-endian
selector: clear chrome rows before printing to kill stale text
docs: add ENiGMA½ install instructions
```

Don't mash unrelated changes into one commit. One logical fix per
commit makes blame / bisect / revert all easier.

## Architecture orientation

The fastest way to find your way around:

- `cmd/avatar_chat_universal/main.go` — entry point. Read top-to-bottom
  for a tour of every subsystem we wire up.
- `internal/ui/app.go` — the main render loop. Anything visible to the
  user passes through here.
- `internal/ansi/frame.go` — the cell-buffer + dirty-diff renderer.
  Every UI element ultimately writes into a Frame.
- `internal/chat/` — the JSON-RPC client. Read this before touching
  anything chat-protocol-related.

When in doubt, grep — file naming is consistent with the package name
and the public types.

## Releasing

Tagged pushes (`vX.Y.Z`) trigger the multi-platform release workflow
([.github/workflows/release.yml](.github/workflows/release.yml)) and
upload tarballs to the GitHub Release. Don't tag without bumping
[CHANGELOG.md](CHANGELOG.md) first.

Versioning is loose semver:
- **Major** for breaking config / data-format changes.
- **Minor** for new features.
- **Patch** for bug fixes / docs.

Pre-1.0 we're a bit looser on the major-bump rule, but if a user's
existing `avatar_chat.ini` would stop working after upgrading, that's
a major.

## Code of conduct

The BBS scene is small and sometimes cranky. Be civil; assume the
person on the other side has fewer hours in their day than you'd like.
That's it.
