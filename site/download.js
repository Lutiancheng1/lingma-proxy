(function () {
  const statusEl = document.getElementById("download-status");
  const metaEl = document.getElementById("download-meta");
  const gridEl = document.getElementById("download-grid");
  const defaultUpdateManifestUrl =
    "https://pub-8b42d9b2b003480e9392e0123a37d52e.r2.dev/updates/feishu/stable/manifest.json";
  const config = window.LINGMA_SITE_CONFIG || {};

  const platforms = [
    {
      key: "darwin-arm64",
      title: "macOS Apple Silicon",
      detail: "DMG 安装包，下载后打开并覆盖安装。",
    },
    {
      key: "windows-amd64",
      title: "Windows x64",
      detail: "便携 exe，下载后手动替换旧版本。",
    },
  ];

  function formatSize(bytes) {
    if (!bytes || bytes <= 0) return "";
    const mb = bytes / 1024 / 1024;
    return `${mb.toFixed(mb >= 100 ? 0 : 1)} MB`;
  }

  function renderFallback(message) {
    statusEl.textContent = message;
    gridEl.innerHTML = "";
    metaEl.textContent = "";
  }

  function renderManifest(manifest) {
    statusEl.textContent = `最新版本：v${manifest.version || "未知"}`;
    metaEl.textContent = manifest.releaseNotes || "";
    gridEl.innerHTML = "";

    platforms.forEach((platform) => {
      const asset = manifest.assets && manifest.assets[platform.key];
      const card = document.createElement("article");
      card.className = "download-card";

      const title = document.createElement("h2");
      title.textContent = platform.title;
      card.appendChild(title);

      const detail = document.createElement("p");
      detail.textContent = platform.detail;
      card.appendChild(detail);

      if (asset && asset.url) {
        const link = document.createElement("a");
        link.className = "button";
        link.href = asset.url;
        link.textContent = "下载安装包";
        card.appendChild(link);

        const info = document.createElement("p");
        info.className = "asset-info";
        info.textContent = [asset.filename, formatSize(asset.size), asset.kind].filter(Boolean).join(" · ");
        card.appendChild(info);
      } else {
        const unavailable = document.createElement("p");
        unavailable.className = "muted";
        unavailable.textContent = "当前 manifest 未提供该平台包。";
        card.appendChild(unavailable);
      }

      gridEl.appendChild(card);
    });
  }

  async function main() {
    const updateManifestUrl = config.updateManifestUrl || defaultUpdateManifestUrl;
    if (!updateManifestUrl) {
      renderFallback("还没有配置 OTA manifest 地址。发布 Action 成功后，这里会自动显示最新下载链接。");
      return;
    }

    try {
      const response = await fetch(updateManifestUrl, { cache: "no-store" });
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }
      renderManifest(await response.json());
    } catch (error) {
      renderFallback(`读取最新版本失败：${error.message}`);
    }
  }

  main();
})();
