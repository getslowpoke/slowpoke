#!/bin/sh
# Slowpoke agent installer.
#
#   curl -fsSL SERVER/install.sh | sudo sh -s -- --token spa_...
#
# It installs one small agent on this machine, lets it look at what is already running here,
# writes its own configuration and starts it. It never changes the configuration of your
# services: whatever it cannot read yet is reported in the panel with the line that turns it on.
#
#   --token spa_...  the token of this machine (required, from the panel: Machines -> Add agent)
#   --name NAME      how this machine is called in the panel (default: its hostname)
#   --env KEY=VALUE  a password the configuration refers to, kept out of it (repeatable),
#                    e.g. --env SLOWPOKE_DB_PASSWORD=...
#   --auto-update    let the agent replace itself with the one this server hands out, whenever
#                    the server is a different version (off unless you ask for it)
#   --server URL     Slowpoke server (default: the one that served this script)
#   --force          write a new configuration over an existing one
#   --uninstall      stop the agent and remove every file it owns
set -eu

SERVER="@@SERVER@@"
TOKEN=""
NAME=""
FORCE=""
UNINSTALL=""
AUTO_UPDATE=""

# Paths, overridable for unusual layouts and for the tests that exercise this script.
BIN="${SLOWPOKE_BIN:-/usr/local/bin/slowpoke-agent}"
# With --auto-update the agent replaces its own binary, so the binary cannot live in /usr, which
# the unit keeps read-only: it goes in a directory of its own, owned by the agent's user and the
# only thing the unit opens for writing, and BIN becomes a link to it.
LIB="${SLOWPOKE_LIB:-/usr/local/lib/slowpoke}"
CONFIG="${SLOWPOKE_CONFIG:-/etc/slowpoke/agent.yaml}"
UNIT="${SLOWPOKE_UNIT:-/etc/systemd/system/slowpoke-agent.service}"
STATE="${SLOWPOKE_STATE:-/var/lib/slowpoke}"
SYSTEMD_DIR="${SLOWPOKE_SYSTEMD_DIR:-/run/systemd/system}"

say() { printf '%s\n' "$*"; }
die() { printf 'slowpoke: %s\n' "$*" >&2; exit 1; }

usage() {
	say "usage: sh install.sh --token spa_... [--name NAME] [--env KEY=VALUE] [--auto-update] [--server URL] [--force] [--uninstall]"
}

# Passwords given with --env are collected here and end up in agent.env, never in the panel.
ENV_LINES=""
add_env() {
	case "$1" in
	*=*) ;;
	*) die "--env wants KEY=VALUE, got $1" ;;
	esac
	ENV_LINES="$ENV_LINES$1
"
}

while [ $# -gt 0 ]; do
	case "$1" in
	--token) TOKEN="${2:-}"; shift 2 ;;
	--token=*) TOKEN="${1#*=}"; shift ;;
	--name) NAME="${2:-}"; shift 2 ;;
	--name=*) NAME="${1#*=}"; shift ;;
	--env) add_env "${2:-}"; shift 2 ;;
	--env=*) add_env "${1#*=}"; shift ;;
	--server) SERVER="${2:-}"; shift 2 ;;
	--server=*) SERVER="${1#*=}"; shift ;;
	--auto-update) AUTO_UPDATE=1; shift ;;
	--force) FORCE=1; shift ;;
	--uninstall) UNINSTALL=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) usage >&2; die "unknown option: $1" ;;
	esac
done

[ "$(id -u)" = 0 ] || die "run this as root: pipe it into 'sudo sh -s -- --token spa_...'"
[ -d "$SYSTEMD_DIR" ] || die "this machine does not run systemd: install the agent with Docker instead (see the agent guide)"

if [ -n "$UNINSTALL" ]; then
	systemctl disable --now slowpoke-agent >/dev/null 2>&1 || true
	rm -f "$UNIT" "$BIN"
	systemctl daemon-reload >/dev/null 2>&1 || true
	rm -rf "$(dirname "$CONFIG")" "$STATE" "$LIB"
	(userdel slowpoke || deluser slowpoke) >/dev/null 2>&1 || true
	say "Slowpoke agent removed. Nothing else on this machine was touched."
	exit 0
fi

[ -n "$TOKEN" ] || { usage >&2; die "missing --token"; }
[ -n "$SERVER" ] || { usage >&2; die "missing --server"; }
[ "$(uname -s)" = "Linux" ] || die "the agent runs on Linux, this is $(uname -s)"

case "$(uname -m)" in
x86_64 | amd64) PLATFORM=linux-amd64 ;;
aarch64 | arm64) PLATFORM=linux-arm64 ;;
*) die "no agent for $(uname -m): build it from source (docs/install/agent.md)" ;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL -H "Authorization: Bearer $TOKEN" "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q --header="Authorization: Bearer $TOKEN" -O "$2" "$1"; }
else
	die "neither curl nor wget is installed"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

say "-> Downloading the agent ($PLATFORM) from $SERVER"
fetch "$SERVER/agent/$PLATFORM" "$TMP/agent" ||
	die "cannot download the agent: is $SERVER reachable from this machine, and is the token valid?"
fetch "$SERVER/agent/$PLATFORM.sha256" "$TMP/sum" || die "cannot download the checksum from $SERVER"

WANT="$(cut -d' ' -f1 <"$TMP/sum")"
GOT=""
if command -v sha256sum >/dev/null 2>&1; then
	GOT="$(sha256sum "$TMP/agent" | cut -d' ' -f1)"
