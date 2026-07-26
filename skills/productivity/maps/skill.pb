meta {
  name: "maps"
  version: "1.2.0"
  summary: "Geocode, POIs, routes, timezones via OpenStreetMap/OSRM"
  author: "Mibayy"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "maps"
  keywords: "geocode"
  keywords: "nearby"
  keywords: "directions"
  keywords: "distance"
  keywords: "routing"
  keywords: "places"
  keywords: "location"
  keywords: "coordinates"
  keywords: "latitude"
  keywords: "longitude"
  keywords: "overpass"
  keywords: "nominatim"
  keywords: "osrm"
  keywords: "timezone"
  intents: "geocode"
  intents: "reverse_geocode"
  intents: "find_nearby"
  intents: "get_distance"
  intents: "get_directions"
  intents: "get_timezone"
  intents: "get_area"
  intents: "bbox_search"
  patterns: "(find|search|show) .*(nearby|near|around|close to)"
  patterns: "(directions|navigate|route) .*(to|from)"
  patterns: "(distance|how far|travel time) .*(to|from|between)"
  patterns: "(geocode|coordinates|location) .*(of|for)"
  patterns: "(timezone|time zone) .*(of|for|at)"
  patterns: "(what|where) .*(near|around|close)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "python3"
}

provides {
  capabilities: "geocoding"
  capabilities: "reverse_geocoding"
  capabilities: "nearby_pois"
  capabilities: "distance_matrix"
  capabilities: "turn_by_turn_directions"
  capabilities: "timezone_lookup"
  capabilities: "bounding_box_search"
  output_types: ".json"
}

