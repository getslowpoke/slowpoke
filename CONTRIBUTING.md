# Contributing to Slowpoke

Patches are welcome, and so is a bug report that only says "this number looks wrong". Slowpoke
measures other people's production systems: the fastest way to make it better is to tell it what
it got wrong on yours.

## Before you write code

Open an issue first if the change is more than a fix — a new source of measures, a new page, a
change to how something is priced. Five lines of "here is what I want to measure and why" save an
afternoon on both sides.

## The rules the project is built on

These are not style preferences: breaking one is a bug, whatever the tests say.

- **Never slow what we measure.** The agent runs with `GOMAXPROCS=1`, a 64 MB soft limit and
  bounded maps everywhere. Under pressure it drops detail; it never grows. The monitored
  application never waits for Slowpoke.
- **Count everything exactly once.** One request seen by nginx and by OpenTelemetry is one
  request. Dedupe, never sum.
- **Per-day figures use the observed period**, never a fixed week: a project installed ten
  minutes ago must not claim "2 calls per day".
- **The agent never runs SQL chosen by the server.** `EXPLAIN` only on statements seen on that
  machine, and query values are redacted before anything leaves it.
- **`internal/protocol` is a wire contract** between agents and servers of different versions:
  add fields, never rename one or change what it means.
- **Fingerprints are a cross-language contract**: change `internal/fingerprint` only together
  with a new case in `spec/fingerprint_fixtures.json`, and say in the commit that stored hashes
  change.

## How we work

**Tests first.** Write the failing test, watch it fail, then write the code. A bug found on a real
installation gets a regression test named after the real case.

**Green on both databases.** The server runs on SQLite and PostgreSQL from the same SQL: `?`
placeholders, `ON CONFLICT ... DO UPDATE`, no engine-specific function without a branch.

Everything runs in Docker — no Go, Node or PHP toolchain needed on your machine:

```sh
./scripts/go test ./internal/agent/ -run TestName   # one package while you iterate
./scripts/go vet ./...
./scripts/test                                      # the full suite: SQLite, then PostgreSQL
./lab/up                                            # four real apps, an agent and a server
```

`lab/` is the end-to-end truth: four applications in four languages, really slow, with a real
agent watching them. If you touch the agent or ingestion, prove it there and read the numbers —
unit tests alone have missed bugs that the lab found in one morning.

## The two licences

The agent and the framework packages are MIT, the server and the panel are AGPL-3.0-or-later; see
[LICENSE-MIT](LICENSE-MIT) and [LICENSE](LICENSE). The halves share no code and a test enforces
it, so before you import a server package from the agent — or the other way round — move the
shared part into `internal/protocol` or `internal/fingerprint` instead.

Your contribution goes out under the licence of the files you touched. Keep new code on the side
of the fence where it belongs, and if you are unsure which side that is, ask in the issue.

## Sign your commits (DCO)

Slowpoke uses the [Developer Certificate of Origin](https://developercertificate.org/): a one-line
statement that you wrote the patch, or otherwise have the right to send it under these licences.
There is no CLA to sign and no copyright to assign.

Add the line by committing with `-s`:

```sh
git commit -s -m "agent: stop counting a retried request twice"
```

which appends:

```
Signed-off-by: Your Name <your.email@example.com>
```

Use your real name and an address that reaches you.

## Commits and pull requests

Write the commit message in English: what changed and why, grouped by area, in the imperative
("agent: …", "web: …"). If a change alters stored data or the meaning of a number on the panel,
say so in the message — someone will read it while wondering why their graph moved.

Keep the pull request to one subject, with the tests in it. If it fixes something you saw in
production, describe what you saw: that sentence usually becomes the name of the test.

## Style

Go: standard library first, small packages, comments that explain *why*. English in code,
comments, commits and documentation. Interface copy is written for a busy CTO in plain words, in
Italian and English (`web/src/lib/i18n.js`), and numbers always carry their unit — "12 s lost per
day", never a bare 12.
