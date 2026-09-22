# Deploys

Recording deploys lets Slowpoke judge every release, link regressions (bosses) to the change that caused them, and
compute delivery health.

## Record a deploy

Call the machine API at the end of your deploy script or CI job, with the project token (`spk_…`) or an agent token
(`spa_…`):

```sh
curl -fsS -X POST https://slowpoke.example.com/api/v1/deploys \
  -H "Authorization: Bearer $SLOWPOKE_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"version\": \"$(git describe --tags --always)\",
       \"commit\": \"$(git rev-parse HEAD)\",
       \"author\": \"$(git log -1 --format=%ae)\",
       \"message\": \"$(git log -1 --format=%s | tr -d '\"')\",
       \"url\": \"https://github.com/acme/shop/commit/$(git rev-parse HEAD)\"}"
```

| Field | Limit | Meaning |
|---|---|---|
| `version` | 100 characters | Tag or release name |
| `commit` | 64 characters | Commit hash |
| `author` | 100 characters | Name or email of whoever deployed. Used to credit missions, own bosses and send direct messages. |
| `message` | 500 characters | Commit or release message |
| `url` | 500 characters | Link to the commit, pull request or pipeline |
| `deployed_at` | | Unix time in seconds. Missing, or more than an hour in the future: now. |

`version` or `commit` is enough.

### GitHub Actions

```yaml
- name: Tell Slowpoke
  if: success()
  run: |
    curl -fsS -X POST "${{ vars.SLOWPOKE_URL }}/api/v1/deploys" \
      -H "Authorization: Bearer ${{ secrets.SLOWPOKE_TOKEN }}" \
      -H "Content-Type: application/json" \
      -d '{"version": "${{ github.ref_name }}", "commit": "${{ github.sha }}",
           "author": "${{ github.actor }}", "url": "${{ github.server_url }}/${{ github.repository }}/commit/${{ github.sha }}"}'
```

### Deployer

On a machine with the Slowpoke agent, the deploy can take the server and the token from the
agent's own configuration: add this to `deploy.php` (Deployer 6 or 7). It runs once even with
several hosts, does nothing where there is no agent, and never fails a deploy.

```php
task('slowpoke:deploy', function () {
    try {
        $commit = trim(runLocally('git ls-remote ' . escapeshellarg(get('repository')) . ' ' . escapeshellarg('refs/heads/' . get('branch')) . ' | cut -f1'));
        $payload = json_encode([
            'version' => get('application') . '@' . substr($commit, 0, 8),
            'commit'  => $commit,
            'author'  => trim(runLocally('git config user.email || true')),
            'message' => substr(trim(runLocally('git log -1 --format=%s ' . escapeshellarg($commit) . ' 2>/dev/null || true')), 0, 500),
        ]);
        $script = <<<'SH'
f=/etc/slowpoke/agent.yaml
[ -r "$f" ] || exit 0
server=$(sed -n 's/^server:[[:space:]]*//p' "$f" | head -1 | tr -d "\"' ")
token=$(sed -n 's/^token:[[:space:]]*//p' "$f" | head -1 | tr -d "\"' ")
curl -fsS -m 10 -X POST "$server/api/v1/deploys" -H "Authorization: Bearer $token" -H "Content-Type: application/json" -d "$SLOWPOKE_DEPLOY" >/dev/null || echo "slowpoke: deploy not recorded"
SH;
        run('SLOWPOKE_DEPLOY=' . escapeshellarg($payload) . ' bash -c ' . escapeshellarg($script) . ' || true');
    } catch (\Throwable $e) {
        writeln('<comment>Slowpoke: deploy not recorded</comment>');
    }
})->once();
after('deploy', 'slowpoke:deploy');
```

The deploy user must be able to read `/etc/slowpoke/agent.yaml` (it is when Deployer connects as
root); otherwise call the API from your machine with `runLocally` and a project token.

## The verdict

Slowpoke compares the speed index over the `deploys.compare_minutes` (60 by default) after each deploy
with the same minutes of the days before (weekdays with weekdays, weekends with weekends): at 18:00
the traffic changes on any day, deploy or not. While there are fewer than three such days, it
compares with the minutes right before the deploy.

| Verdict | When |
|---|---|
| **Better** | +3 index points or more. Worth 30 points and the **Green deploy** badge. |
| **Worse** | −3 points or more |
| **Neutral** | In between |
| **No data** | No index on one side, or no requests after the deploy |

Deploys up to 7 days old are evaluated as data arrives.

## Regressions

When a debt item doubles its recent cost, the boss that appears is linked to the latest deploy of the last two
windows and owned by its author. See [Bosses](game.md#bosses).
