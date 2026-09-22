# Install the agent

The agent runs on every machine you want to measure: web servers, database servers, workers. It reads what is already
there (access logs, the PHP-FPM slow log, database statistics, `/proc`), aggregates it per minute and sends it to the
server over HTTPS.

It is built not to be noticed:

- at most one CPU core (`GOMAXPROCS=1`) and a 64 MB soft memory limit by default;
- bounded maps and buffers everywhere: under pressure it drops detail, it never grows;
- read-only on files and statistics. The only SQL it runs besides statistics is `EXPLAIN` on statements it saw on that
  machine, inside a read-only transaction with a 10 s timeout;
- up to 240 payloads kept in memory while the server is unreachable, then the oldest are dropped.

## A whole fleet

One agent sees its neighbours: it reads the live connections of its own machine, so the panel
knows about machines nobody has ever installed anything on. From **your own computer**, with your
ssh keys and your `~/.ssh/config`:

```sh
curl -fsSL https://slowpoke.example.com/fleet.sh | sh -s -- \
  --token spu_… --project my-project --dry-run
```

It asks the panel which machines are still dark, tries to reach each one over ssh, skips the ones
that already have an agent, creates a token per machine and runs the one-line install below. The
`--token` is a **personal** token (Settings → Access): the panel never gets access to anything,
everything happens from your machine with your keys. `--dry-run` shows the list and stops,
`--auto-update` leaves those agents following the version of the server.

Discovery sees neighbours, not everything: a machine nobody talks to cannot be found this way.
Add those by name with `--only host1,host2`.

## One command

On a Linux machine with systemd, the whole installation is one line. Create a token for the machine in the UI
(**Machines → Add agent**) or with `slowpoke-server create-token -project <slug> -name <machine>`, then:

```sh
curl -fsSL https://slowpoke.example.com/install.sh | sudo sh -s -- --token spa_…
```

The script downloads the agent **from your own Slowpoke server** (no registry, nothing from the internet), checks its
checksum, creates a `slowpoke` system user that can read logs and write nothing, lets the agent look at the machine
and write its own `/etc/slowpoke/agent.yaml`, installs a systemd unit and starts it. It prints what it found and what
is still off, with the line that turns each thing on.

```sh
--name api-1                  how this machine is called in the panel (default: its hostname)
--env SLOWPOKE_DB_PASSWORD=…  a password the configuration refers to (repeatable)
--auto-update                 let this machine follow the version of the server by itself
--server URL                  a different server from the one that served the script
--force                       write a new configuration over the one already there
--uninstall                   stop the agent and remove every file it owns
```

Every option also takes the `--name=api-1` form. Passwords given with `--env` are written to
`/etc/slowpoke/agent.env`, readable only by the agent, and the unit reads it: the configuration keeps referring to
`${SLOWPOKE_DB_PASSWORD}` and no password is ever in it. Giving `--env` again changes that one value and leaves the
others alone, so adding a database password later is one more line:

```sh
curl -fsSL https://slowpoke.example.com/install.sh | sudo sh -s -- --token spa_… --env SLOWPOKE_DB_PASSWORD=…
```

Running it again upgrades the agent to the version of your server and keeps your configuration.

### Keeping a fleet aligned

Every answer the server gives an agent carries the server's version and, when the server has it,
where the agent for that machine can be downloaded. What happens next is your choice:

- by default the agent writes one line in its log (`a newer agent is waiting on the server`) and
  the **Machines** page marks it "to update". You update it by running the install command again;
- with `auto_update: true` in `agent.yaml` (or `--auto-update` at install time) the agent
  downloads that binary from **your own server**, checks its checksum, runs it once to make sure
  it works and says the expected version, puts it in place and stops: systemd starts the new one.
  Nothing is downloaded from the internet and nothing but the agent binary is touched. Asking for
  it changes where the agent lives: the binary goes to `/usr/local/lib/slowpoke/slowpoke-agent`,
  owned by the `slowpoke` user, `/usr/local/bin/slowpoke-agent` becomes a link to it, and that one
  directory is the only thing the unit opens for writing (`ReadWritePaths`). Everything else stays
  read-only for the agent. Installing again without `--auto-update` puts it back as it was;
- inside a container the agent never replaces itself, because the image is the truth: there you
  update the image. It says so in the log instead.

