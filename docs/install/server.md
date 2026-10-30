# Install the server

The server is one static binary that serves the API, the web UI and the background workers. It stores everything in
PostgreSQL (recommended) or SQLite (for a first try or a small team).

| | Minimum | Comfortable |
|---|---|---|
| CPU | 1 vCPU | 2 vCPU |
| Memory | 256 MB | 512 MB, plus PostgreSQL |
| Disk | 1 GB | depends on retention and traffic, see [Retention](../security.md#retention) |

In our load test, a server with 2 CPUs and 512 MB in front of PostgreSQL with 2 CPUs and 1 GB handled 100 heavy agent
payloads per second (about 3,000 agents) with a p99 of 11 ms.

## One command (recommended)

```sh
curl -fsSL https://getslowpoke.com/install.sh | sh
```

Docker is the only thing that has to be there already. The installer writes `compose.yaml` and
`.env` in `/opt/slowpoke` (or `./slowpoke` when you are not root), **generates the database
password itself**, starts everything and waits until the panel answers. Options:

| | |
|---|---|
| `--dir <directory>` | where `compose.yaml` and `.env` go |
| `--port <port>` | where the panel answers, default 8080 |
| `--sqlite` | no database container: one file, to try it out |
| `--image <name>` | an image other than the public one, for anyone building their own |

Running it again keeps the password of the database that is already there - it never rewrites one -
updates the images and restarts. Read it before piping it into a shell, as you would with any
installer: it is a short shell script, and it is in the repository.

## What it sets up, by hand

```sh
git clone https://github.com/getslowpoke/slowpoke.git && cd slowpoke
echo "POSTGRES_PASSWORD=$(openssl rand -hex 16)" > .env
docker compose up -d --build
```

`compose.yaml` starts:

- `server`: the Slowpoke image, built from source, running as an unprivileged user without a shell, with a
  healthcheck;
- `postgres`: PostgreSQL 17 with its data in the `postgres-data` volume.

Variables you can put in `.env`:

| Variable | Default | Meaning |
|---|---|---|
| `POSTGRES_PASSWORD` | required | Password of the `slowpoke` database user |
| `SLOWPOKE_PORT` | `8080` | Port published on the host |
| `SLOWPOKE_PUBLIC_URL` | empty | The address people and agents use, e.g. `https://slowpoke.example.com` |
| `SLOWPOKE_AGENT_DIR` | `/usr/local/share/slowpoke` | Where the agent binaries served at `/agent/<platform>` live. The image already carries them |
| `SLOWPOKE_UPDATE_FEED` | the public release list | Asked **only** when somebody presses "Check for a new version" in Settings. Set it empty for a panel that never talks to anything |
| `SLOWPOKE_SMTP_HOST` | empty | SMTP server for team invitations. Empty: the Team page gives you the invitation link to send yourself |
| `SLOWPOKE_SMTP_PORT` | `587` | `587` uses STARTTLS when offered, `465` is TLS from the start |
| `SLOWPOKE_SMTP_USERNAME` / `SLOWPOKE_SMTP_PASSWORD` | empty | SMTP login, sent only over TLS |
| `SLOWPOKE_SMTP_FROM` | empty | Sender, e.g. `Slowpoke <slowpoke@example.com>`. Required together with the host |

The easiest way is the panel: **Team → Email** has the settings of the common providers (Brevo, Cloudflare,
Google Workspace, Amazon SES, Resend, Postmark, Mailgun), keeps the password masked and sends a test email to you.
The variables above, when set, win over the panel.

> Send from a subdomain verified only for Slowpoke (for example `slowpoke@slowpoke.yourcompany.com`): the SPF,
> DKIM and DMARC records of your company mail stay exactly as they are, and nothing Slowpoke sends can hurt
> the reputation of the main domain.

Any SMTP provider works. With [Cloudflare Email Service](https://developers.cloudflare.com/email-service/api/send-emails/smtp/):
onboard your domain under Email Service > Email Sending, create an API token with the "Email Sending: Edit"
permission, then set `SLOWPOKE_SMTP_HOST=smtp.mx.cloudflare.net`, `SLOWPOKE_SMTP_PORT=465`,
`SLOWPOKE_SMTP_USERNAME=api_token`, the token as `SLOWPOKE_SMTP_PASSWORD`, and a sender on the onboarded domain.

Migrations run automatically at every start.

## Docker with SQLite

Good for a first try or a single small project:

```sh
docker build --target server -t slowpoke/server .
docker run -d --name slowpoke --restart unless-stopped \
  -p 8080:8080 -v slowpoke-data:/data \
  slowpoke/server
```

The database is `/data/slowpoke.db` inside the `slowpoke-data` volume. SQLite runs in WAL mode with a single
connection: move to PostgreSQL when several teams or many machines report to the same server.

## Single binary

The toolchain runs in Docker, so nothing needs to be installed besides Docker on the build machine:

```sh
docker run --rm -v "$(pwd)":/src -w /src/web node:22-alpine sh -c "npm ci && npm run build"
./scripts/go build -o bin/slowpoke-server ./cmd/slowpoke-server
```

Copy `bin/slowpoke-server` to the server (it is statically linked, Linux amd64 by default; set `GOARCH=arm64` for ARM)
and run it with systemd:

```ini
# /etc/systemd/system/slowpoke-server.service
[Unit]
Description=Slowpoke server
After=network-online.target postgresql.service

[Service]
User=slowpoke
Environment=SLOWPOKE_DATABASE_URL=postgres://slowpoke:CHANGE_ME@127.0.0.1:5432/slowpoke?sslmode=disable
Environment=SLOWPOKE_PUBLIC_URL=https://slowpoke.example.com
Environment=SLOWPOKE_LISTEN=127.0.0.1:8080
ExecStart=/usr/local/bin/slowpoke-server serve
Restart=on-failure
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

With SQLite, add `StateDirectory=slowpoke` and use `SLOWPOKE_DATABASE_URL=sqlite:///var/lib/slowpoke/slowpoke.db`.

## First user

The first visit to the UI asks for the **setup code**, then a name, an email and a password of at least 10 characters.
Setup is only possible while no user exists. The code is what keeps the panel from belonging to whoever reaches it
first: the installer prints it at the end (with a link that fills it in), the server logs it at every start while
nobody has set it up (`"setup_code"`), and this prints it again:

```sh
docker compose exec server slowpoke-server setup-code   # or just: slowpoke-server setup-code
```

It is used once and forgotten. Ten wrong codes from one address block it for 15 minutes, like wrong passwords.

From the command line instead, with no code needed (whoever runs it already controls the server):

```sh
docker compose exec server slowpoke-server create-user -email you@example.com -name "Your Name" -password '…'
```

Every user can see and manage every project: there are no per-user roles yet, so only create accounts for people you
trust with all projects on this server.

## Forgotten password

Slowpoke sends no email, so there is no reset link: the way back in is the machine itself. It asks for the new
password twice, shows nothing while you type, and closes every session that account has open — including whoever was
still logged in with the old one.

```sh
docker compose exec server slowpoke-server set-password -email you@example.com   # or, installed by hand:
slowpoke-server set-password -email you@example.com
```

In a script, pass it in the environment (`SLOWPOKE_PASSWORD='…' slowpoke-server set-password -email …`) rather than
with `-password`, which leaves the password in the shell history and in `ps`.

## HTTPS and reverse proxy

Agents refuse to send their token over plain `http://` to a public address, and browsers deserve HTTPS too. Put the
server behind your usual reverse proxy and set `SLOWPOKE_PUBLIC_URL` to the public HTTPS address: session cookies
become `Secure` and links in notifications, issues and MCP answers use that address.

Caddy:

```
slowpoke.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

nginx:

```nginx
server {
    listen 443 ssl http2;
    server_name slowpoke.example.com;
    # ssl_certificate ... ; ssl_certificate_key ... ;

    client_max_body_size 20m;   # agent payloads are gzip-compressed, 20 MB is the server's own limit

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Tell the server which proxy it can believe about the visitor's address, so the login rate limit (10 failed attempts
per 15 minutes per address) applies to each visitor and not to the proxy:

```sh
SLOWPOKE_TRUSTED_PROXIES=127.0.0.1,::1
```

nginx must pass `X-Forwarded-For` (`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`). Behind Cloudflare,
also let nginx restore the visitor's address from Cloudflare's ranges before it forwards the request, so a direct
request to your server cannot pretend to come from someone else:

```nginx
# http block; the full list is at https://www.cloudflare.com/ips/
set_real_ip_from 173.245.48.0/20;
set_real_ip_from 103.21.244.0/22;
# ... every Cloudflare IPv4 and IPv6 range ...
real_ip_header CF-Connecting-IP;
```

## Scaling out

One process runs everything by default. For more machines or a busier team, split roles with `SLOWPOKE_ROLE`:

| Role | Runs |
|---|---|
| `all` (default) | HTTP API and UI, ingest workers, game engine (every minute), notifications (every 30 s), retention (every hour) |
| `web` | HTTP only: several instances behind a load balancer |
| `worker` | Background work only, no HTTP |

All instances share the same PostgreSQL. Agent payloads go to a durable queue in the database and are processed by
the workers (`SLOWPOKE_WORKERS`, default 4); when the queue holds more than `SLOWPOKE_MAX_QUEUE` batches (default
20,000) the server answers `429` and agents retry later, so nothing grows without bounds. Game and notification jobs
take a database lock, so running several workers is safe.

See [Server configuration](../reference/server.md) for every variable.

## Backups

Everything lives in the database:

```sh
docker compose exec postgres pg_dump -U slowpoke -Fc slowpoke > slowpoke-$(date +%F).dump
```

With SQLite, copy the database with `sqlite3 slowpoke.db ".backup slowpoke-backup.db"` or stop the server before
copying the file.

## Upgrades

```sh
git pull
docker compose up -d --build
```

Migrations run at start. Agents and servers of different versions talk to each other: the agent protocol only ever
gains fields, so you can upgrade the server first and the agents at your own pace.

## Try it with demo data

```sh
docker compose exec server slowpoke-server seed-demo -days 30 -name "Demo Shop"
```

creates a project with 30 days of realistic data: debt items, missions, bosses, deploys and badges.

## Releasing a new version (maintainers)

A tag is a release. `git tag -a v0.2.0 -m "what changed" && git push origin v0.2.0` starts the
workflow in `.github/workflows/release.yml`, which builds the binaries for `linux/amd64` and
`linux/arm64` (server and agent), attaches them to the GitHub release with their `SHA256SUMS`,
and pushes multi-platform images to `ghcr.io/<owner>/server` and `ghcr.io/<owner>/agent`. The
version baked into both is the tag without its `v`, which is what the panel compares with the
release feed.

## Updates

**Settings → General → Updates** shows which version this server is and which machines run an
older agent. The check for a newer Slowpoke is a button, never a background task: pressing it
makes one request to `SLOWPOKE_UPDATE_FEED` and sends nothing about you or your machines. The
panel then shows the command that applies to this installation — `docker compose pull && docker
compose up -d` when it runs in a container, `./scripts/deploy <host>` when it runs as a binary —
and never replaces itself: a self-hosted panel does not download a binary from the internet and
put it in place of itself while you are looking at it.

The machines are a different story, because they do not need the internet at all: this server
hands out the agent it was built with, so re-running the install command upgrades a machine, and
an agent with `auto_update: true` follows this server by itself. See
[Install the agent](agent.md#keeping-a-fleet-aligned).
