// cc-switch custom usage script for lingma-proxy.
//
// Shows the QoderCN/Lingma account credit quota in cc-switch's usage display
// by calling the proxy's /quota endpoint (real numbers from the gateway's
// openapi, never estimated).
//
// How to use (cc-switch → provider → usage query → template: "custom"):
//   - base URL  : your proxy, e.g. http://127.0.0.1:8095
//   - API key   : your proxy inbound key (the one in auth-keys.txt)
//   - script    : paste this whole file
//
// cc-switch substitutes {{apiKey}} / {{baseUrl}} before running the script,
// then performs the HTTP request itself (the JS sandbox has no network) and
// passes the parsed JSON response to extractor(). The request is same-origin
// with base URL and loopback, so it passes cc-switch's URL safety checks.
//
// /quota response shape:
//   { user_type, unit, total, used, remaining, percentage, is_exceeded,
//     reset_at_ms, source }
({
  request: {
    url: "{{baseUrl}}".replace(/\/+$/, "") + "/quota",
    method: "GET",
    // The proxy accepts either x-api-key or Authorization: Bearer.
    headers: { "x-api-key": "{{apiKey}}" },
  },
  extractor: function (r) {
    if (!r || typeof r !== "object") {
      return { isValid: false, invalidMessage: "no quota data returned" };
    }
    var num = function (v) {
      return typeof v === "number" && isFinite(v) ? v : null;
    };
    var total = num(r.total);
    var used = num(r.used);
    var remaining = num(r.remaining);

    // Derive a used-% locally rather than trusting r.percentage's basis.
    var extra = null;
    if (total && used != null) {
      extra = "used " + Math.round((used / total) * 1000) / 10 + "%";
    }
    if (typeof r.reset_at_ms === "number" && r.reset_at_ms > 0) {
      var day = new Date(r.reset_at_ms).toISOString().slice(0, 10);
      extra = (extra ? extra + " · " : "") + "resets " + day;
    }

    return {
      isValid: r.is_exceeded ? false : true,
      invalidMessage: r.is_exceeded ? "quota exceeded" : null,
      planName: typeof r.user_type === "string" && r.user_type ? r.user_type : "lingma",
      unit: typeof r.unit === "string" && r.unit ? r.unit : "credits",
      total: total,
      used: used,
      remaining: remaining,
      extra: extra,
    };
  },
})
