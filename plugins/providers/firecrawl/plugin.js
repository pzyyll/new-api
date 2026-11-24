// Bridge DeepSeek Fetch to the shared Firecrawl Cloud and self-hosted Scrape API.

export const meta = {
  apiVersion: 1,
  key: "firecrawl",
  name: "Firecrawl",
  description: {
    en: "Fetch web pages as Markdown through Firecrawl",
    zh: "通过 Firecrawl 抓取网页并返回 Markdown",
  },
  version: "1.0.0",
  author: { name: "QuantumNous" },
  baseUrl: "https://api.firecrawl.dev",
  models: ["deepseek-fetch"],
  keyHint: { en: "Firecrawl API key or reverse proxy token", zh: "Firecrawl API 密钥或反向代理令牌" },
  relay: [{ incoming_path: "/v1/tools/fetch", upstream_path: "/v2/scrape", converter: "plugin" }],
};

export function convertRequest(_ctx, req) {
  return {
    stream: false,
    body: { url: req.body.url, formats: ["markdown"], onlyMainContent: true },
  };
}

export function convertResponse(_ctx, res) {
  if (res.status < 200 || res.status >= 300) {
    const status = res.status >= 400 && res.status <= 599 ? res.status : 502;
    return { error: { status, message: "Firecrawl request failed" } };
  }

  let payload;
  try {
    payload = JSON.parse(res.body);
  } catch {
    return { error: { status: 502, message: "Firecrawl returned an unreadable response" } };
  }
  if (payload?.success !== true) {
    return { error: { status: 502, message: "Firecrawl did not complete the scrape" } };
  }

  const page = payload.data;
  // Firecrawl can include an error page in a successful API response.
  const targetStatus = page?.metadata?.statusCode;
  if (targetStatus === 401 || targetStatus === 403 || targetStatus === 451) {
    return { error: { status: 403, message: "The target site refused the fetch" } };
  }
  if (Number.isInteger(targetStatus) && targetStatus >= 400 && targetStatus <= 599) {
    return { error: { status: 404, message: "Firecrawl returned no readable content for this page" } };
  }
  if (typeof page?.markdown !== "string") {
    return { error: { status: 502, message: "Firecrawl returned an unrecognized Markdown response" } };
  }
  if (!page.markdown.trim()) {
    return { error: { status: 404, message: "Firecrawl returned no readable content for this page" } };
  }
  return { body: { title: typeof page.metadata?.title === "string" ? page.metadata.title : "", content: page.markdown } };
}

// Scrape returns a single JSON response.
export function convertStreamEvent() {
  return {};
}
