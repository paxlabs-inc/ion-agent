# GitHub traction baseline probe

Use unauthenticated public API when possible; auth only if rate-limited. Prefer a **script file** over nested `python3 -c` quoting.

## Endpoints

```
GET https://api.github.com/repos/{owner}/{repo}
GET https://api.github.com/repos/{owner}/{repo}/languages
GET https://api.github.com/repos/{owner}/{repo}/releases
GET https://api.github.com/repos/{owner}/{repo}/tags?per_page=10
GET https://api.github.com/repos/{owner}/{repo}/community/profile
GET https://api.github.com/repos/{owner}/{repo}/issues?state=all&per_page=30
GET https://api.github.com/repos/{owner}/{repo}/stats/participation
GET https://api.github.com/users/{login}
GET https://api.github.com/users/{login}/repos?per_page=100
GET https://api.github.com/orgs/{org}/repos?per_page=100
```

Headers: `User-Agent: neo-research`, `Accept: application/vnd.github+json`  
Topics: `Accept: application/vnd.github.mercy-preview+json` on topics endpoint if used.

## Fields that matter for traction reports

From repo object:

- `stargazers_count`, `forks_count`, `subscribers_count`, `open_issues_count`
- `created_at`, `pushed_at`, `homepage`, `language`, `topics`
- `has_issues`, `has_discussions`, `has_wiki`
- `license` (`spdx_id` often `NOASSERTION` for custom)
- `owner.type` via nested owner or user endpoint (**User vs Organization**)

From community profile:

- `health_percentage`
- `files.issue_template` (null = missing templates — common blocker)
- `files.license`, `contributing`, `readme`, CoC

Issues feed:

- GitHub returns **PRs in `/issues`**. Filter: skip if `"pull_request" in item`.
- Zero real issues + active pushes ⇒ looks internal-only to outsiders.

Participation:

- `stats/participation` → weekly commit arrays (`all` vs `owner`) — useful “shipping signal” even when stars are low.

Traffic (`/traffic/views`, `/clones`) needs push access — expect 401/403 publicly; don’t invent numbers.

## HN presence

```
https://hn.algolia.com/api/v1/search?query={product+OR+repo}&tags=story
```

Record `nbHits` and top titles/points. Zero hits = greenfield for Show HN.

## Sibling dilution

List owner repos sorted by stars. If one hero sits at N× other repos, recommend **single star target** + ecosystem links, not equal-weight promotion of empty sibling repos.

## License read

Fetch `LICENSE*` raw content (or `/contents/LICENSE.md` base64). Extract:

- SPDX / name
- Free use boundary
- Commercial triggers (fees, AUM, seats)
- Copyleft on modifications

Report **optics** (badge says Other) separately from **actual terms** (may be free under high triggers).

## Minimal Python skeleton

```python
#!/usr/bin/env python3
import json, urllib.request

def get(url):
    req = urllib.request.Request(
        url,
        headers={"User-Agent": "neo-research", "Accept": "application/vnd.github+json"},
    )
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)

repo = get("https://api.github.com/repos/OWNER/REPO")
# print selected keys; filter issues; list releases...
```

Save under `/tmp/gh_traction_probe.py` and iterate.
