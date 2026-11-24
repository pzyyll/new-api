// Exa web search and page fetch for DeepSeek's Search and Fetch APIs. The
// channel key reaches Exa as x-api-key; the host handles channel selection,
// key rotation, retries and billing.

const SEARCH_PATH = "/v1/tools/search";
const FETCH_PATH = "/v1/tools/fetch";

const DEFAULT_COUNT = 10;
const MAX_CONTENT_CHARS = 10000;
const SNIPPET_CHARS = 200;

export const meta = {
  apiVersion: 1,
  key: "exa-web-tools",
  name: "Exa web tools",
  description: {
    en: "Web search and page fetch through Exa",
    zh: "通过 Exa 进行网页搜索与页面抓取",
  },
  version: "1.0.0",
  author: { name: "QuantumNous" },
  baseUrl: "https://api.exa.ai",
  relay: [
    {
      incoming_path: SEARCH_PATH,
      upstream_path: "/search",
      converter: "plugin",
      auth: { type: "header", name: "x-api-key", value: "{api_key}" },
    },
    {
      incoming_path: FETCH_PATH,
      upstream_path: "/contents",
      converter: "plugin",
      auth: { type: "header", name: "x-api-key", value: "{api_key}" },
    },
  ],
};

export function convertRequest(ctx, req) {
  const body = req.body || {};
  switch (ctx.path) {
    case SEARCH_PATH:
      return searchRequest(body);
    case FETCH_PATH:
      return fetchRequest(body);
    default:
      throw new Error("unsupported path " + ctx.path);
  }
}

export function convertResponse(ctx, res) {
  if (res.status < 200 || res.status >= 300) {
    const status = res.status >= 400 && res.status <= 599 ? res.status : 502;
    return { error: { status, message: "Exa request failed" } };
  }
  let payload;
  try {
    payload = JSON.parse(res.body);
  } catch {
    return { error: { status: 502, message: "Exa returned an unreadable response" } };
  }
  return ctx.path === FETCH_PATH ? fetchResponse(payload) : searchResponse(payload, ctx.state);
}

// Search and Fetch are answered in one JSON reply; no request is streamed.
export function convertStreamEvent() {
  return {};
}

function searchRequest(body) {
  const count = Number.isInteger(body.count) ? body.count : DEFAULT_COUNT;
  const contents = {
    highlights: true,
    summary: typeof body.ori_question === "string" && body.ori_question ? { query: body.ori_question } : true,
  };
  // The page text is only fetched when the client asks for it.
  if (body.need_content === true) {
    contents.text = { maxCharacters: MAX_CONTENT_CHARS };
  }
  const query = queryFilters(typeof body.query === "string" ? body.query : "");
  const upstream = { query: query.text, numResults: count, contents };
  if (query.includeDomains.length > 0) {
    upstream.includeDomains = query.includeDomains;
  }
  if (query.excludeDomains.length > 0) {
    upstream.excludeDomains = query.excludeDomains;
  }
  if (query.startPublishedDate) {
    upstream.startPublishedDate = query.startPublishedDate;
  }
  if (query.endPublishedDate) {
    upstream.endPublishedDate = query.endPublishedDate;
  }
  return { body: upstream, state: { limit: count, needContent: body.need_content === true } };
}

function fetchRequest(body) {
  return { body: { urls: [body.url], text: true } };
}

function searchResponse(payload, state) {
  if (!payload || !Array.isArray(payload.results)) {
    return { error: { status: 502, message: "Exa returned an unrecognized search response" } };
  }
  const limit = state && Number.isInteger(state.limit) ? state.limit : DEFAULT_COUNT;
  const withContent = Boolean(state && state.needContent === true);
  const results = [];
  for (const item of payload.results) {
    if (results.length >= limit) {
      break;
    }
    if (!item || !isHTTPURL(item.url)) {
      continue;
    }
    const summary = typeof item.summary === "string" ? item.summary : "";
    const highlight = Array.isArray(item.highlights) && typeof item.highlights[0] === "string" ? item.highlights[0] : "";
    results.push({
      rerank_score: relevanceScore(item.score),
      title: typeof item.title === "string" ? item.title : "",
      url: item.url,
      snippet: highlight || truncate(summary),
      summary,
      // The page text only counts as content when the client asked for it.
      content: withContent && typeof item.text === "string" ? item.text : "",
      site_name: null,
      site_icon: isHTTPURL(item.favicon) ? item.favicon : null,
      date: publishedDate(item.publishedDate),
      authority: -1,
    });
  }
  if (results.length === 0 && payload.results.length > 0) {
    return { error: { status: 502, message: "Exa returned no valid search results" } };
  }
  return { body: { results } };
}

function fetchResponse(payload) {
  if (!payload || !Array.isArray(payload.results)) {
    return { error: { status: 502, message: "Exa returned an unrecognized contents response" } };
  }
  const page = payload.results[0];
  const content = page && typeof page.text === "string" ? page.text : "";
  if (content) {
    return { body: { title: typeof page.title === "string" ? page.title : "", content } };
  }
  const status = Array.isArray(payload.statuses) ? payload.statuses[0] : null;
  if (status && isBlocked(status)) {
    return { error: { status: 403, message: "Exa is not allowed to fetch this page" } };
  }
  return { error: { status: 404, message: "Exa returned no readable content for this page" } };
}

