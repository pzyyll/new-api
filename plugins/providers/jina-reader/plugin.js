// Bridge DeepSeek Fetch to Jina Reader's single-URL read endpoint.
// The channel Base URL selects the Reader instance, not the requested URL.

export const meta = {
  apiVersion: 1,
  key: "jina-reader",
  name: "Jina Reader",
  description: {
    en: "Fetch web pages and PDF text through Jina Reader",
    zh: "通过 Jina Reader 抓取网页与 PDF 正文",
  },
  version: "1.0.2",
  author: { name: "QuantumNous" },
  baseUrl: "https://r.jina.ai",
  models: ["deepseek-fetch"],
  keyHint: { en: "Jina API key, proxy token, or placeholder for anonymous mode", zh: "Jina API 密钥、反代令牌或匿名模式占位值" },
  channelConfig: {
    type: "object",
    properties: {
      anonymous: {
        type: "boolean",
        title: { en: "Connect without authentication", zh: "无认证连接" },
        description: { en: "For a self-hosted Reader on a trusted network", zh: "用于可信网络内的自建 Reader" },
        default: false,
      },
    },
  },
  relay: [{ incoming_path: "/v1/tools/fetch", upstream_path: "/", converter: "plugin", auth: { type: "none" } }],
};

export function signRequest(ctx, request) {
  const authorization = request.headers.Authorization;
  if (ctx.config.anonymous === true) {
    if (authorization) {
      throw new Error("Remove the Authorization header override to use Jina Reader without authentication");
    }
    return {};
  }
  if (authorization) {
    return {};
  }
  return { headers: { Authorization: "Bearer " + ctx.apiKey } };
}

export function convertRequest(_ctx, req) {
  return {
    headers: { Accept: "application/json" },
    stream: false,
    body: { url: req.body.url },
  };
}

export function convertResponse(_ctx, res) {
  if (res.status < 200 || res.status >= 300) {
    const status = res.status >= 400 && res.status <= 599 ? res.status : 502;
    if (status === 422) {
      return { error: { status: 404, message: "Jina Reader could not extract content from this page" } };
    }
    if (status === 451) {
      return { error: { status: 403, message: "Jina Reader refused to fetch this page" } };
    }
    return { error: { status, message: "Jina Reader request failed" } };
  }

  let payload;
  try {
    payload = JSON.parse(res.body);
  } catch {
    return { error: { status: 502, message: "Jina Reader returned an unreadable response" } };
  }
  const page = payload?.data;
  if (!page || typeof page !== "object" || Array.isArray(page)) {
    return { error: { status: 502, message: "Jina Reader returned an unrecognized response" } };
  }

  // Reader can return HTTP 200 with the target's error page as its content.
  const targetStatus = typeof page.warning === "string" ? page.warning.match(/^Target URL returned error ([45]\d{2})(?=[:\s]|$)/m)?.[1] : undefined;
  if (targetStatus === "401" || targetStatus === "403" || targetStatus === "451") {
    return { error: { status: 403, message: "The target site refused the fetch" } };
  }
  if (targetStatus || typeof page.content !== "string" || !page.content.trim()) {
    return { error: { status: 404, message: "Jina Reader returned no readable content for this page" } };
  }
  return { body: { title: typeof page.title === "string" ? page.title : "", content: page.content } };
}

// This route only returns one JSON response.
export function convertStreamEvent() {
  return {};
}