A server installed as a binary hands out the agent only if the binaries are next to it
(`/usr/local/share/slowpoke`, or `SLOWPOKE_AGENT_DIR`); `./scripts/deploy` puts them there. The
Docker image already carries them.

The server hands out the agent for `linux-amd64` and `linux-arm64`. The Docker image carries both; for a server
installed as a binary, put them in `/usr/local/share/slowpoke` (or set `SLOWPOKE_AGENT_DIR`), otherwise the download
answers with the reason.

## 1. Get the configuration

### Let the agent look at the machine

`slowpoke-agent detect` reads the machine and tells you what Slowpoke can measure there: nginx sites, PHP-FPM pools,
databases and applications with their framework (Laravel, Symfony, WordPress, Django, FastAPI, Flask, Next.js,
NestJS, Express, Go, Rails). For everything that is missing it prints the exact change: a log format with the request
duration, the PHP-FPM slow log, the package to install. It only reads files: nothing is changed.

```sh
sudo slowpoke-agent detect
sudo slowpoke-agent detect -server https://slowpoke.example.com -token spa_… -name api-1 -write /etc/slowpoke/agent.yaml
```

The generated configuration already points to the logs the report asks you to add, so the agent picks them up as soon
as they exist. WebSocket and streaming locations found in nginx are added to `ignore_paths`. A token for the machine
can be created in the UI or with `slowpoke-server create-token -project <slug> -name <machine>`.

### Or use the wizard

In the UI, open **Machines → Add agent**. The wizard creates a token for this machine and writes the configuration.
Save it as `/etc/slowpoke/agent.yaml`, readable only by root (the agent warns at start if everyone can read it):

```sh
sudo mkdir -p /etc/slowpoke
sudo nano /etc/slowpoke/agent.yaml
sudo chmod 640 /etc/slowpoke/agent.yaml
```

A typical web server running Laravel on PHP-FPM with a local MySQL:

```yaml
server: https://slowpoke.example.com
token: ${SLOWPOKE_TOKEN}          # environment variables are expanded
host: web-1
sources:
  - type: host
  - type: nginx
    name: app
    path: /var/log/nginx/access.log
  - type: php-fpm
    name: www
    path: /var/log/php-fpm/www-slow.log
  - type: mysql-slowlog
    path: /var/log/mysql/mysql-slow.log
  - type: mysql
    dsn: "slowpoke:${SLOWPOKE_DB_PASSWORD}@tcp(127.0.0.1:3306)/"
```

Every option is in the [agent configuration reference](../reference/agent.md).

## 2. Run it

### With Docker

Build the image from source (published images are not available yet):

```sh
git clone https://github.com/getslowpoke/slowpoke.git && cd slowpoke
docker build --target agent -t slowpoke/agent .
```

Run it with read-only access to what it measures:

```sh
docker run -d --name slowpoke-agent --restart unless-stopped \
  --pid host --network host \
  -v /proc:/host/proc:ro -e SLOWPOKE_HOST_ROOT=/host \
  -v /var/log:/var/log:ro \
  -v /etc/slowpoke:/etc/slowpoke:ro \
  -v slowpoke-agent:/var/lib/slowpoke \
  slowpoke/agent
```

- `--network host` lets the agent reach databases on `127.0.0.1` and see the machine's connections for the
  infrastructure map. `--pid host` and `/proc` give it the machine's CPU, memory and processes instead of the
  container's.
- `/var/lib/slowpoke` keeps the read position of each log across restarts.
- Mount the directories of any log that is not under `/var/log`.

The agent runs as root inside the container because log files are usually owned by root or `adm`; every mount is
read-only except its own state volume.

### Without Docker (systemd), by hand

This is what `install.sh` above does for you. Build the binary (the toolchain runs in Docker):

```sh
./scripts/go build -o bin/slowpoke-agent ./cmd/slowpoke-agent          # Linux amd64
GOARCH=arm64 ./scripts/go build -o bin/slowpoke-agent ./cmd/slowpoke-agent   # ARM
```

Install it with a dedicated user that can read logs through the `adm` group:

```sh
sudo install -m 755 bin/slowpoke-agent /usr/local/bin/slowpoke-agent
sudo useradd --system --no-create-home --shell /usr/sbin/nologin --groups adm slowpoke
sudo chgrp slowpoke /etc/slowpoke/agent.yaml
```