elif command -v shasum >/dev/null 2>&1; then
	GOT="$(shasum -a 256 "$TMP/agent" | cut -d' ' -f1)"
fi
[ -z "$GOT" ] || [ "$GOT" = "$WANT" ] || die "checksum mismatch: expected $WANT, downloaded $GOT"

# Run it before trusting it. A machine of ours once got the panel's own web page installed as its
# agent, because an older server answered this path with its index page: the checksum matched, of
# course, since both came from the same server.
chmod 755 "$TMP/agent"
"$TMP/agent" -version >/dev/null 2>&1 ||
	die "what $SERVER handed out for $PLATFORM does not run: it is not an agent for this machine. Update the server, or check its agent directory. Nothing was changed here."

# A user that can read logs and write nothing.
if ! id slowpoke >/dev/null 2>&1; then
	if command -v useradd >/dev/null 2>&1; then
		useradd --system --no-create-home --shell /usr/sbin/nologin slowpoke
	else
		adduser -S -H -s /sbin/nologin slowpoke >/dev/null 2>&1 || die "cannot create the slowpoke user"
	fi
fi
# On Debian and Ubuntu the adm group reads /var/log; elsewhere the unit uses a capability.
if getent group adm >/dev/null 2>&1; then
	(usermod -aG adm slowpoke || addgroup slowpoke adm) >/dev/null 2>&1 || true
fi

# Where the agent runs from. Without auto-update it stays where it always was, owned by root.
EXE="$BIN"
if [ -n "$AUTO_UPDATE" ]; then
	EXE="$LIB/slowpoke-agent"
	install -d -m 755 "$LIB"
elif [ -L "$BIN" ]; then
	# It was following the server and now it is not: back to one binary, owned by root.
	rm -f "$BIN" "$LIB/slowpoke-agent"
fi
install -m 755 "$TMP/agent" "$EXE"
if [ "$EXE" != "$BIN" ]; then
	rm -f "$BIN"
	ln -s "$EXE" "$BIN"
fi
CONFIG_DIR="$(dirname "$CONFIG")"
install -d -m 750 "$CONFIG_DIR"
chgrp slowpoke "$CONFIG_DIR" 2>/dev/null || true

if [ -f "$CONFIG" ] && [ -z "$FORCE" ]; then
	say "-> Keeping the configuration already in $CONFIG (--force writes a new one)"
else
	say "-> Looking at this machine"
	if [ -n "$NAME" ]; then
		"$BIN" detect -server "$SERVER" -token "$TOKEN" -name "$NAME" -write "$CONFIG" -force
	else
		"$BIN" detect -server "$SERVER" -token "$TOKEN" -write "$CONFIG" -force
	fi
fi
if [ -n "$AUTO_UPDATE" ]; then
	# Replacing a file means renaming inside its directory, so the directory is what the agent
	# must own. Nothing else on the machine becomes writable by it.
	chown -R slowpoke "$LIB" 2>/dev/null || true
	if ! grep -q '^auto_update:' "$CONFIG"; then
		printf 'auto_update: true\n' >>"$CONFIG"
	fi
	say "-> This agent will follow the version of the server"
fi
chgrp slowpoke "$CONFIG" 2>/dev/null || true
chmod 640 "$CONFIG"

# agent.env holds what the configuration refers to but must not contain: database and cache
# passwords. The unit reads it if it exists; the values given now replace the ones with the
# same name and leave the others alone.
ENVFILE="$CONFIG_DIR/agent.env"
if [ -n "$ENV_LINES" ]; then
	printf '%s' "$ENV_LINES" >"$TMP/env"
	if [ -f "$ENVFILE" ]; then
		while IFS= read -r line; do
			[ -n "$line" ] || continue
			key="${line%%=*}"
			grep -q "^$key=" "$TMP/env" || printf '%s\n' "$line" >>"$TMP/env"
		done <"$ENVFILE"
	fi
	install -m 640 "$TMP/env" "$ENVFILE"
	chgrp slowpoke "$ENVFILE" 2>/dev/null || true
	say "-> Passwords written to $ENVFILE"
fi

UNIT_WRITABLE=""
if [ -n "$AUTO_UPDATE" ]; then
	UNIT_WRITABLE="ReadWritePaths=$LIB"
fi
install -d -m 755 "$(dirname "$UNIT")"
cat >"$UNIT" <<UNITFILE
[Unit]
Description=Slowpoke agent
Documentation=https://github.com/getslowpoke/slowpoke
After=network-online.target

[Service]
User=slowpoke
SupplementaryGroups=adm
# Read any log (PHP-FPM writes its slow log as root with mode 0600) without being able to write anything.
AmbientCapabilities=CAP_DAC_READ_SEARCH
CapabilityBoundingSet=CAP_DAC_READ_SEARCH
EnvironmentFile=-$ENVFILE
ExecStart=$EXE -config $CONFIG
StateDirectory=slowpoke
Restart=always
RestartSec=5
Nice=10
NoNewPrivileges=true
ProtectSystem=strict
$UNIT_WRITABLE
ProtectHome=true
PrivateTmp=true
MemoryMax=128M

[Install]
WantedBy=multi-user.target
UNITFILE

systemctl daemon-reload
systemctl enable slowpoke-agent >/dev/null 2>&1 || true
systemctl restart slowpoke-agent

say ""
say "The agent is running. This machine shows up in the panel within a minute:"
say "  $SERVER"
say ""
say "  systemctl status slowpoke-agent     journalctl -u slowpoke-agent -f"
say "  The machine page says what it reads, what it costs and what is still off."
