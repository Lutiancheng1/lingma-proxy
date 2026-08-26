// cc-switch custom usage script for lingma-proxy: shows the QoderCN/Lingma
// credit quota from the proxy's /quota endpoint (real gateway numbers).
//
// In cc-switch (provider → usage query → template "custom"): base URL = your
// proxy, API key = your proxy inbound key, script = this whole file. cc-switch
// substitutes {{apiKey}}/{{baseUrl}} and runs the HTTP request itself (its JS
// sandbox has no network), passing the parsed JSON to extractor().
//
// /quota response: { user_type, unit, total, used, remaining, percentage,
//   is_exceeded, reset_at_ms, source }
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
