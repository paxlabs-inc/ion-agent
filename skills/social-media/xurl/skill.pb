meta {
  name: "xurl"
  version: "1.1.1"
  summary: "X/Twitter via xurl CLI — post, search, DM, media, v2 API."
  author: "xdevplatform + openclaw + Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
}

triggers {
  keywords: "xurl"
  keywords: "twitter"
  keywords: "x api"
  keywords: "tweet"
  keywords: "post to x"
  keywords: "search x"
  keywords: "dm"
  intents: "post_tweet"
  intents: "search_tweets"
  intents: "send_dm"
  intents: "upload_media"
  intents: "manage_followers"
  intents: "engage_tweet"
  patterns: "(post|tweet|search|dm|follow|like|repost) .*x(\\.com)?"
  patterns: "xurl"
  patterns: "twitter .*(post|search|follow|dm)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "xurl"
}

provides {
  capabilities: "x_twitter_api"
  output_types: "json"
}

actions {
  id: "xurl_posting"
  description: "Post, reply, quote, or delete on X"
  trigger_phrases: "post to x"
  trigger_phrases: "tweet this"
  trigger_phrases: "reply to"
  trigger_phrases: "quote tweet"
  trigger_phrases: "delete post"
    rules {
      text: "Never read, print, parse, or send ~/.xurl to LLM context — credentials must stay private"
      priority: CRITICAL
    }
    rules {
      text: "Never use --verbose/-v in agent sessions — it leaks auth headers/tokens"
      priority: CRITICAL
    }
    rules {
      text: "Forbidden flags: --bearer-token, --consumer-key, --consumer-secret, --access-token, --token-secret, --client-id, --client-secret"
      priority: CRITICAL
    }
    rules {
      text: "Verify auth first: xurl auth status — confirm default app has credentials before any write"
      priority: HIGH
    }
    rules {
      text: "Confirm target post/user and intent before any write action (post, reply, like, repost, DM, follow, block, delete)"
      priority: HIGH
    }
    rules {
      text: "POST_ID accepts full URLs — xurl extracts the ID automatically"
      priority: NORMAL
    }
    data {
      key: "quick_ref"
      map_value {
        entries {
          key: "post"
          string_value: "xurl post \"text\""
        }
        entries {
          key: "reply"
          string_value: "xurl POST_ID \"text\""
        }
        entries {
          key: "quote"
          string_value: "xurl POST_ID \"text\""
        }
        entries {
          key: "delete"
          string_value: "xurl delete POST_ID"
        }
      }
    }
    examples {
      label: "post with media"
      language: "bash"
      code: "xurl media upload photo.jpg\nxurl post \"Check this out!\" --media-id MEDIA_ID"
    }
}
actions {
  id: "xurl_reading"
  description: "Read posts, search, check timeline and mentions"
  trigger_phrases: "search x"
  trigger_phrases: "read tweet"
  trigger_phrases: "x timeline"
  trigger_phrases: "mentions"
  trigger_phrases: "search twitter"
    rules {
      text: "Never read ~/.xurl or expose credentials"
      priority: CRITICAL
    }
    rules {
      text: "Start with a cheap read (xurl whoami, xurl search ... -n 3) to confirm reachability"
      priority: HIGH
    }
    rules {
      text: "All output is JSON to stdout — parse directly, don't reformat"
      priority: NORMAL
    }
    data {
      key: "search_examples"
      list_value {
        items {
          string_value: "xurl search \"golang\""
        }
        items {
          string_value: "xurl search \"from:elonmusk\" -n 20"
        }
        items {
          string_value: "xurl search \"#buildinpublic lang:en\" -n 15"
        }
      }
    }
    examples {
      label: "search and engage"
      language: "bash"
      code: "xurl search \"topic of interest\" -n 10\nxurl like POST_ID_FROM_RESULTS\nxurl reply POST_ID_FROM_RESULTS \"Great point!\""
    }
}
actions {
  id: "xurl_engagement"
  description: "Like, repost, bookmark, follow, block, mute"
  trigger_phrases: "like on x"
  trigger_phrases: "repost"
  trigger_phrases: "bookmark"
  trigger_phrases: "follow on x"
  trigger_phrases: "block user"
  trigger_phrases: "mute"
    rules {
      text: "Never expose credentials or use forbidden flags"
      priority: CRITICAL
    }
    rules {
      text: "Confirm target and intent before any engagement action"
      priority: HIGH
    }
    rules {
      text: "Commands needing caller's user ID (like, repost, bookmark, follow) auto-fetch via /2/users/me"
      priority: NORMAL
    }
    examples {
      label: "follow and engage"
      language: "bash"
      code: "xurl follow @XDevelopers\nxurl like 1234567890\nxurl bookmark 1234567890"
    }
}
actions {
  id: "xurl_direct_messages"
  description: "Send and list direct messages"
  trigger_phrases: "dm on x"
  trigger_phrases: "send dm"
  trigger_phrases: "list dms"
  trigger_phrases: "direct message"
    rules {
      text: "Never expose credentials"
      priority: CRITICAL
    }
    rules {
      text: "Confirm recipient and message content before sending"
      priority: HIGH
    }
    examples {
      label: "send a DM"
      language: "bash"
      code: "xurl dm @someuser \"Hey, saw your post!\""
    }
}
actions {
  id: "xurl_media"
  description: "Upload media files for use in posts"
  trigger_phrases: "upload media to x"
  trigger_phrases: "upload image to x"
  trigger_phrases: "upload video to x"
    rules {
      text: "Videos need server-side processing — use xurl media status --wait MEDIA_ID to poll"
      priority: HIGH
    }
    rules {
      text: "For image upload failures with 'media processing failed', add --category tweet_image --media-type image/png"
      priority: NORMAL
    }
    examples {
      label: "upload and post with media"
      language: "bash"
      code: "xurl media upload meme.png\nxurl post \"lol\" --media-id MEDIA_ID"
    }
}
actions {
  id: "xurl_raw_api"
  description: "Raw access to any X API v2 endpoint"
  trigger_phrases: "raw x api"
  trigger_phrases: "x api v2"
  trigger_phrases: "xurl raw"
    rules {
      text: "Use xurl /2/... for GET, xurl -X POST /2/... -d '{...}' for POST"
      priority: HIGH
    }
    rules {
      text: "Force streaming with -s flag on streaming endpoints"
      priority: NORMAL
    }
    examples {
      label: "raw API call"
      language: "bash"
      code: "xurl /2/users/me\nxurl -X POST /2/tweets -d '{\"text\":\"Hello world!\"}'"
    }
}

guardrails {
  text: "Never read, print, or send ~/.xurl to LLM context"
  scope: ALWAYS
}

guardrails {
  text: "Never use --verbose/-v — leaks auth headers"
  scope: ALWAYS
}

guardrails {
  text: "Never pass secrets via command-line flags"
  scope: AUTH_OPS
}