```ini
# /etc/systemd/system/slowpoke-agent.service
[Unit]
Description=Slowpoke agent
After=network-online.target

[Service]
User=slowpoke
SupplementaryGroups=adm
# Read any log (PHP-FPM writes its slow log as root with mode 0600) without being able to write anything.
AmbientCapabilities=CAP_DAC_READ_SEARCH
CapabilityBoundingSet=CAP_DAC_READ_SEARCH
ExecStart=/usr/local/bin/slowpoke-agent -config /etc/slowpoke/agent.yaml
StateDirectory=slowpoke
Restart=always
RestartSec=5
Nice=10
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
MemoryMax=128M

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now slowpoke-agent
journalctl -u slowpoke-agent -f
```

Make sure the `slowpoke` user can read each log file you configure (`sudo -u slowpoke head -1 /var/log/nginx/access.log`).

### Optional: name the process behind each connection

The infrastructure map draws the connections it reads from `/proc/net/tcp`. Without extra rights they belong to the
machine as a whole; with `CAP_SYS_PTRACE` the agent can also follow `/proc/<pid>/fd` for processes it does not own, so
each connection is attributed to the service that opened it: `php-fpm: api`, `nginx`, a queue worker, the backup agent.

```ini
AmbientCapabilities=CAP_DAC_READ_SEARCH CAP_SYS_PTRACE
CapabilityBoundingSet=CAP_DAC_READ_SEARCH CAP_SYS_PTRACE
```

`CAP_SYS_PTRACE` is what `ss -p` and `netstat -p` need, and it also allows reading the memory of other processes: grant
it only where that is acceptable. The agent reads `fd`, `comm` and `cmdline` and nothing else, with a hard budget
(20,000 links, 300 ms) so a busy machine is never scanned twice. Drop the capability and restart to turn it off; the map
keeps working, without process names. In Docker: `--cap-add SYS_PTRACE --pid host`.

## 3. Prepare what it reads

### Web servers

The agent can make this change itself, which is the safe way to do it on the only production
server a small team has:

```sh
sudo slowpoke-agent enable-duration --dry-run   # exactly what it would write, and where
sudo slowpoke-agent enable-duration             # writes it, runs nginx -t, reloads
sudo slowpoke-agent enable-duration --undo      # back the way it was
```

It defines the log format once in `/etc/nginx/conf.d/slowpoke.conf`, adds two lines to the server
block of each site that has no duration yet — the first one repeats the log that site already
writes, because an `access_log` inside a server block replaces the inherited one — keeps the
previous files **outside** the directories nginx reads (a copy of a server block left in
`sites-enabled` is a second server block), and asks every site on the machine what it answers
before and after the reload. If nginx refuses the configuration, or a site answers differently
than it did a second earlier, everything goes back and the command says so.

When the application behind the web server runs on **another machine** - an ingress, a load
balancer, containers elsewhere - no agent sees both sides, so the agent cannot pair the access log
line with the trace of the same request and both would be counted. The host is what links them:
the framework packages send the one they answered for, and a web source says its own with `site:`
when the name of the source is not the host already:

```yaml
sources:
  - type: nginx
    name: web            # a name, not a host
    site: shop.example.com
    path: /var/log/nginx/shop.access.log
```

The web server's line wins, because it is the whole wait: queueing for a free worker, reading the
request, writing the response out. What only the application knows - route templates, the queries
of each request, the file and the line - keeps coming from the package.

The same change by hand:

The agent needs the duration of every request.

- **nginx**: add `rt=$request_time` at the end of your log format (a trailing `request_time=` or a trailing number of
  seconds in quotes also work), or use JSON logs.

  The least invasive way is a second log only for Slowpoke, so existing logs and the tools reading them do not
  change. Define the format once, e.g. in `/etc/nginx/conf.d/slowpoke.conf`:

  ```nginx
  log_format slowpoke '$remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent '
                      '"$http_referer" "$http_user_agent" rt=$request_time';
  ```

  and add the log to the site's `server` block. If the site had no `access_log` of its own, repeat the inherited one:
  in nginx an `access_log` in a `server` block **replaces** the one from the `http` block for that site.

  ```nginx
  access_log /var/log/nginx/access.log;                          # the log you already had
  access_log /var/log/nginx/shop.example.com.slowpoke.log slowpoke;
  ```

  `slowpoke-agent detect` prints these lines for each site.

