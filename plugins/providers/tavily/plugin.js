// Tavily web search and page fetch for DeepSeek's Search and Fetch APIs. The
// channel key reaches Tavily as a Bearer token; the host handles channel
// selection, key rotation, retries and billing.

const SEARCH_PATH = "/v1/tools/search";
const FETCH_PATH = "/v1/tools/fetch";

const DEFAULT_COUNT = 10;
const MAX_SEARCH_RESULTS = 20;
const SNIPPET_CHARS = 200;

export const meta = {
  apiVersion: 1,
  key: "tavily-web-tools",
  name: "Tavily web tools",
  description: {
    en: "Web search and page fetch through Tavily",
    zh: "通过 Tavily 进行网页搜索与页面抓取",
  },
  version: "1.0.0",
  author: { name: "QuantumNous" },
  baseUrl: "https://api.tavily.com",
  relay: [
    { incoming_path: SEARCH_PATH, upstream_path: "/search", converter: "plugin" },
    { incoming_path: FETCH_PATH, upstream_path: "/extract", converter: "plugin" },
  ],
};

export function convertRequest(ctx, req) {
  const body = req.body || {};
  switch (ctx.path) {
    case SEARCH_PATH:
      return searchRequest(body);
    case FETCH_PATH:
      return { body: { urls: [body.url], format: "markdown" } };
    default:
      throw new Error("unsupported path " + ctx.path);
  }
}

export function convertResponse(ctx, res) {
  if (res.status < 200 || res.status >= 300) {
    // Tavily reports an exhausted plan as 432 or 433; the client sees a payment problem.
    let status = res.status;
    if (status === 432 || status === 433) {
      status = 402;
    } else if (status < 400 || status > 599) {
      status = 502;
    }
    return { error: { status, message: "Tavily request failed" } };
  }
  let payload;
  try {
    payload = JSON.parse(res.body);
  } catch {
    return { error: { status: 502, message: "Tavily returned an unreadable response" } };
  }
  return ctx.path === FETCH_PATH ? fetchResponse(payload) : searchResponse(payload, ctx.state);
}

// Search and Fetch are answered in one JSON reply; no request is streamed.
export function convertStreamEvent() {
  return {};
}

function searchRequest(body) {
  const count = Number.isInteger(body.count) ? body.count : DEFAULT_COUNT;
  const filters = queryFilters(typeof body.query === "string" ? body.query : "");
  const upstream = {
    query: filters.text,
    // Tavily returns at most 20 results per request; asking for fewer is
    // never filled with another request.
    max_results: Math.min(count, MAX_SEARCH_RESULTS),
    include_raw_content: body.need_content === true ? "markdown" : false,
    include_favicon: true,
    include_published_date: true,
  };
  if (filters.includeDomains.length > 0) {
    upstream.include_domains = filters.includeDomains;
  }
  if (filters.excludeDomains.length > 0) {
    upstream.exclude_domains = filters.excludeDomains;
  }
  if (filters.after) {
    upstream.start_date = filters.after;
  }
  if (filters.before) {
    upstream.end_date = filters.before;
  }
  if (filters.after || filters.before) {
    upstream.filter_by_published_date = true;
  }
  return { body: upstream, state: { limit: Math.min(count, MAX_SEARCH_RESULTS), needContent: body.need_content === true } };
}

function searchResponse(payload, state) {
  if (!payload || !Array.isArray(payload.results)) {
    return { error: { status: 502, message: "Tavily returned an unrecognized search response" } };
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
    const content = typeof item.content === "string" ? item.content : "";
    results.push({
      rerank_score: relevanceScore(item.score),
      title: typeof item.title === "string" ? item.title : "",
      url: item.url,
      snippet: truncate(content),
      summary: content,
      // The page text only counts as content when the client asked for it.
      content: withContent && typeof item.raw_content === "string" ? item.raw_content : "",
      site_name: null,
      site_icon: isHTTPURL(item.favicon) ? item.favicon : null,
      date: publishedDate(item.published_date),
      authority: -1,
    });
  }
  if (results.length === 0 && payload.results.length > 0) {
    return { error: { status: 502, message: "Tavily returned no valid search results" } };
  }
  return { body: { results } };
}

function fetchResponse(payload) {
  if (!payload || (!Array.isArray(payload.results) && !Array.isArray(payload.failed_results))) {
    return { error: { status: 502, message: "Tavily returned an unrecognized extract response" } };
  }
  const page = Array.isArray(payload.results) ? payload.results[0] : null;
  const content = page && typeof page.raw_content === "string" ? page.raw_content : "";
  if (content) {
    return { body: { title: "", content } };
  }
  if (blockedFailure(payload.failed_results)) {
    return { error: { status: 403, message: "Tavily is not allowed to fetch this page" } };
  }
  return { error: { status: 404, message: "Tavily returned no readable content for this page" } };
}

// blockedFailure reports whether Tavily named a refusal of the target page.
// Any other failure means the page could not be read, which is a missing page.
function blockedFailure(failures) {
  if (!Array.isArray(failures)) {
    return false;
  }
  return failures.some((failure) => typeof failure?.error === "string" && /blocked|forbidden|policy|disallow/i.test(failure.error));
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
// Tavily filters and returns the remaining query. An operator that cannot be
// translated stays in the query as text.
const DOMAIN = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$/;

function queryFilters(query) {
  const tokens = splitQuery(query);
  const kept = [];
  const includeDomains = [];
  const excludeDomains = [];
  let after = "";
  let before = "";
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
    const start = dateToken(tokens[index], "after");
    if (start) {
      after = laterDate(after, start);
      converted = true;
      continue;
    }
    const end = dateToken(tokens[index], "before");
    if (end) {
      before = earlierDate(before, end);
      converted = true;
      continue;
    }
    kept.push(tokens[index]);
  }

  // A query made only of operators keeps its text: Tavily needs a query, and
  // a partly translated one would change what was asked.
  if (!converted || kept.length === 0) {
    return { text: query, includeDomains: [], excludeDomains: [], after: "", before: "" };
  }
  return { text: kept.join(" "), includeDomains, excludeDomains, after, before };
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
  const parsed = new Date(value + "T00:00:00Z");
  if (Number.isNaN(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== value) {
    return null;
  }
  return value;
}

function laterDate(current, candidate) {
  return !current || candidate > current ? candidate : current;
}

function earlierDate(current, candidate) {
  return !current || candidate < current ? candidate : current;
}
