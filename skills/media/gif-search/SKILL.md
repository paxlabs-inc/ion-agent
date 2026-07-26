---
name: gif-search
description: "Search/download GIFs from Tenor via curl + jq."
version: 1.1.0
author: Ion Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  env_vars: [TENOR_API_KEY]
  commands: [curl, jq]
metadata:
  ion:
    tags: [GIF, Media, Search, Tenor, API]
---

# GIF Search (Tenor API)

Look up and save GIFs straight from the Tenor API with curl. No additional
libraries required.

## When to use

Handy when you need reaction GIFs, want to produce visual assets, or need to
drop a GIF into a conversation.

## Setup

Export your Tenor API key (add it to `${ION_HOME:-~/.ion}/.env`):

```bash
TENOR_API_KEY=your_key_here
```

Grab a free key from https://developers.google.com/tenor/guides/quickstart —
the Google Cloud Console Tenor API key costs nothing and comes with generous
rate limits.

## Prerequisites

- `curl` and `jq` (both ship by default on macOS/Linux)
- `TENOR_API_KEY` environment variable set

## Search for GIFs

```bash
# Search and get GIF URLs
curl -s "https://tenor.googleapis.com/v2/search?q=thumbs+up&limit=5&key=${TENOR_API_KEY}" | jq -r '.results[].media_formats.gif.url'

# Get smaller/preview versions
curl -s "https://tenor.googleapis.com/v2/search?q=nice+work&limit=3&key=${TENOR_API_KEY}" | jq -r '.results[].media_formats.tinygif.url'
```

## Download a GIF

```bash
# Search and download the top result
URL=$(curl -s "https://tenor.googleapis.com/v2/search?q=celebration&limit=1&key=${TENOR_API_KEY}" | jq -r '.results[0].media_formats.gif.url')
curl -sL "$URL" -o celebration.gif
```

## Get Full Metadata

```bash
curl -s "https://tenor.googleapis.com/v2/search?q=cat&limit=3&key=${TENOR_API_KEY}" | jq '.results[] | {title: .title, url: .media_formats.gif.url, preview: .media_formats.tinygif.url, dimensions: .media_formats.gif.dims}'
```

## API Parameters

| Parameter | Description |
|-----------|-------------|
| `q` | Search query (URL-encode spaces as `+`) |
| `limit` | Max results (1-50, default 20) |
| `key` | API key (from `$TENOR_API_KEY` env var) |
| `media_filter` | Filter formats: `gif`, `tinygif`, `mp4`, `tinymp4`, `webm` |
| `contentfilter` | Safety: `off`, `low`, `medium`, `high` |
| `locale` | Language: `en_US`, `es`, `fr`, etc. |

## Available Media Formats

Every result contains several formats under `.media_formats`:

| Format | Use case |
|--------|----------|
| `gif` | Full quality GIF |
| `tinygif` | Small preview GIF |
| `mp4` | Video version (smaller file size) |
| `tinymp4` | Small preview video |
| `webm` | WebM video |
| `nanogif` | Tiny thumbnail |

## Notes

- URL-encode the query: use `+` for spaces and `%XX` for special characters
- `tinygif` URLs are better suited for chat when bandwidth matters
- GIF URLs work directly in markdown: `![alt](url)`