actions {
  id: "search"
  description: "Geocode a place name to coordinates"
  trigger_phrases: "find coordinates"
  trigger_phrases: "geocode"
  trigger_phrases: "where is"
  trigger_phrases: "location of"
    rules {
      text: "Returns lat, lon, display name, type, bounding box, importance score"
      priority: HIGH
    }
    rules {
      text: "Uses Nominatim (OpenStreetMap) — max 1 req/s handled automatically"
      priority: NORMAL
    }
    data {
      key: "command_pattern"
      string_value: "python3 $MAPS search \"PLACE NAME\""
    }
    examples {
      label: "geocode a landmark"
      language: "bash"
      code: "python3 $MAPS search \"Eiffel Tower\""
    }
}
actions {
  id: "reverse"
  description: "Convert coordinates to a street address"
  trigger_phrases: "reverse geocode"
  trigger_phrases: "address for coordinates"
  trigger_phrases: "what's at this location"
    rules {
      text: "Returns full address breakdown: street, city, state, country, postcode"
      priority: HIGH
    }
    data {
      key: "command_pattern"
      string_value: "python3 $MAPS reverse LAT LON"
    }
    examples {
      label: "reverse geocode coordinates"
      language: "bash"
      code: "python3 $MAPS reverse 48.8584 2.2945"
    }
}
actions {
  id: "nearby"
  description: "Find places by category near a location"
  trigger_phrases: "find nearby"
  trigger_phrases: "restaurants near"
  trigger_phrases: "hospitals around"
  trigger_phrases: "what's nearby"
  trigger_phrases: "nearby cafes"
    rules {
      text: "Accepts lat/lon OR --near \"address\" for auto-geocoding"
      priority: HIGH
    }
    rules {
      text: "Multiple --category flags merge into one query"
      priority: HIGH
    }
    rules {
      text: "Results include: name, address, lat/lon, distance_m, maps_url, directions_url, and tags (cuisine, hours, phone, website)"
      priority: NORMAL
    }
    data {
      key: "command_pattern"
      string_value: "python3 $MAPS nearby [LAT LON | --near \"PLACE\"] --category CAT [--radius M] [--limit N]"
    }
    data {
      key: "categories"
      list_value {
        items {
          string_value: "restaurant"
        }
        items {
          string_value: "cafe"
        }
        items {
          string_value: "bar"
        }
        items {
          string_value: "hospital"
        }
        items {
          string_value: "pharmacy"
        }
        items {
          string_value: "hotel"
        }
        items {
          string_value: "supermarket"
        }
        items {
          string_value: "atm"
        }
        items {
          string_value: "gas_station"
        }
        items {
          string_value: "museum"
        }
        items {
          string_value: "park"
        }
        items {
          string_value: "airport"
        }
        items {
          string_value: "train_station"
        }
      }
    }
    examples {
      label: "find restaurants near a landmark"
      language: "bash"
      code: "python3 $MAPS nearby --near \"Colosseum Rome\" --category restaurant --radius 500"
    }
    examples {
      label: "find cafes from coordinates"
      language: "bash"
      code: "python3 $MAPS nearby 48.8584 2.2945 cafe --limit 10"
    }
}
actions {
  id: "distance"
  description: "Calculate travel distance and time between two places"
  trigger_phrases: "how far"
  trigger_phrases: "distance to"
  trigger_phrases: "travel time"
  trigger_phrases: "how long to get"
    rules {
      text: "Modes: driving (default), walking, cycling"
      priority: HIGH
    }
    rules {
      text: "Returns road distance, duration, and straight-line distance"
      priority: HIGH
    }
    rules {
      text: "Uses OSRM — best coverage for Europe and North America"
      priority: NORMAL
    }
    data {
      key: "command_pattern"
      string_value: "python3 $MAPS distance \"ORIGIN\" --to \"DESTINATION\" [--mode driving|walking|cycling]"
    }
    examples {
      label: "driving distance between cities"
      language: "bash"
      code: "python3 $MAPS distance \"Paris\" --to \"Lyon\""
    }
}
actions {
  id: "directions"
  description: "Turn-by-turn navigation between two places"
  trigger_phrases: "directions to"
  trigger_phrases: "how to get to"
  trigger_phrases: "navigate to"
  trigger_phrases: "route to"
    rules {
      text: "Returns numbered steps with instruction, distance, duration, road name, and maneuver type"
      priority: HIGH
    }
    rules {
      text: "Modes: driving (default), walking, cycling"
      priority: NORMAL
    }
    data {
      key: "command_pattern"
      string_value: "python3 $MAPS directions \"ORIGIN\" --to \"DESTINATION\" [--mode walking|driving|cycling]"
    }
    examples {
      label: "walking directions"
      language: "bash"
      code: "python3 $MAPS directions \"Eiffel Tower\" --to \"Louvre Museum\" --mode walking"
    }
}
actions {
  id: "timezone"
  description: "Get timezone information for coordinates"
  trigger_phrases: "timezone for"
  trigger_phrases: "what time zone"
  trigger_phrases: "local time at"
    rules {
      text: "Returns timezone name, UTC offset, and current local time"
      priority: HIGH
    }
    data {
      key: "command_pattern"
      string_value: "python3 $MAPS timezone LAT LON"
    }
    examples {
      label: "timezone for Tokyo"
      language: "bash"
      code: "python3 $MAPS timezone 35.6762 139.6503"
    }
}
actions {
  id: "area_bbox"
  description: "Get bounding box for a place or search POIs within a bbox"
  trigger_phrases: "bounding box"
  trigger_phrases: "area of"
  trigger_phrases: "search within area"
    rules {
      text: "area returns bbox coordinates, width/height in km, approximate area"
      priority: HIGH
    }
    rules {
      text: "bbox finds POIs within a geographic rectangle — use area first to get coordinates"
      priority: HIGH
    }
    data {
      key: "command_patterns"
      map_value {
        entries {
          key: "area"
          string_value: "python3 $MAPS area \"PLACE NAME\""
        }
        entries {
          key: "bbox"
          string_value: "python3 $MAPS bbox SOUTH WEST NORTH EAST CATEGORY [--limit N]"
        }
      }
    }
    examples {
      label: "get area bounding box"
      language: "bash"
      code: "python3 $MAPS area \"Manhattan, New York\""
    }
    examples {
      label: "search within bounding box"
      language: "bash"
      code: "python3 $MAPS bbox 40.75 -74.00 40.77 -73.98 restaurant --limit 20"
    }
}

guardrails {
  text: "Nominatim rate limit: max 1 req/s — handled automatically by script"
  scope: ALWAYS
}

guardrails {
  text: "OSRM routing coverage best for Europe and North America"
  scope: ALWAYS
}

guardrails {
  text: "Overpass API can be slow during peak hours — script auto-falls back between mirrors"
  scope: ALWAYS
}

guardrails {
  text: "For 'open now?' questions, check hours field; if missing, verify with web_search since OSM hours are community-maintained"
  scope: READ_OPS
}

related {
  name: "find-nearby"
  relationship: "alternative_to"
  description: "Superseded by maps skill — all find-nearby features handled by nearby command"
}