- **Apache**: end the `LogFormat` with `%D` (microseconds) and use `type: apache`.

  ```apache
  LogFormat "%h %l %u %t \"%r\" %>s %b \"%{Referer}i\" \"%{User-Agent}i\" %D" slowpoke
  CustomLog ${APACHE_LOG_DIR}/access.log slowpoke
  ```

- **Caddy**: the JSON access log already has the duration; use `type: caddy`.

WebSocket upgrades (status 101) are not counted as requests. List long-by-design paths such as event streams in
`ignore_paths`.

### PHP-FPM

Turn on the slow log in each pool. Every request slower than the timeout writes its PHP stack, which becomes a
"stall" pointing at the application file and line:

```ini
request_slowlog_timeout = 1s
slowlog = /var/log/php-fpm/www-slow.log
```

In Docker, PHP-FPM needs `cap_add: [SYS_PTRACE]` to write the slow log.

### MySQL and MariaDB

Create a user for the agent:

```sql
CREATE USER 'slowpoke'@'127.0.0.1' IDENTIFIED BY 'a-long-password';
GRANT PROCESS, SELECT ON *.* TO 'slowpoke'@'127.0.0.1';
```

`SELECT` is what `EXPLAIN` and `performance_schema` need; the agent never writes.

Turn on the statistics:

```ini
[mysqld]
performance_schema = ON          # default ON in MySQL 8, OFF in MariaDB
slow_query_log = ON              # optional, but gives real statements with their values for EXPLAIN
slow_query_log_file = /var/log/mysql/mysql-slow.log
long_query_time = 1
```

- **`mysql`** reads `SHOW GLOBAL STATUS` and the top 1,000 statements of
  `performance_schema.events_statements_summary_by_digest`, and runs `EXPLAIN` on request.
- **`mysql-slowlog`** reads the slow log. When both are configured, slow totals come from the slow log only, so nothing
  is counted twice.

### Redis

Nothing to create: the agent reads `INFO` and the slow command log as any client does. If `requirepass`
is set, give the agent the password in `agent.yaml`. To record slow commands (off means nothing is
logged):

```sh
redis-cli CONFIG SET slowlog-log-slower-than 10000   # 10 ms
redis-cli CONFIG REWRITE                             # keep it after a restart
```

### PostgreSQL

Load `pg_stat_statements` (needs a restart) and create the extension in the database the agent connects to:

```ini
# postgresql.conf
shared_preload_libraries = 'pg_stat_statements'
```

```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
CREATE USER slowpoke WITH PASSWORD 'a-long-password';
GRANT pg_read_all_stats TO slowpoke;   -- statistics of every database
GRANT pg_read_all_data TO slowpoke;    -- PostgreSQL 14+: lets EXPLAIN see your tables (read-only)
```

On PostgreSQL 13 and older, grant `SELECT` on the application's tables instead of `pg_read_all_data`.

`pg_stat_statements` keeps statements with `$1` placeholders instead of values. On **PostgreSQL 16 and newer** the
agent explains them with `EXPLAIN (GENERIC_PLAN)`, a plan built without values; row counts are then the planner's
estimates, and the query doctor says so. On older versions those statements get no plan.

`EXPLAIN` runs in the database named in the DSN: add one `postgres` source per database if the application uses
several.

## 4. Check it

The machine appears under **Machines** within a minute, with its sources. If not, read the log
(`docker logs slowpoke-agent` or `journalctl -u slowpoke-agent`). Common causes:

| Message | Fix |
|---|---|
| `server "http://…" uses plain http: the token would travel in clear` | Use the server's HTTPS address. Plain `http://` is only accepted for localhost, private IPs, `.internal`, `.local` and single-label hosts, or with `allow_insecure: true`. |
| `401` from the server | The token was revoked or mistyped: create a new one in the wizard. |
| `permission denied` on a log | Mount the directory (Docker) or add the user to the log's group (systemd). |
| Database errors in the log | Check the grants and that `performance_schema` / `pg_stat_statements` are enabled. |

## Remove a machine

Stop the agent first (`docker rm -f slowpoke-agent` or `sudo systemctl disable --now slowpoke-agent`), then remove the
machine under **Machines**. Its measures (CPU, memory, connections) are deleted and its token is revoked; project
statistics, missions and points stay.
