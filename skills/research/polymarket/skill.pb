meta {
  name: "polymarket"
  version: "1.0.0"
  summary: "Query Polymarket prediction markets: prices, orderbooks, history"
  author: "Ion Agent + Teknium"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "polymarket"
  keywords: "prediction market"
  keywords: "betting odds"
  keywords: "event probability"
  keywords: "market prices"
  intents: "query_polymarket"
  intents: "get_market_prices"
  intents: "search_markets"
  intents: "get_price_history"
  patterns: "(what are the odds|probability|likelihood) .*(of|that|for)"
  patterns: "(polymarket|prediction market) .*(price|odds|market)"
  patterns: "(search|find|browse) .*(polymarket|prediction)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "curl"
  binaries: "python3"
}

provides {
  capabilities: "polymarket_search"
  capabilities: "market_prices"
  capabilities: "orderbook_data"
  capabilities: "price_history"
}

actions {
  id: "search_markets"
  description: "Search Polymarket for markets matching a query"
  trigger_phrases: "search polymarket"
  trigger_phrases: "find prediction markets"
  trigger_phrases: "polymarket search"
    rules {
      text: "All endpoints are read-only and require zero authentication"
      priority: CRITICAL
    }
    rules {
      text: "Use Gamma API public-search endpoint with user's query"
      priority: HIGH
    }
    rules {
      text: "Parse response: extract events and their nested markets"
      priority: HIGH
    }
    rules {
      text: "Present: market question, current prices as percentages, volume"
      priority: NORMAL
    }
    data {
      key: "gamma_api_base"
      string_value: "https://gamma-api.polymarket.com"
    }
    data {
      key: "clob_api_base"
      string_value: "https://clob.polymarket.com"
    }
    data {
      key: "data_api_base"
      string_value: "https://data-api.polymarket.com"
    }
}
actions {
  id: "get_market_prices"
  description: "Get current prices and market details"
  trigger_phrases: "polymarket prices"
  trigger_phrases: "market prices"
  trigger_phrases: "current odds"
    rules {
      text: "outcomePrices field is JSON-encoded array like [\"0.80\", \"0.20\"] — prices ARE probabilities (0.65 = 65%)"
      priority: CRITICAL
    }
    rules {
      text: "Format prices as percentages for readability: outcomePrices [\"0.652\", \"0.348\"] → 'Yes: 65.2%, No: 34.8%'"
      priority: HIGH
    }
    rules {
      text: "Always show market question and probability, include volume when available"
      priority: HIGH
    }
    rules {
      text: "clobTokenIds: JSON-encoded array of two token IDs [Yes, No] for price/book queries"
      priority: NORMAL
    }
    rules {
      text: "conditionId: hex string used for price history queries"
      priority: NORMAL
    }
    examples {
      label: "present market result"
      language: "text"
      code: "\"Will X happen?\" — 65.2% Yes ($1.2M volume)"
    }
}
actions {
  id: "get_price_history"
  description: "Get historical price data for a market"
  trigger_phrases: "price history"
  trigger_phrases: "historical odds"
  trigger_phrases: "market history"
    rules {
      text: "Use conditionId from market data for price history queries via CLOB API"
      priority: HIGH
    }
    rules {
      text: "Some new markets may have empty price history"
      priority: NORMAL
    }
}
actions {
  id: "parse_double_encoded_fields"
  description: "Handle double-encoded JSON fields in Gamma API responses"
  trigger_phrases: "parse polymarket response"
  trigger_phrases: "decode polymarket data"
    rules {
      text: "Gamma API returns outcomePrices, outcomes, clobTokenIds as JSON strings inside JSON (double-encoded)"
      priority: HIGH
    }
    rules {
      text: "Parse with json.loads(market['outcomePrices']) to get actual array"
      priority: HIGH
    }
    examples {
      label: "python parsing"
      language: "python"
      code: "import json\nprices = json.loads(market['outcomePrices'])\n# prices is now [\"0.652\", \"0.348\"]\nyes_pct = float(prices[0]) * 100"
    }
}

guardrails {
  text: "This skill is read-only — it does not support placing trades"
  scope: ALWAYS
}

guardrails {
  text: "Trading requires wallet-based crypto authentication (EIP-712 signatures)"
  scope: ALWAYS
}

guardrails {
  text: "Geographic restrictions apply to trading but read-only data is globally accessible"
  scope: ALWAYS
}