// isBlocked reports whether Exa named a refusal of the target page.
function isBlocked(status) {
  if (status?.status !== "error") {
    return false;
  }
  return status.error?.httpStatusCode === 403 || status.error?.tag === "CRAWL_BLOCKED";
}

// relevanceScore maps a vendor relevance of 0 to 1 onto DeepSeek's 0 to 3.
// Without a comparable vendor score the result carries 0.
function relevanceScore(score) {
  if (typeof score !== "number" || !Number.isFinite(score)) {
    return 0;
  }
  return Math.round(Math.min(Math.max(score, 0), 1) * 3 * 1e6) / 1e6;
}

function publishedDate(value) {
  if (typeof value !== "string" || !value) {
    return null;
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return null;
  }
  return date.toISOString().replace(/\.\d{3}Z$/, "Z");
}

// HTTP_URL matches an absolute http or https URL with a host and no
// whitespace. The runtime offers no WHATWG URL parser, and this is all the
// result schema needs.
const HTTP_URL = /^https?:\/\/[^\s/?#]+[^\s]*$/i;

function isHTTPURL(value) {
  return typeof value === "string" && HTTP_URL.test(value);
}

function truncate(text) {
  const characters = Array.from(text);
  return characters.length <= SNIPPET_CHARS ? text : characters.slice(0, SNIPPET_CHARS).join("");
}

// queryFilters maps the unambiguous search operators DeepSeek documents onto
// Exa filters and returns the remaining query. An operator that cannot be
// translated stays in the query as text.
const DOMAIN = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$/;

function queryFilters(query) {
  const tokens = splitQuery(query);
  const kept = [];
  const includeDomains = [];
  const excludeDomains = [];
  let startPublishedDate = "";
  let endPublishedDate = "";
  let converted = false;

  for (let index = 0; index < tokens.length; index++) {
    const site = siteToken(tokens[index]);
    if (site) {
      const domains = [site.domain];
      let next = index;
      while (next + 2 < tokens.length && tokens[next + 1].toUpperCase() === "OR") {
        const partner = siteToken(tokens[next + 2]);
        if (!partner || partner.exclude !== site.exclude) {
          break;
        }
        domains.push(partner.domain);
        next += 2;
      }
      (site.exclude ? excludeDomains : includeDomains).push(...domains);
      index = next;
      converted = true;
      continue;
    }
    const after = dateToken(tokens[index], "after");
    if (after) {
      startPublishedDate = laterDate(startPublishedDate, isoDate(after));
      converted = true;
      continue;
    }
    const before = dateToken(tokens[index], "before");
    if (before) {
      endPublishedDate = earlierDate(endPublishedDate, isoDate(before));
      converted = true;
      continue;
    }
    kept.push(tokens[index]);
  }

  // A query made only of operators keeps its text: Exa needs a query, and a
  // partly translated one would change what was asked.
  if (!converted || kept.length === 0) {
    return { text: query, includeDomains: [], excludeDomains: [], startPublishedDate: "", endPublishedDate: "" };
  }
  return {
    text: kept.join(" "),
    includeDomains,
    excludeDomains,
    startPublishedDate,
    endPublishedDate,
  };
}

// splitQuery splits on whitespace outside quotes, so an operator inside a
// quoted phrase stays part of the query.
function splitQuery(query) {
  const tokens = [];
  let current = "";
  let quote = "";
  for (const character of query) {
    if (quote) {
      current += character;
      if (character === quote) {
        quote = "";
      }
      continue;
    }
    if (character === '"' || character === "'") {
      quote = character;
      current += character;
      continue;
    }
    if (/\s/.test(character)) {
      if (current) {
        tokens.push(current);
        current = "";
      }
      continue;
    }
    current += character;
  }
  if (current) {
    tokens.push(current);
  }
  return tokens;
}

function siteToken(token) {
  const exclude = token.startsWith("-");
  const value = exclude ? token.slice(1) : token;
  if (!value.toLowerCase().startsWith("site:")) {
    return null;
  }
  const domain = value.slice("site:".length).toLowerCase();
  if (!DOMAIN.test(domain)) {
    return null;
  }
  return { domain, exclude };
}

function dateToken(token, name) {
  const prefix = name + ":";
  if (!token.toLowerCase().startsWith(prefix)) {
    return null;
  }
  const date = token.slice(prefix.length);
  // A bare year names its first day in both date filters.
  const value = /^\d{4}$/.test(date) ? date + "-01-01" : date;
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) {
    return null;
  }
  const parsed = new Date(isoDate(value));
  if (Number.isNaN(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== value) {
    return null;
  }
  return value;
}

function isoDate(value) {
  return value + "T00:00:00.000Z";
}

function laterDate(current, candidate) {
  return !current || candidate > current ? candidate : current;
}

function earlierDate(current, candidate) {
  return !current || candidate < current ? candidate : current;
}
