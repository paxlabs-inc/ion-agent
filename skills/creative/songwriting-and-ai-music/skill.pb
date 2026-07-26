meta {
  name: "songwriting-and-ai-music"
  version: "1.0.0"
  summary: "Songwriting craft and Suno AI music prompt engineering"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "songwriting"
  keywords: "music"
  keywords: "suno"
  keywords: "parody"
  keywords: "lyrics"
  keywords: "song"
  keywords: "AI music"
  keywords: "music prompt"
  intents: "write_song"
  intents: "create_parody"
  intents: "suno_prompt"
  intents: "adapt_song"
  intents: "generate_music"
  patterns: "(write|create|compose) .*(song|lyrics|parody)"
  patterns: "(suno|music) .*(prompt|generation)"
  patterns: "(adapt|rewrite) .*(song|lyrics)"
  patterns: "parody"
}

requires {
}

provides {
  capabilities: "songwriting"
  capabilities: "lyrics_craft"
  capabilities: "suno_prompts"
  capabilities: "parody_adaptation"
  output_types: ".txt"
}

actions {
  id: "song_structure"
  description: "Design song structure — pick from common skeletons or invent"
  trigger_phrases: "song structure"
  trigger_phrases: "arrange the song"
  trigger_phrases: "verse chorus structure"
    rules {
      text: "Common skeletons: ABABCB (most pop/rock), AABA (jazz/ballads), ABAB (simple/direct), AAA (folk/storytelling)."
      priority: HIGH
    }
    rules {
      text: "Six building blocks: Intro, Verse, Pre-Chorus, Chorus, Bridge, Outro. You don't need all of them."
      priority: HIGH
    }
    rules {
      text: "Structure serves emotion, not the other way around. Some great songs are just one evolving section."
      priority: NORMAL
    }
    data {
      key: "structures"
      map_value {
        entries {
          key: "ABABCB"
          string_value: "Verse/Chorus/Verse/Chorus/Bridge/Chorus — most pop/rock"
        }
        entries {
          key: "AABA"
          string_value: "Verse/Verse/Bridge/Verse — jazz standards, ballads"
        }
        entries {
          key: "ABAB"
          string_value: "Verse/Chorus alternating — simple, direct"
        }
        entries {
          key: "AAA"
          string_value: "Verse/Verse/Verse strophic — folk, storytelling"
        }
      }
    }
}
actions {
  id: "write_lyrics"
  description: "Write lyrics with attention to rhyme, meter, emotional arc, and prosody"
  trigger_phrases: "write lyrics"
  trigger_phrases: "compose lyrics"
  trigger_phrases: "lyric writing"
    rules {
      text: "Show don't tell: 'Your hoodie's still on the hook by the door' > 'I was sad'. But sometimes plain power works: 'I give my life'."
      priority: CRITICAL
    }
    rules {
      text: "Mix rhyme types: perfect, family, assonance, consonance, near/slant. All perfect = nursery rhyme, all slant = lazy."
      priority: HIGH
    }
    rules {
      text: "The hook: the line people remember. Usually the title or core phrase. Place where it lands hardest."
      priority: HIGH
    }
    rules {
      text: "Prosody: stable feelings → settled melodies + perfect rhymes. Unstable feelings → wandering melodies + near-rhymes."
      priority: HIGH
    }
    rules {
      text: "Emotional arc: Intro 2-3, Verse 5-6, Pre-Chorus 7, Chorus 8-9, Final Chorus 9-10. CONTRAST is the most powerful trick."
      priority: HIGH
    }
    rules {
      text: "Avoid: cliches on autopilot, forcing word order for rhyme, same energy in every section, treating first draft as sacred."
      priority: NORMAL
    }
}
actions {
  id: "parody_adaptation"
  description: "Rewrite existing song with new lyrics while preserving rhythm and melody"
  trigger_phrases: "parody"
  trigger_phrases: "adapt a song"
  trigger_phrases: "rewrite lyrics"
  trigger_phrases: "new words to existing song"
    rules {
      text: "Map original structure first: count syllables per line, mark rhyme scheme, identify stressed syllables, note held notes."
      priority: CRITICAL
    }
    rules {
      text: "Match stressed syllables to same beats. Total syllable count can flex by 1-2 unstressed syllables."
      priority: HIGH
    }
    rules {
      text: "On long held notes, match vowel sound of original (LOOOVE→FOOOD, not LIFE)."
      priority: HIGH
    }
    rules {
      text: "Monosyllabic swaps in key spots keep rhythm intact (Crime→Code, Snake→Noose)."
      priority: HIGH
    }
    rules {
      text: "Keep some original lines intact — adds recognizability and emotional weight."
      priority: NORMAL
    }
}
actions {
  id: "suno_prompt"
  description: "Engineer Suno AI style and metatag prompts for music generation"
  trigger_phrases: "suno prompt"
  trigger_phrases: "suno style"
  trigger_phrases: "ai music prompt"
  trigger_phrases: "music generation prompt"
    rules {
      text: "Style field formula: Genre + Mood + Era + Instruments + Vocal Style + Production + Dynamics. Use up to 1000 chars in V4.5+."
      priority: CRITICAL
    }
    rules {
      text: "NO artist names or trademarks. Describe the sound instead: '90s grunge' not 'Nirvana-style'."
      priority: CRITICAL
    }
    rules {
      text: "Describe the JOURNEY not just genre: 'Begins as haunting whisper... builds through chorus with full orchestra... strips back to lone piano.'"
      priority: HIGH
    }
    rules {
      text: "Build a vocal PERSONA not just a gender: 'weathered torch singer, smoky alto, slight rasp, starts vulnerable, builds to devastating power'."
      priority: HIGH
    }
    rules {
      text: "Metatags in [brackets] inside lyrics: [Verse], [Chorus], [Whispered], [Belted], [High Energy], [Emotional Climax]."
      priority: HIGH
    }
    rules {
      text: "5-8 tags per section max. Don't contradict ([Calm] + [Aggressive] same section). Put tags in BOTH style and lyrics."
      priority: NORMAL
    }
    data {
      key: "metatag_categories"
      map_value {
        entries {
          key: "structure"
          string_value: "[Intro] [Verse] [Pre-Chorus] [Chorus] [Bridge] [Outro] [Instrumental]"
        }
        entries {
          key: "vocal"
          string_value: "[Whispered] [Belted] [Falsetto] [Powerful] [Soulful] [Raspy] [Breathy]"
        }
        entries {
          key: "dynamics"
          string_value: "[High Energy] [Building Energy] [Explosive] [Emotional Climax] [Quiet arrangement]"
        }
        entries {
          key: "gender"
          string_value: "[Female Vocals] [Male Vocals]"
        }
        entries {
          key: "atmosphere"
          string_value: "[Melancholic] [Euphoric] [Nostalgic] [Aggressive] [Dreamy] [Intimate]"
        }
      }
    }
    data {
      key: "phonetic_tricks"
      map_value {
        entries {
          key: "respelling"
          string_value: "through→thru, Nous→Noose"
        }
        entries {
          key: "caps"
          string_value: "ALL CAPS = louder, more intense"
        }
        entries {
          key: "vowel_extend"
          string_value: "lo-o-o-ove = sustained melisma"
        }
        entries {
          key: "ellipses"
          string_value: "I... need... you = dramatic pauses"
        }
        entries {
          key: "numbers"
          string_value: "spell out: 24/7 → twenty four seven"
        }
      }
    }
}
actions {
  id: "phonetic_tricks"
  description: "Phonetic respelling and delivery control for AI singers"
  trigger_phrases: "phonetic respelling"
  trigger_phrases: "ai pronunciation"
  trigger_phrases: "delivery control"
    rules {
      text: "Spell words as they sound. Proper nouns have highest failure rate — test in short 30s clip first."
      priority: HIGH
    }
    rules {
      text: "Spell out numbers: '24/7' → 'twenty four seven'. Space acronyms: 'AI' → 'A I'."
      priority: HIGH
    }
    rules {
      text: "Once generated, pronunciation is baked in — fix in lyrics BEFORE generation."
      priority: NORMAL
    }
}

guardrails {
  text: "Everything is a guideline, not a rule — art breaks rules on purpose. Use what serves the song."
  scope: ALWAYS
}

guardrails {
  text: "No artist names or trademarks in Suno prompts — describe the sound instead"
  scope: ALWAYS
}
