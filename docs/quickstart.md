# Quick start

From nothing to your first debt items in about 15 minutes. You will:

1. start the Slowpoke server,
2. create a user and a project,
3. install the agent on one machine that serves your application,
4. read the first numbers.

You need Docker on the machine that runs the server, and root access (or Docker) on the machine you want to measure.
They can be the same machine for a first try.

## 1. Start the server

```sh
git clone https://github.com/getslowpoke/slowpoke.git && cd slowpoke
echo "POSTGRES_PASSWORD=$(openssl rand -hex 16)" > .env
docker compose up -d
```

This pulls `ghcr.io/getslowpoke/server` and starts it with PostgreSQL 17. Add `--build` to build the image from
source instead (about two minutes the first time), which is also what happens when no image has been published yet.
The server listens on port 8080; set `SLOWPOKE_PORT` in `.env` to change it.

Later, `docker compose pull && docker compose up -d` is the whole update. **Settings → General → Updates** tells you
when there is one, and which of your machines are still on an older agent.

Check that it is up:

```sh
curl -s http://localhost:8080/healthz   # {"ok":true,"version":"…"}
```

> For anything beyond a first try, put the server behind HTTPS and set `SLOWPOKE_PUBLIC_URL`: see
> [Install the server](install/server.md).

## 2. Create the first user and a project

Open http://localhost:8080. The first visit asks for the **setup code**, a name, an email and a password (at least 10
characters): this user is the administrator. The code is in the server's log, and
`docker compose exec server slowpoke-server setup-code` prints it again; the one-line installer prints it for you.

Create a project for your application and pick its **time zone**: days, levels and the weekly digest follow it.

## 3. Install the agent

Open **Machines → Add agent**. The wizard asks nothing about what runs on the machine: it creates a token for this
machine only (you can revoke it later), hands you one command, and waits until the agent calls in. The machine
answers the questions by itself.

On the machine, run the one command the wizard shows, as root:

```sh
curl -fsSL http://your-slowpoke-server:8080/install.sh | sudo sh -s -- --token spa_…
```

It downloads the agent from your own server, creates a user that can read logs and write nothing, lets the agent look
at the machine and write its own `/etc/slowpoke/agent.yaml`, installs a systemd unit and starts it. Nothing else on
the machine is changed: what the agent cannot read yet is listed on the machine page, with the line that turns it on
and what it costs.

With Docker instead of systemd, or on a machine without systemd, see [Install the agent](install/agent.md#with-docker).

The machine appears in the wizard within a minute. If it does not, read `journalctl -u slowpoke-agent -f` (or
`docker logs slowpoke-agent`): the agent says exactly what it cannot reach or read.

### Make your web server log the duration

Slowpoke needs the duration of each request. nginx does not log it by default; add `rt=$request_time` at the end of
the log format:

```nginx
log_format slowpoke '$remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent '
                    '"$http_referer" "$http_user_agent" rt=$request_time';
access_log /var/log/nginx/access.log slowpoke;
```

Apache needs `%D` at the end of the `LogFormat`; Caddy's JSON access log already has it. PHP-FPM needs a slow log:

```ini
; in the pool configuration, e.g. /etc/php/8.3/fpm/pool.d/www.conf
request_slowlog_timeout = 1s
slowlog = /var/log/php-fpm/www-slow.log
```

Databases need statement statistics turned on: see [MySQL and MariaDB](install/agent.md#mysql-and-mariadb) and
[PostgreSQL](install/agent.md#postgresql).

## 4. Read the first numbers

Give it a few minutes of real traffic, then open the project:

- **Overview**: the speed index, the debt per day and what changed today.
- **Slow things**: every endpoint and query with its cost in seconds per day. Costs are spread over the time Slowpoke
  has actually observed, so a project installed ten minutes ago already shows honest per-day figures.
- **Query doctor**: open an expensive query. For queries that cost at least 30 s per day, the agent sends an `EXPLAIN`
  plan within minutes, with findings such as "full table scan on `orders`" and the index to add.
- **Missions**: after the first full window (24 hours by default), the most expensive items become missions.

## Next steps

- Record your deploys so each one gets a verdict and regressions become bosses: [Deploys](deploys.md).
- Get the file and line of every query with a framework package: [Instrument your application](instrument/README.md).
- Let your coding assistant pick up missions: [Coding assistants](assistants.md).
- Send the good news and the regressions where your team talks: [Notifications](notifications.md).
