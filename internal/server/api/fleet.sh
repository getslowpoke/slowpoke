#!/bin/sh
# Slowpoke fleet: installs the agent on the machines this project talks to and nobody measures.
#
#   curl -fsSL SERVER/fleet.sh | sh -s -- --token spu_... --project my-project --dry-run
#
# It runs on YOUR computer, with YOUR ssh keys and your ~/.ssh/config: the panel never gets
# access to anything. It asks the panel which machines are still dark, tries to reach each one,
# creates a token for it, and runs the same one-line install you would have run by hand.
#
#   --token spu_...   a personal token (Settings -> Access), the only thing it needs from you
#   --project SLUG    which project the machines belong to
#   --only a,b,c      these machines instead of the ones the panel found (for machines nobody
#                     talks to: discovery sees neighbours, not everything)
#   --user NAME       ssh user (default: whatever your ssh config says)
#   --auto-update     the installed agents follow the version of the server by themselves
#   --dry-run         say what it would do and stop
#   --yes             do not ask
set -eu

SERVER="@@SERVER@@"
TOKEN=""
PROJECT=""
ONLY=""
SSH_USER=""
AUTO=""
DRY=""
ASSUME_YES=""

say() { printf '%s\n' "$*"; }
die() { printf 'slowpoke: %s\n' "$*" >&2; exit 1; }
usage() { say "usage: sh fleet.sh --token spu_... --project SLUG [--only a,b] [--dry-run] [--yes]"; }

while [ $# -gt 0 ]; do
	case "$1" in
	--token) TOKEN="${2:-}"; shift 2 ;;
	--token=*) TOKEN="${1#*=}"; shift ;;
	--project) PROJECT="${2:-}"; shift 2 ;;
	--project=*) PROJECT="${1#*=}"; shift ;;
	--only) ONLY="${2:-}"; shift 2 ;;
	--only=*) ONLY="${1#*=}"; shift ;;
	--user) SSH_USER="${2:-}"; shift 2 ;;
	--user=*) SSH_USER="${1#*=}"; shift ;;
	--server) SERVER="${2:-}"; shift 2 ;;
	--server=*) SERVER="${1#*=}"; shift ;;
	--auto-update) AUTO=" --auto-update"; shift ;;
	--dry-run) DRY=1; shift ;;
	--yes) ASSUME_YES=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) usage >&2; die "unknown option: $1" ;;
	esac
done

[ -n "$TOKEN" ] || { usage >&2; die "missing --token (Settings -> Access -> personal token)"; }
[ -n "$PROJECT" ] || { usage >&2; die "missing --project"; }
command -v ssh >/dev/null 2>&1 || die "ssh not found: this runs on your own computer, with your keys"
command -v curl >/dev/null 2>&1 || die "curl not found"

SSH="ssh -n -o BatchMode=yes -o ConnectTimeout=8"
target() { [ -n "$SSH_USER" ] && printf '%s@%s' "$SSH_USER" "$1" || printf '%s' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

if [ -n "$ONLY" ]; then
	printf '%s\n' "$ONLY" | tr ',' '\n' | while read -r m; do [ -n "$m" ] && printf '%s\t%s\t-\n' "$m" "$m"; done >"$TMP/list"
else
	say "-> Asking $SERVER which machines nobody measures"
	curl -fsS -H "Authorization: Bearer $TOKEN" "$SERVER/api/projects/$PROJECT/fleet" >"$TMP/list" ||
		die "cannot read the fleet: is the token a personal one, and the project $PROJECT?"
fi

if [ ! -s "$TMP/list" ]; then
	say "Every machine this project talks to already has an agent."
	say "Machines nobody talks to are invisible from here: add them with --only host1,host2"
	exit 0
fi

say ""
say "Machines without an agent:"
while IFS="$(printf '\t')" read -r address name services; do
	[ -n "$address" ] || continue
	say "  $address  ($name, answers: $services)"
done <"$TMP/list"

if [ -n "$DRY" ]; then
	say ""
	say "Nothing was done. Without --dry-run, each machine gets its own token and this command:"
	say "  curl -fsSL $SERVER/install.sh | sudo sh -s -- --token <its token> --name <its hostname>$AUTO"
	exit 0
fi
if [ -z "$ASSUME_YES" ]; then
	printf '\nInstall the agent on these machines? [y/N] '
	read -r answer </dev/tty || answer=""
	case "$answer" in y | Y | yes | YES) ;; *) say "Nothing was done."; exit 0 ;; esac
fi

DONE=0
SKIPPED=0
FAILED=0
while IFS="$(printf '\t')" read -r address name services; do
	[ -n "$address" ] || continue
	host="$(target "$address")"
	printf '\n%s: ' "$address"

	if ! $SSH "$host" true >/dev/null 2>&1; then
		printf 'cannot connect over ssh (no key, no access, or not a machine)\n'
		FAILED=$((FAILED + 1))
		continue
	fi
	if $SSH "$host" 'systemctl is-active slowpoke-agent' >/dev/null 2>&1; then
		printf 'already has an agent\n'
		SKIPPED=$((SKIPPED + 1))
		continue
	fi
	# When you named the machine yourself, that is its name: it is how you call it everywhere
	# else. Only a machine found by its address is named after its own hostname.
	case "$address" in
	*[!0-9.]*) hostname="$address" ;;
	*) hostname="$($SSH "$host" 'hostname' 2>/dev/null | tr -d '\r')" ;;
	esac
	[ -n "$hostname" ] || hostname="$address"

	agent_token="$(curl -fsS -X POST \
		-H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
		-H "Accept: text/plain" -H "X-Slowpoke: 1" \
		-d "{\"kind\":\"agent\",\"name\":\"$hostname\"}" \
		"$SERVER/api/projects/$PROJECT/tokens" | tr -d '\r\n')"
	if [ -z "$agent_token" ]; then
		printf 'the panel did not give a token for it\n'
		FAILED=$((FAILED + 1))
		continue
	fi

	printf 'installing on %s... ' "$hostname"
	if $SSH "$host" "curl -fsSL $SERVER/install.sh | sudo sh -s -- --token $agent_token --name $hostname$AUTO" >"$TMP/out" 2>&1; then
		printf 'done\n'
		DONE=$((DONE + 1))
	else
		printf 'failed\n'
		sed 's/^/    /' "$TMP/out" | tail -5
		FAILED=$((FAILED + 1))
	fi
done <"$TMP/list"

say ""
say "Installed: $DONE · already had one: $SKIPPED · could not: $FAILED"
say "They show up in the panel within a minute: $SERVER"
