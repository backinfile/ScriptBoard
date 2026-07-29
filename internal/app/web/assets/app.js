(() => {
  "use strict";

  const iconPaths = {
    "activity": '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>',
    "app-window": '<rect width="20" height="16" x="2" y="4" rx="2"/><path d="M2 8h20"/><path d="M6 6h.01"/><path d="M10 6h.01"/>',
    "arrow-down": '<path d="m6 9 6 6 6-6"/><path d="M12 3v12"/>',
    "arrow-left": '<path d="m12 19-7-7 7-7"/><path d="M19 12H5"/>',
    "arrow-right": '<path d="m12 5 7 7-7 7"/><path d="M5 12h14"/>',
    "arrow-up": '<path d="m18 15-6-6-6 6"/><path d="M12 21V9"/>',
    "bookmark-plus": '<path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h5"/><path d="M19 3v6"/><path d="M16 6h6"/>',
    "box": '<path d="m21 8-9-5-9 5 9 5 9-5Z"/><path d="m3 8 9 5 9-5"/><path d="M3 8v8l9 5 9-5V8"/><path d="M12 13v8"/>',
    "braces": '<path d="M8 3H7a2 2 0 0 0-2 2v5a2 2 0 0 1-2 2 2 2 0 0 1 2 2v5c0 1.1.9 2 2 2h1"/><path d="M16 21h1a2 2 0 0 0 2-2v-5c0-1.1.9-2 2-2a2 2 0 0 1-2-2V5a2 2 0 0 0-2-2h-1"/>',
    "calendar-clock": '<path d="M16 2v4"/><path d="M3 10h18"/><path d="M8 2v4"/><rect x="3" y="4" width="18" height="18" rx="2"/><path d="M17 14v2l1 1"/>',
    "calendar-plus": '<path d="M16 2v4"/><path d="M3 10h18"/><path d="M8 2v4"/><rect x="3" y="4" width="18" height="18" rx="2"/><path d="M12 14v4"/><path d="M10 16h4"/>',
    "check": '<path d="m20 6-11 11-5-5"/>',
    "chevrons-up-down": '<path d="m7 15 5 5 5-5"/><path d="m7 9 5-5 5 5"/>',
    "chevron-right": '<path d="m9 18 6-6-6-6"/>',
    "circle-user-round": '<path d="M18 20a6 6 0 0 0-12 0"/><circle cx="12" cy="10" r="4"/><circle cx="12" cy="12" r="10"/>',
    "circle-x": '<circle cx="12" cy="12" r="10"/><path d="m15 9-6 6"/><path d="m9 9 6 6"/>',
    "clock-alert": '<circle cx="12" cy="13" r="8"/><path d="M12 9v4"/><path d="M12 17h.01"/><path d="M5 3 2 6"/><path d="m22 6-3-3"/>',
    "copy": '<rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>',
    "cpu": '<rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><path d="M9 1v3M15 1v3M9 20v3M15 20v3M20 9h3M20 14h3M1 9h3M1 14h3"/>',
    "database": '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14c0 1.7 4 3 9 3s9-1.3 9-3V5"/><path d="M3 12c0 1.7 4 3 9 3s9-1.3 9-3"/>',
    "download": '<path d="M12 3v12"/><path d="m7 10 5 5 5-5"/><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>',
    "ellipsis": '<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>',
    "eye": '<path d="M2.1 12a10.7 10.7 0 0 1 19.8 0 10.7 10.7 0 0 1-19.8 0"/><circle cx="12" cy="12" r="3"/>',
    "eye-off": '<path d="m2 2 20 20"/><path d="M6.7 6.7A11.7 11.7 0 0 0 2.1 12a10.7 10.7 0 0 0 14.1 5.2"/><path d="M10.7 10.7a3 3 0 0 0 4.2 4.2"/><path d="M14.3 5.2A10.7 10.7 0 0 1 21.9 12a11.8 11.8 0 0 1-2.2 3.2"/>',
    "file": '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/>',
    "file-cog": '<path d="M14 2H6a2 2 0 0 0-2 2v8"/><path d="M14 2v6h6"/><circle cx="13" cy="18" r="3"/><path d="m15.6 16.5.9-.5M10.4 19.5l-.9.5M15.6 19.5l.9.5M10.4 16.5l-.9-.5"/>',
    "file-lock-2": '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h7"/><path d="M14 2v6h6"/><rect x="14" y="15" width="8" height="6" rx="1"/><path d="M16 15v-2a2 2 0 0 1 4 0v2"/>',
    "file-terminal": '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/><path d="m8 13 2 2-2 2"/><path d="M12 17h4"/>',
    "folder": '<path d="M3 6h5l2 2h11v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
    "folder-code": '<path d="M10 10.5 8 12l2 1.5M14 10.5l2 1.5-2 1.5"/><path d="M2 6h5l2 2h13"/><path d="M2 6v12a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V8"/>',
    "folder-open": '<path d="M6 14l1.5-3h13l-2 7a2 2 0 0 1-2 1H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2v1"/>',
    "folder-plus": '<path d="M12 10v6M9 13h6"/><path d="M3 6h5l2 2h11v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
    "git-branch": '<line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>',
    "git-commit-horizontal": '<circle cx="12" cy="12" r="3"/><line x1="3" x2="9" y1="12" y2="12"/><line x1="15" x2="21" y1="12" y2="12"/>',
    "hard-drive": '<path d="M5.5 5.1 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.8 4H7.2a2 2 0 0 0-1.7 1.1z"/><path d="M2 12h20"/><path d="M6 16h.01M10 16h.01"/>',
    "image": '<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3-3a2 2 0 0 0-3 0l-9 9"/>',
    "info": '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>',
    "key-round": '<path d="M2.6 16.8a6 6 0 1 0 8.6-8.3A6 6 0 0 0 2.6 16.8"/><path d="m15 9 6-6M17 5l2 2M14 8l2 2"/>',
    "languages": '<path d="m5 8 6 6"/><path d="m4 14 6-6 2-3"/><path d="M2 5h12"/><path d="M7 2h1"/><path d="m22 22-5-10-5 10"/><path d="M14 18h6"/>',
    "loader-circle": '<path d="M21 12a9 9 0 1 1-6.2-8.6"/>',
    "lock": '<rect width="18" height="11" x="3" y="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
    "log-out": '<path d="M10 17l5-5-5-5"/><path d="M15 12H3"/><path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"/>',
    "memory-stick": '<path d="M6 19v-3M10 19v-3M14 19v-3M18 19v-3M8 11V9M16 11V9"/><rect x="2" y="5" width="20" height="11" rx="2"/>',
    "network": '<rect x="16" y="16" width="6" height="6" rx="1"/><rect x="2" y="16" width="6" height="6" rx="1"/><rect x="9" y="2" width="6" height="6" rx="1"/><path d="M5 16v-3h14v3M12 12V8"/>',
    "panel-left-open": '<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/><path d="m14 9 3 3-3 3"/>',
    "pause": '<rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/>',
    "pin": '<path d="M12 17v5"/><path d="M5 17h14"/><path d="M15 3.5c0 2 1 3.5 2 4.5H7c1-1 2-2.5 2-4.5"/><path d="M9 3h6"/>',
    "pin-off": '<path d="M12 17v5"/><path d="M5 17h12"/><path d="M4 4 20 20"/><path d="M9 3h6"/><path d="M15 3.5c0 2 1 3.5 2 4.5h-4"/><path d="M7 8c.7-.7 1.4-1.8 1.8-3"/>',
    "play": '<path d="m6 3 14 9-14 9z"/>',
    "plus": '<path d="M5 12h14M12 5v14"/>',
    "power": '<path d="M12 2v10"/><path d="M18.4 6.6a9 9 0 1 1-12.8 0"/>',
    "refresh-cw": '<path d="M21 12a9 9 0 0 0-15.2-6.5L3 8"/><path d="M3 3v5h5"/><path d="M3 12a9 9 0 0 0 15.2 6.5L21 16"/><path d="M16 16h5v5"/>',
    "rotate-ccw": '<path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/>',
    "scroll-text": '<path d="M15 12h-5M15 8h-5"/><path d="M19 17V5a2 2 0 0 0-2-2H4"/><path d="M8 21h12a2 2 0 0 0 2-2v-1H11v1a2 2 0 1 1-4 0V5a2 2 0 1 0-4 0v2h4"/>',
    "search": '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
    "settings": '<path d="M12.2 2h-.4a2 2 0 0 0-2 2v.2a2 2 0 0 1-1 1.7l-.4.2a2 2 0 0 1-2 0l-.1-.1a2 2 0 0 0-2.7.7l-.2.4a2 2 0 0 0 .7 2.7l.1.1a2 2 0 0 1 1 1.7v.5a2 2 0 0 1-1 1.7l-.1.1a2 2 0 0 0-.7 2.7l.2.4a2 2 0 0 0 2.7.7l.1-.1a2 2 0 0 1 2 0l.4.2a2 2 0 0 1 1 1.7v.2a2 2 0 0 0 2 2h.4a2 2 0 0 0 2-2v-.2a2 2 0 0 1 1-1.7l.4-.2a2 2 0 0 1 2 0l.1.1a2 2 0 0 0 2.7-.7l.2-.4a2 2 0 0 0-.7-2.7l-.1-.1a2 2 0 0 1-1-1.7v-.5a2 2 0 0 1 1-1.7l.1-.1a2 2 0 0 0 .7-2.7l-.2-.4a2 2 0 0 0-2.7-.7l-.1.1a2 2 0 0 1-2 0l-.4-.2a2 2 0 0 1-1-1.7V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>',
    "shield-check": '<path d="M20 13c0 5-3.5 7.5-8 9-4.5-1.5-8-4-8-9V5l8-3 8 3z"/><path d="m9 12 2 2 4-4"/>',
    "shield-alert": '<path d="M20 13c0 5-3.5 7.5-8 9-4.5-1.5-8-4-8-9V5l8-3 8 3z"/><path d="M12 8v4"/><path d="M12 16h.01"/>',
    "square": '<rect x="3" y="3" width="18" height="18" rx="2"/>',
    "square-pen": '<path d="M12 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.4 2.6a2.1 2.1 0 0 1 3 3L12 15l-4 1 1-4z"/>',
    "square-terminal": '<path d="m7 7 3 3-3 3M13 13h4"/><rect x="3" y="3" width="18" height="18" rx="2"/>',
    "trash-2": '<path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6M10 11v5M14 11v5"/>',
    "triangle-alert": '<path d="m21.7 18-8-14a2 2 0 0 0-3.4 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.7-3z"/><path d="M12 9v4M12 17h.01"/>',
    "unlock": '<rect width="18" height="11" x="3" y="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 9.9-1"/>',
    "upload": '<path d="M12 3v12M17 8l-5-5-5 5"/><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>',
    "user-round": '<circle cx="12" cy="8" r="5"/><path d="M20 21a8 8 0 0 0-16 0"/>',
    "x": '<path d="M18 6 6 18M6 6l12 12"/>',
    "zap": '<path d="M4 14a1 1 0 0 1-.8-1.6l9-11a.5.5 0 0 1 .9.4L12 8.5A1 1 0 0 0 13 10h7a1 1 0 0 1 .8 1.6l-9 11a.5.5 0 0 1-.9-.4L12 15.5A1 1 0 0 0 11 14z"/>'
  };

  const appScript = document.currentScript || document.querySelector('script[src*="/assets/app-v2.js"]');
  const appAssetVersion = appScript ? new URL(appScript.src, location.href).searchParams.get("v") : "";
  const versionedAssetURL = path => appAssetVersion
    ? `${path}?v=${encodeURIComponent(appAssetVersion)}`
    : path;

  let cleanupPage = () => {};
  let markdownLibrariesPromise = null;
  const scriptAssetPromises = new Map();
  let navigationBusy = false;
  let navigationSequence = 0;
  let navigationRequest = null;
  let taskPanelState = null;
  let taskPanelRequest = null;

  const locale = () => document.documentElement.lang === "zh-CN" ? "zh-CN" : "en-US";
  const copy = {
    "zh-CN": {
      current: "数据正常", attention: "需要关注", stale: "数据已过期",
      activeRuns: "个活动 Run", processing: "处理中…", complete: "实时输出已结束",
      connected: "实时输出已连接", disconnected: "实时输出连接中断",
      loading: "加载中…", loadFailed: "页面加载失败", retry: "重试",
      submitFailed: "操作未完成，请检查网络后重试。为避免重复操作，ScriptBoard 不会自动再次提交。",
      websiteNormal: "网站监控正常", websiteNoOpenIssues: "没有故障或待复核项",
      websiteDownOne: "个网站故障", websiteDownMany: "个网站故障",
      websiteVerifyingOne: "个正在复核", websiteVerifyingMany: "个正在复核",
      websiteNoConfirmedFailure: "没有已确认的网站故障",
      statuses: {
        starting: "正在启动", running: "运行中", stopping: "正在停止", timing_out: "正在超时终止",
        succeeded: "成功", failed: "失败", stopped: "已停止", timed_out: "已超时", rejected: "已拒绝"
      }
    },
    "en-US": {
      current: "Data current", attention: "Attention needed", stale: "Data stale",
      activeRuns: "active Runs", processing: "Processing…", complete: "Live output complete",
      connected: "Live output connected", disconnected: "Live output disconnected",
      loading: "Loading…", loadFailed: "Unable to load this page", retry: "Retry",
      submitFailed: "The action did not complete. Check your connection and retry. ScriptBoard will not resubmit it automatically.",
      websiteNormal: "Website monitoring normal", websiteNoOpenIssues: "No failures or pending verifications",
      websiteDownOne: "website down", websiteDownMany: "websites down",
      websiteVerifyingOne: "website under verification", websiteVerifyingMany: "websites under verification",
      websiteNoConfirmedFailure: "No confirmed website failures",
      statuses: {
        starting: "Starting", running: "Running", stopping: "Stopping", timing_out: "Timing out",
        succeeded: "Succeeded", failed: "Failed", stopped: "Stopped", timed_out: "Timed out", rejected: "Rejected"
      }
    }
  };
  const words = () => copy[locale()];

  function makeIcon(name) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    svg.classList.add("lucide", `lucide-${name}`);
    svg.innerHTML = iconPaths[name] || iconPaths.activity;
    return svg;
  }

  function renderIcons(root = document) {
    root.querySelectorAll("[data-lucide]:not([data-icon-ready])").forEach(target => {
      target.prepend(makeIcon(target.dataset.lucide));
      target.dataset.iconReady = "";
    });
  }

  function localizeTimes(root = document) {
    const formatter = new Intl.DateTimeFormat(locale(), {
      year: "numeric", month: "short", day: "2-digit", hour: "2-digit", minute: "2-digit"
    });
    root.querySelectorAll("time[data-local-time][datetime]").forEach(element => {
      const value = new Date(element.dateTime);
      if (!Number.isNaN(value.valueOf())) element.textContent = formatter.format(value);
    });
  }

  function loadScriptAsset(path, ready) {
    const current = ready();
    if (current) return Promise.resolve(current);
    if (scriptAssetPromises.has(path)) return scriptAssetPromises.get(path);
    const promise = new Promise((resolve, reject) => {
      const source = versionedAssetURL(path);
      const existing = Array.from(document.scripts).find(script => {
        try {
          return new URL(script.src, location.href).pathname === path;
        } catch {
          return false;
        }
      });
      const script = existing || document.createElement("script");
      const finish = () => {
        const loaded = ready();
        if (loaded) resolve(loaded);
        else reject(new Error(`${path} did not initialize`));
      };
      script.addEventListener("load", finish, { once: true });
      script.addEventListener("error", () => reject(new Error(`Unable to load ${path}`)), { once: true });
      if (!existing) {
        script.src = source;
        script.async = true;
        script.dataset.scriptboardAsset = path;
        document.head.append(script);
      }
    });
    scriptAssetPromises.set(path, promise);
    return promise;
  }

  function loadMarkdownLibraries() {
    if (!markdownLibrariesPromise) {
      markdownLibrariesPromise = Promise.all([
        loadScriptAsset("/assets/markdown-it.min.js", () => window.markdownit),
        loadScriptAsset("/assets/purify.min.js", () => window.DOMPurify)
      ]);
    }
    return markdownLibrariesPromise;
  }

  function supplementalHighlightAsset(language) {
    switch (language.toLowerCase()) {
      case "powershell":
      case "ps1":
        return { language: "powershell", path: "/assets/highlight-powershell.min.js" };
      case "dos":
      case "bat":
      case "batch":
      case "cmd":
        return { language: "dos", path: "/assets/highlight-dos.min.js" };
      default:
        return null;
    }
  }

  async function loadHighlightLanguages(languages) {
    const [highlighter] = await Promise.all([
      loadScriptAsset("/assets/highlight.min.js", () => window.hljs),
      loadScriptAsset("/assets/purify.min.js", () => window.DOMPurify)
    ]);
    const supplemental = new Map();
    languages.forEach(language => {
      if (highlighter.getLanguage(language)) return;
      const asset = supplementalHighlightAsset(language);
      if (asset) supplemental.set(asset.path, asset.language);
    });
    await Promise.all(Array.from(supplemental, ([path, language]) =>
      loadScriptAsset(path, () => window.hljs?.getLanguage(language))));
    return highlighter;
  }

  function sanitizedHighlight(source, language) {
    if (!window.hljs?.getLanguage(language)) return null;
    const highlighted = window.hljs.highlight(source, {
      language,
      ignoreIllegals: true
    });
    return window.DOMPurify.sanitize(highlighted.value, {
      ALLOWED_TAGS: ["span"],
      ALLOWED_ATTR: ["class"],
      ALLOW_ARIA_ATTR: false,
      ALLOW_DATA_ATTR: false,
      RETURN_DOM_FRAGMENT: true
    });
  }

  function highlightElement(element, language) {
    const fragment = sanitizedHighlight(element.textContent, language);
    if (!fragment) return false;
    element.replaceChildren(fragment);
    element.classList.add("hljs");
    return true;
  }

  function fencedCodeLanguage(element) {
    const languageClass = Array.from(element.classList)
      .find(className => className.startsWith("language-"));
    if (!languageClass) return "";
    const language = languageClass.slice("language-".length);
    return /^[a-z0-9_+#.-]+$/i.test(language) ? language : "";
  }

  async function highlightMarkdownCode(root) {
    const blocks = Array.from(root.querySelectorAll("pre > code"))
      .map(element => ({ element, language: fencedCodeLanguage(element) }))
      .filter(block => block.language);
    if (blocks.length === 0) return;
    await loadHighlightLanguages(blocks.map(block => block.language));
    blocks.forEach(block => highlightElement(block.element, block.language));
  }

  function initScriptPreview() {
    const source = document.querySelector("[data-script-preview]");
    if (!source) return;
    const language = source.dataset.highlightLanguage || "";
    const container = source.closest("pre");
    container?.setAttribute("aria-busy", "true");
    loadHighlightLanguages([language]).then(() => {
      if (source.isConnected) highlightElement(source, language);
      container?.removeAttribute("aria-busy");
    }).catch(() => {
      container?.removeAttribute("aria-busy");
    });
  }

  function managedMarkdownURL(reference, baseURL) {
    try {
      const resolved = new URL(reference, new URL(baseURL, location.origin));
      const prefix = "/resources/files/view/";
      if (resolved.origin !== location.origin || !resolved.pathname.startsWith(prefix)) return null;
      return resolved;
    } catch {
      return null;
    }
  }

  function managedRouteURL(url, prefix) {
    const path = url.pathname.slice("/resources/files/view/".length);
    return `${prefix}${path}${url.search}${url.hash}`;
  }

  function replaceExternalMarkdownImage(image, reference) {
    const replacement = document.createElement("span");
    replacement.className = "markdown-external-image";
    replacement.append(makeIcon("image"));
    const label = image.getAttribute("alt")?.trim() || reference;
    try {
      const target = new URL(reference, location.href);
      if (target.protocol === "http:" || target.protocol === "https:") {
        const link = document.createElement("a");
        link.href = target.href;
        link.rel = "noopener noreferrer";
        link.dataset.native = "";
        link.textContent = label;
        replacement.append(link);
      } else {
        replacement.append(document.createTextNode(label));
      }
    } catch {
      replacement.append(document.createTextNode(label));
    }
    image.replaceWith(replacement);
  }

  function rewriteMarkdownResources(root, baseURL) {
    root.querySelectorAll("a[href]").forEach(link => {
      const reference = link.getAttribute("href") || "";
      if (reference.startsWith("#")) return;
      const managed = managedMarkdownURL(reference, baseURL);
      if (managed) {
        const extension = managed.pathname.slice(managed.pathname.lastIndexOf(".")).toLowerCase();
        if (managed.pathname.endsWith("/")) {
          link.href = managedRouteURL(managed, "/resources/files/");
        } else if (extension === ".md") {
          link.href = managedRouteURL(managed, "/resources/files/view/");
        } else {
          link.href = managedRouteURL(managed, "/resources/files/download/");
          link.dataset.native = "";
        }
        return;
      }
      try {
        const target = new URL(reference, location.href);
        if (!["http:", "https:", "mailto:"].includes(target.protocol)) {
          link.removeAttribute("href");
          return;
        }
        if (target.origin !== location.origin) link.rel = "noopener noreferrer";
      } catch {
        link.removeAttribute("href");
      }
    });

    root.querySelectorAll("img").forEach(image => {
      const reference = image.getAttribute("src") || "";
      const managed = managedMarkdownURL(reference, baseURL);
      if (managed) {
        image.src = managedRouteURL(managed, "/resources/files/preview/");
        image.loading = "lazy";
        image.decoding = "async";
        return;
      }
      try {
        const target = new URL(reference, location.href);
        if (target.origin === location.origin && ["http:", "https:"].includes(target.protocol)) {
          image.src = target.href;
          image.loading = "lazy";
          image.decoding = "async";
          return;
        }
      } catch {
        // Invalid and non-local images are replaced below.
      }
      replaceExternalMarkdownImage(image, reference);
    });
  }

  function initMarkdownPreview() {
    const preview = document.querySelector("[data-markdown-preview]");
    const source = document.querySelector("[data-markdown-source]");
    if (!preview || !source) return;
    preview.setAttribute("aria-busy", "true");
    loadMarkdownLibraries().then(async () => {
      if (!preview.isConnected || !source.isConnected) return;
      const renderer = window.markdownit({
        html: false,
        linkify: true
      });
      const fragment = window.DOMPurify.sanitize(renderer.render(source.textContent), {
        USE_PROFILES: { html: true },
        SANITIZE_NAMED_PROPS: true,
        ALLOW_DATA_ATTR: false,
        FORBID_TAGS: ["style", "form", "input", "button", "textarea", "select", "option"],
        FORBID_ATTR: ["style"],
        RETURN_DOM_FRAGMENT: true
      });
      rewriteMarkdownResources(fragment, preview.dataset.markdownBase || "/resources/files/view/");
      await highlightMarkdownCode(fragment);
      if (!preview.isConnected || !source.isConnected) return;
      preview.replaceChildren(fragment);
      preview.hidden = false;
      preview.removeAttribute("aria-busy");
      source.hidden = true;
    }).catch(() => {
      preview.removeAttribute("aria-busy");
    });
  }

  function setSidebar(open) {
    document.body.classList.toggle("sidebar-open", open);
    document.querySelector("[data-sidebar-toggle]")?.setAttribute("aria-expanded", String(open));
    const scrim = document.querySelector(".sidebar-scrim");
    if (scrim) scrim.hidden = !open;
  }

  function isNativeLink(link, destination) {
    return link.hasAttribute("data-native") || link.hasAttribute("download") ||
      link.target === "_blank" || destination.origin !== location.origin ||
      destination.hash || destination.pathname.startsWith("/assets/") ||
      destination.pathname.endsWith(".csv");
  }

  async function fetchDocument(url, options = {}) {
    const response = await fetch(url, {
      credentials: "same-origin",
      ...options,
      headers: { "X-ScriptBoard-Navigation": "pjax", "Accept": "text/html", ...(options.headers || {}) }
    });
    const type = response.headers.get("content-type") || "";
    if (!type.includes("text/html")) return { response, document: null };
    const text = await response.text();
    return { response, document: new DOMParser().parseFromString(text, "text/html") };
  }

  function navigationOwnsPath(linkPath, path) {
    if (linkPath === "/monitor") return path === "/monitor";
    if (linkPath === "/resources/files/") {
      return path.startsWith("/resources/files/") || path === "/resources/trash";
    }
    return path === linkPath || path.startsWith(`${linkPath}/`);
  }

  function mainNavigationLink(url, exact = false) {
    const path = new URL(url, location.href).pathname;
    return Array.from(document.querySelectorAll(".sidebar-nav a[href]")).find(link => {
      const linkPath = new URL(link.href, location.href).pathname;
      return exact ? linkPath === path : navigationOwnsPath(linkPath, path);
    }) || null;
  }

  function navigationTitle(link) {
    const label = link?.querySelector("span:last-child")?.textContent.trim();
    return label ? `${label} · ScriptBoard` : document.title;
  }

  function updateShellLocation(url) {
    const destination = new URL(url, location.href);
    const current = mainNavigationLink(destination.href);
    document.querySelectorAll(".sidebar-nav a[aria-current='page']").forEach(link => link.removeAttribute("aria-current"));
    current?.setAttribute("aria-current", "page");

    const settings = document.querySelector('.sidebar-utilities a[href="/settings/account"]');
    if (settings) {
      if (destination.pathname.startsWith("/settings/")) settings.setAttribute("aria-current", "page");
      else settings.removeAttribute("aria-current");
    }
    const returnTo = document.querySelector('form[action="/settings/locale"] input[name="return_to"]');
    if (returnTo) returnTo.value = `${destination.pathname}${destination.search}${destination.hash}`;
  }

  function isDeferredDataURL(url) {
    const path = new URL(url, location.href).pathname;
    if (path === "/resources/variables" ||
        path === "/config/quick-runs" ||
        path === "/config/schedules" ||
        path === "/history/runs" ||
        path === "/monitor/runs" ||
        path === "/history/audit" ||
        path === "/resources/trash") {
      return true;
    }
    if (!path.startsWith("/resources/files/")) return false;
    const relative = path.slice("/resources/files/".length);
    return !["new-directory", "upload", "run/", "quick-run/", "download/", "preview/", "view/", "edit/"]
      .some(prefix => relative === prefix.replace(/\/$/, "") || relative.startsWith(prefix));
  }

  function createDeferredDataFailure(url, title) {
    const content = document.createElement("div");
    content.className = "data-load-state data-load-state--error";
    content.dataset.deferredState = "error";
    content.setAttribute("role", "alert");
    const icon = document.createElement("span");
    icon.className = "data-load-state__icon";
    icon.dataset.lucide = "triangle-alert";
    const heading = document.createElement("h2");
    heading.textContent = words().loadFailed;
    const retry = document.createElement("button");
    retry.className = "button button--primary";
    retry.type = "button";
    const retryIcon = document.createElement("span");
    retryIcon.dataset.lucide = "rotate-ccw";
    const retryLabel = document.createElement("span");
    retryLabel.textContent = words().retry;
    retry.append(retryIcon, retryLabel);
    retry.addEventListener("click", () => navigate(url, false, { deferredData: true, title }));
    content.append(icon, heading, retry);
    renderIcons(content);
    return content;
  }

  function showDeferredDataFailure(url, title) {
    const currentRegion = document.querySelector("[data-deferred-region]");
    if (!currentRegion) return false;
    cleanupPage();
    currentRegion.replaceChildren(createDeferredDataFailure(url, title));
    initPage();
    return true;
  }

  function setTaskLinkBusy(link, busy) {
    if (!link) return;
    link.toggleAttribute("aria-busy", busy);
    if (busy) {
      link.setAttribute("aria-disabled", "true");
    } else {
      link.removeAttribute("aria-disabled");
    }
  }

  function cancelTaskPanelRequest() {
    const request = taskPanelRequest;
    if (!request) return;
    taskPanelRequest = null;
    request.controller.abort();
    setTaskLinkBusy(request.trigger, false);
  }

  async function navigate(url, push = true, options = {}) {
    cancelTaskPanelRequest();
    navigationRequest?.controller.abort();
    const request = {
      sequence: ++navigationSequence,
      controller: new AbortController(),
    };
    navigationRequest = request;
    navigationBusy = true;
    document.documentElement.setAttribute("aria-busy", "true");
    const deferredData = options.deferredData ?? isDeferredDataURL(url);
    const immediate = deferredData && options.immediate === true;
    const title = options.title || navigationTitle(mainNavigationLink(url));
    let shellCommitted = false;
    if (immediate) {
      if (push) history.pushState({ pjax: true }, "", url);
      document.title = title;
      updateShellLocation(url);
      setSidebar(false);
      window.scrollTo({ top: 0, behavior: "auto" });
    }
    try {
      const result = await fetchDocument(url, {
        signal: request.controller.signal,
        headers: deferredData ? { "X-ScriptBoard-Data": "shell" } : {},
      });
      if (navigationRequest !== request || request.sequence !== navigationSequence) return;
      const responseURL = new URL(result.response.url || url, location.href);
      if (!result.document || responseURL.pathname === "/login") {
        location.assign(result.response.url || url);
        return;
      }
      if (!result.response.ok) {
        location.assign(result.response.url || url);
        return;
      }
      const nextMain = result.document.querySelector("main");
      const currentMain = document.querySelector("main");
      if (!nextMain || !currentMain) {
        location.assign(result.response.url || url);
        return;
      }
      cleanupPage();
      currentMain.replaceWith(document.importNode(nextMain, true));
      document.title = result.document.title;
      document.documentElement.lang = result.document.documentElement.lang || document.documentElement.lang;
      if (deferredData) {
        shellCommitted = true;
        if (immediate && location.href !== result.response.url) {
          history.replaceState({ pjax: true }, "", result.response.url);
        }
      } else if (push) {
        history.pushState({ pjax: true }, "", result.response.url);
      }
      updateShellLocation(result.response.url);
      setSidebar(false);
      window.scrollTo({ top: 0, behavior: "auto" });
      initPage();

      if (deferredData) {
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        if (navigationRequest !== request || request.sequence !== navigationSequence) return;
        const dataResult = await fetchDocument(result.response.url || url, { signal: request.controller.signal });
        if (navigationRequest !== request || request.sequence !== navigationSequence) return;
        const dataResponseURL = new URL(dataResult.response.url || url, location.href);
        if (!dataResult.document || dataResponseURL.pathname === "/login") {
          location.assign(dataResult.response.url || url);
          return;
        }
        if (!dataResult.response.ok) {
          showDeferredDataFailure(url, title);
          return;
        }
        const nextRegion = dataResult.document.querySelector("[data-deferred-region]");
        const currentRegion = document.querySelector("[data-deferred-region]");
        if (!nextRegion || !currentRegion) {
          location.assign(dataResult.response.url || url);
          return;
        }
        cleanupPage();
        currentRegion.replaceWith(document.importNode(nextRegion, true));
        document.title = dataResult.document.title;
        document.documentElement.lang = dataResult.document.documentElement.lang || document.documentElement.lang;
        if (!immediate && push) {
          history.pushState({ pjax: true }, "", dataResult.response.url);
        } else if (location.href !== dataResult.response.url) {
          history.replaceState({ pjax: true }, "", dataResult.response.url);
        }
        updateShellLocation(dataResult.response.url);
        initPage();
      }

      if (options.focusSelector) {
        const focusTarget = document.querySelector(options.focusSelector);
        focusTarget?.focus();
        if (focusTarget instanceof HTMLInputElement) {
          const end = focusTarget.value.length;
          focusTarget.setSelectionRange(end, end);
        }
      }
    } catch (error) {
      if (error?.name === "AbortError" || navigationRequest !== request) return;
      if (!shellCommitted || !showDeferredDataFailure(url, title)) location.assign(url);
    } finally {
      if (navigationRequest === request) {
        navigationRequest = null;
        navigationBusy = false;
        document.documentElement.removeAttribute("aria-busy");
      }
    }
  }

  function buildTaskPanel(main, url, push) {
    const returnFocus = taskPanelState?.returnFocus || document.activeElement;
    closeTaskPanel(false, false);
    const host = document.createElement("div");
    const cleanups = [];
    host.className = "task-panel-host";
    host.innerHTML = '<button class="task-panel-scrim" type="button"></button><section class="task-panel" role="dialog" aria-modal="true" tabindex="-1" data-task-panel><button class="task-panel-close" type="button" data-task-panel-close><span data-lucide="x" aria-hidden="true"></span></button></section>';
    const closeLabel = main.dataset.taskCloseLabel || words().loadFailed;
    host.querySelector(".task-panel-scrim").setAttribute("aria-label", closeLabel);
    host.querySelector(".task-panel-close").setAttribute("aria-label", closeLabel);
    const panel = host.querySelector(".task-panel");
    const panelMain = document.importNode(main, true);
    const heading = panelMain.querySelector("h1");
    if (heading) {
      heading.id ||= `task-panel-heading-${Date.now()}`;
      panel.setAttribute("aria-labelledby", heading.id);
    }
    panel.append(panelMain);
    document.body.append(host);
    document.body.classList.add("has-task-panel");
    const background = [...document.body.children]
      .filter(child => child !== host && child instanceof HTMLElement)
      .map(child => ({
        child,
        inert: child.inert,
        ariaHidden: child.getAttribute("aria-hidden"),
      }));
    taskPanelState = { host, returnURL: location.href, taskURL: url, cleanups, background, returnFocus };
    if (push) {
      history.pushState(
        { task: true, returnURL: taskPanelState.returnURL, taskURL: taskPanelState.taskURL },
        "",
        taskPanelState.returnURL,
      );
    }
    requestAnimationFrame(() => host.classList.add("is-open"));
    host.querySelector("[data-task-panel-close]")?.focus();
    background.forEach(({ child }) => {
      child.inert = true;
      child.setAttribute("aria-hidden", "true");
    });
    renderIcons(host);
    localizeTimes(host);
    initScheduleCron(cleanups, host);
    const websiteForm = host.querySelector("[data-website-monitor-form]");
    if (websiteForm) cleanups.push(initWebsiteMonitorForm(websiteForm));
    const websiteMonitoring = host.querySelector("[data-website-monitoring],[data-website-detail]");
    if (websiteMonitoring) cleanups.push(initWebsiteMonitoring(websiteMonitoring));
    const websiteNginx = host.querySelector("[data-website-nginx]");
    if (websiteNginx) cleanups.push(initWebsiteNginx(websiteNginx));
  }

  function openTask(url, push = true, trigger = null) {
    const destination = new URL(url, location.href).href;
    if (navigationBusy) return Promise.resolve();
    if (taskPanelRequest?.url === destination) return taskPanelRequest.promise;
    cancelTaskPanelRequest();

    const request = {
      url: destination,
      controller: new AbortController(),
      trigger,
      promise: null
    };
    taskPanelRequest = request;
    setTaskLinkBusy(trigger, true);

    request.promise = (async () => {
      try {
        const result = await fetchDocument(destination, { signal: request.controller.signal });
        if (taskPanelRequest !== request) return;
        const main = result.document?.querySelector("main[data-task-page]");
        if (!main || !result.response.ok) {
          await navigate(destination, push);
          return;
        }
        if (taskPanelRequest !== request) return;
        buildTaskPanel(main, result.response.url, push);
      } catch (error) {
        if (error?.name !== "AbortError" && taskPanelRequest === request) {
          location.assign(destination);
        }
      } finally {
        setTaskLinkBusy(trigger, false);
        if (taskPanelRequest === request) taskPanelRequest = null;
      }
    })();
    return request.promise;
  }

  function closeTaskPanel(useHistory = true, restoreFocus = true) {
    if (!taskPanelState) return;
    const { host, cleanups = [], background = [], returnFocus } = taskPanelState;
    taskPanelState = null;
    cleanups.splice(0).forEach(cleanup => cleanup());
    background.forEach(({ child, inert, ariaHidden }) => {
      child.inert = inert;
      if (ariaHidden === null) child.removeAttribute("aria-hidden");
      else child.setAttribute("aria-hidden", ariaHidden);
    });
    host.classList.remove("is-open");
    document.body.classList.remove("has-task-panel");
    window.setTimeout(() => host.remove(), 210);
    if (restoreFocus && returnFocus instanceof HTMLElement && returnFocus.isConnected) returnFocus.focus();
    if (useHistory) history.back();
  }

  function resetSubmit(form) {
    form.removeAttribute("aria-busy");
    form.querySelectorAll("[data-submit-original]").forEach(button => {
      button.innerHTML = button.dataset.submitOriginal;
      button.disabled = false;
      button.removeAttribute("aria-busy");
      button.style.minWidth = button.dataset.submitOriginalMinWidth;
      button.style.width = button.dataset.submitOriginalWidth;
      delete button.dataset.submitOriginal;
      delete button.dataset.submitOriginalMinWidth;
      delete button.dataset.submitOriginalWidth;
    });
    form.querySelectorAll("[data-submitter-mirror]").forEach(input => input.remove());
  }

  async function submitAsync(form, submitter) {
    form.querySelector("[data-async-submit-error]")?.remove();
    const submittingTaskState = taskPanelState?.host.contains(form) ? taskPanelState : null;
    const data = new FormData(form);
    if (submitter?.name) data.set(submitter.name, submitter.value);
    if (form.method.toLowerCase() === "get") {
      const destination = new URL(form.action || location.href, location.href);
      destination.search = "";
      data.forEach((value, name) => {
        if (typeof value === "string" && value !== "") destination.searchParams.append(name, value);
      });
      const scheduleSearch = form.querySelector("[data-schedule-filter-name]");
      if (scheduleSearch && scheduleSearch.value.trim() !== scheduleSearch.dataset.scheduleFilterName) {
        destination.searchParams.delete("schedule_id");
      }
      if (form.matches("[data-file-search]") && !destination.searchParams.get("sort")) {
        destination.searchParams.delete("direction");
      }
      try {
        await navigate(destination.href, form.hasAttribute("data-async-push"), {
          focusSelector: form.dataset.focusAfterNavigation,
        });
      } finally {
        resetSubmit(form);
      }
      return;
    }
    try {
      const action = submitter?.hasAttribute("formaction") ? submitter.formAction : form.action;
      const result = await fetchDocument(action, { method: form.method, body: data });
      if (submittingTaskState && taskPanelState !== submittingTaskState) return;
      if (!submittingTaskState && !form.isConnected) return;
      if (result.response.redirected && result.response.ok) {
        const destination = result.response.url;
        if (submittingTaskState) {
          const returnURL = submittingTaskState.returnURL;
          const nextTask = result.document?.querySelector("main[data-task-page]");
          if (nextTask) {
            buildTaskPanel(nextTask, destination, false);
            taskPanelState.returnURL = returnURL;
            history.replaceState(
              { task: true, returnURL, taskURL: destination },
              "",
              returnURL,
            );
            return;
          }
          closeTaskPanel(false);
          if (destination === returnURL && history.state?.task) {
            history.replaceState({ pjax: true }, "", destination);
            history.back();
            return;
          }
          history.replaceState({ pjax: true }, "", destination);
          await navigate(destination, false);
        } else {
          await navigate(destination, true);
        }
        return;
      }
      const nextMain = result.document?.querySelector("main");
      if (nextMain) {
        if (submittingTaskState && nextMain.matches("[data-task-page]")) {
          const returnURL = submittingTaskState.returnURL;
          buildTaskPanel(nextMain, result.response.url, false);
          taskPanelState.returnURL = returnURL;
          history.replaceState(
            { task: true, returnURL, taskURL: result.response.url },
            "",
            returnURL,
          );
          return;
        }
        const target = submittingTaskState ? submittingTaskState.host.querySelector("main") : document.querySelector("main");
        target?.replaceWith(document.importNode(nextMain, true));
        if (!submittingTaskState) document.title = result.document.title;
        initPage();
        return;
      }
      if (!result.response.ok) throw new Error(`HTTP ${result.response.status}`);
    } catch {
      const message = document.createElement("p");
      message.className = "async-submit-error";
      message.dataset.asyncSubmitError = "";
      message.setAttribute("role", "alert");
      message.tabIndex = -1;
      message.textContent = words().submitFailed;
      form.prepend(message);
      message.focus();
    } finally {
      resetSubmit(form);
    }
  }

  async function submitUpdateApply(form, submitter) {
    const data = new FormData(form);
    if (submitter?.name) data.set(submitter.name, submitter.value);
    try {
      const response = await fetch(form.action, {
        method: "POST",
        body: data,
        credentials: "same-origin",
        headers: { "Accept": "application/json" },
      });
      if (!response.ok) {
        throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
      }
      const payload = await response.json();
      const main = document.querySelector("[data-updates-page]");
      if (main) {
        const notice = document.createElement("aside");
        notice.className = "update-reconnect";
        notice.setAttribute("role", "status");
        notice.setAttribute("aria-live", "polite");
        notice.innerHTML = '<span data-lucide="loader-circle" aria-hidden="true"></span><div><strong></strong><p></p></div>';
        notice.querySelector("strong").textContent = main.dataset.updateInstallingTitle;
        notice.querySelector("p").textContent = main.dataset.updateInstallingDescription;
        main.prepend(notice);
        renderIcons(notice);
      }
      let delay = 1000;
      let sawOffline = false;
      const deadline = Date.now() + 180000;
      while (Date.now() < deadline) {
        await new Promise(resolve => window.setTimeout(resolve, delay));
        try {
          const status = await fetch(payload.status_url || "/settings/updates/status", {
            credentials: "same-origin",
            cache: "no-store",
          });
          if (status.ok) {
            const snapshot = await status.json();
            const phase = snapshot.operation?.phase;
            if (sawOffline || ["committed", "rolled_back", "failed_safe", "needs_recovery"].includes(phase)) {
              location.assign("/settings/updates");
              return;
            }
          } else {
            sawOffline = true;
          }
        } catch {
          sawOffline = true;
        }
        delay = Math.min(5000, delay + 1000);
      }
      location.assign("/settings/updates");
    } catch (error) {
      const main = document.querySelector("[data-updates-page]");
      const title = main?.dataset.updateStartError || "Unable to start the update.";
      window.alert(error?.message ? `${title}\n\n${error.message}` : title);
      resetSubmit(form);
    }
  }

  async function submitLogin(form, submitter) {
    const errorBox = document.querySelector("[data-login-error]");
    const errorMessage = document.querySelector("[data-login-error-message]");
    const data = new FormData(form);
    if (submitter?.name) data.set(submitter.name, submitter.value);
    try {
      const response = await fetch(form.action, {
        method: "POST", body: data, credentials: "same-origin",
        headers: { "Accept": "application/json" }
      });
      const payload = await response.json();
      if (response.ok && payload.redirect) {
        location.assign(payload.redirect);
        return;
      }
      if (payload.csrf_token) form.elements.csrf_token.value = payload.csrf_token;
      if (errorBox && errorMessage) {
        errorMessage.textContent = payload.error || form.dataset.networkError;
        errorBox.hidden = false;
      }
    } catch {
      if (errorBox && errorMessage) {
        errorMessage.textContent = form.dataset.networkError;
        errorBox.hidden = false;
      }
    } finally {
      resetSubmit(form);
    }
  }

  function drawPath(points, key, maximum = false) {
    const values = points.map(point => {
      const source = maximum ? point.maximum : point.average;
      return Number(source?.values?.[key] || 0);
    });
    if (!values.length) return "";
    const isPercent = key.endsWith("Percent");
    const ceiling = isPercent ? 100 : Math.max(1, ...values);
    return values.map((value, index) => {
      const x = values.length === 1 ? 0 : index * 1000 / (values.length - 1);
      const y = 78 - Math.min(1, Math.max(0, value / ceiling)) * 74;
      return `${index ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`;
    }).join(" ");
  }

  async function initOverview(cleanups) {
    const root = document.querySelector("[data-host-overview]");
    if (!root) return;
    const controller = new AbortController();
    cleanups.push(() => controller.abort());
    try {
      const response = await fetch(`${root.dataset.overviewUrl}?range=${encodeURIComponent(root.dataset.range)}`, {
        credentials: "same-origin", cache: "no-store", signal: controller.signal
      });
      if (!response.ok) return;
      const data = await response.json();
      root.querySelectorAll("[data-metric-chart]").forEach(chart => {
        const key = chart.dataset.metricChart;
        chart.querySelector("[data-chart-average]")?.setAttribute("d", drawPath(data.series || [], key, false));
        chart.querySelector("[data-chart-peak]")?.setAttribute("d", drawPath(data.series || [], key, true));
      });
    } catch (error) {
      if (error.name !== "AbortError") console.debug("Overview update unavailable");
    }
  }

  const applicationDetailCopy = {
    "zh-CN": {
      loading: "正在读取运行详情",
      loadingHint: "正在获取历史指标、命令、启动信息和关联进程。",
      loadFailed: "暂时无法读取运行详情",
      retry: "重试",
      history: "历史趋势",
      runtime: "运行详情",
      noHistory: "所选时间范围内还没有足够的指标数据。",
      unavailable: "当前运行详情不可用",
      partial: "部分事实因权限或平台能力不可读取。",
      runtimeReasons: {
        not_running: "应用当前未运行",
        detail_probe_unavailable: "运行详情采集器不可用",
        detail_unavailable: "运行详情未返回可读结果",
        unsupported_application_kind: "暂不支持此类应用的运行详情",
        process_exited: "进程已在采集期间退出",
        container_exited: "容器已在采集期间退出",
        identity_restricted: "进程身份受系统权限限制",
        permission_denied: "当前服务账户无权读取这些事实",
        runtime_partially_readable: "仅能读取部分运行事实",
        docker_unavailable: "Docker 当前不可用",
        docker_inspect_unavailable: "无法读取容器配置",
        docker_top_unavailable: "无法读取容器进程",
      },
      requestErrors: {
        application_not_found: "应用已不在当前快照中",
        snapshot_unavailable: "暂时无法刷新应用快照",
        invalid_history_range: "时间范围无效",
        details_unavailable: "暂时无法读取应用详情",
      },
      average: "平均",
      maximum: "峰值",
      cpu: "CPU",
      memory: "内存",
      disk: "磁盘吞吐",
      read: "读取",
      write: "写入",
      commandLine: "实际命令",
      pid: "PID",
      parentPid: "父 PID",
      user: "用户",
      startedAt: "启动时间",
      durationSeconds: "运行时长",
      architecture: "架构",
      threads: "线程",
      handles: "句柄",
      executablePath: "可执行文件",
      workingDirectory: "工作目录",
      listeningPorts: "监听端口",
      connections: "连接",
      startMethod: "启动方式",
      containerId: "容器 ID",
      hostPid: "宿主 PID",
      containerPid: "容器 PID",
      health: "健康状态",
      restartPolicy: "重启策略",
      restartCount: "重启次数",
      image: "镜像",
      ports: "端口映射",
      networkMode: "网络模式",
      mounts: "挂载",
      relatedProcesses: "关联进程",
      copy: "复制命令",
      copied: "已复制",
      seconds: "秒",
      minutes: "分钟",
      hours: "小时",
      empty: "—"
    },
    "en-US": {
      loading: "Reading runtime details",
      loadingHint: "Collecting history, command, startup facts, and related processes.",
      loadFailed: "Runtime details are temporarily unavailable",
      retry: "Retry",
      history: "History",
      runtime: "Runtime details",
      noHistory: "There is not enough metric data in this time range yet.",
      unavailable: "Runtime details are unavailable",
      partial: "Some facts cannot be read because of permissions or platform support.",
      runtimeReasons: {
        not_running: "The application is not running",
        detail_probe_unavailable: "The runtime detail collector is unavailable",
        detail_unavailable: "The runtime detail collector returned no readable result",
        unsupported_application_kind: "Runtime details are not supported for this application kind",
        process_exited: "The process exited while details were collected",
        container_exited: "The container exited while details were collected",
        identity_restricted: "The process identity is restricted by the operating system",
        permission_denied: "The service account cannot read these runtime facts",
        runtime_partially_readable: "Only part of the runtime facts can be read",
        docker_unavailable: "Docker is unavailable",
        docker_inspect_unavailable: "Container configuration cannot be read",
        docker_top_unavailable: "Container processes cannot be read",
      },
      requestErrors: {
        application_not_found: "The application is no longer in the current snapshot",
        snapshot_unavailable: "The application snapshot cannot be refreshed right now",
        invalid_history_range: "The selected time range is invalid",
        details_unavailable: "Application details cannot be read right now",
      },
      average: "Average",
      maximum: "Peak",
      cpu: "CPU",
      memory: "Memory",
      disk: "Disk throughput",
      read: "Read",
      write: "Write",
      commandLine: "Command",
      pid: "PID",
      parentPid: "Parent PID",
      user: "User",
      startedAt: "Started",
      durationSeconds: "Uptime",
      architecture: "Architecture",
      threads: "Threads",
      handles: "Handles",
      executablePath: "Executable",
      workingDirectory: "Working directory",
      listeningPorts: "Listening ports",
      connections: "Connections",
      startMethod: "Start method",
      containerId: "Container ID",
      hostPid: "Host PID",
      containerPid: "Container PID",
      health: "Health",
      restartPolicy: "Restart policy",
      restartCount: "Restart count",
      image: "Image",
      ports: "Port mappings",
      networkMode: "Network mode",
      mounts: "Mounts",
      relatedProcesses: "Related processes",
      copy: "Copy command",
      copied: "Copied",
      seconds: "seconds",
      minutes: "minutes",
      hours: "hours",
      empty: "—"
    }
  };

  const applicationWords = () => applicationDetailCopy[locale()];
  const applicationValue = (object, camel, pascal = camel[0].toUpperCase() + camel.slice(1)) =>
    object?.[camel] ?? object?.[pascal];
  const escapeMarkup = value => String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
  const formatApplicationBytes = value => {
    let bytes = Number(value);
    if (!Number.isFinite(bytes) || bytes < 0) return applicationWords().empty;
    const units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let unit = 0;
    while (bytes >= 1024 && unit < units.length - 1) {
      bytes /= 1024;
      unit += 1;
    }
    return `${bytes >= 100 || unit === 0 ? bytes.toFixed(0) : bytes.toFixed(1)} ${units[unit]}`;
  };
  const formatApplicationRate = value => `${formatApplicationBytes(value)}/s`;
  const formatApplicationDuration = value => {
    const seconds = Math.max(0, Number(value) || 0);
    if (seconds >= 3600) return `${(seconds / 3600).toFixed(seconds >= 36000 ? 0 : 1)} ${applicationWords().hours}`;
    if (seconds >= 60) return `${Math.round(seconds / 60)} ${applicationWords().minutes}`;
    return `${Math.round(seconds)} ${applicationWords().seconds}`;
  };
  const formatApplicationTime = value => {
    if (!value) return applicationWords().empty;
    const date = new Date(value);
    if (Number.isNaN(date.valueOf())) return String(value);
    return new Intl.DateTimeFormat(locale(), {
      year: "numeric", month: "short", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit"
    }).format(date);
  };
  const formatApplicationList = value => {
    if (!Array.isArray(value)) return value || applicationWords().empty;
    return value.length ? value.map(item => {
      if (item == null) return "";
      if (typeof item === "string" || typeof item === "number") return String(item);
      return Object.values(item).filter(part => part !== "" && part != null).join(" · ");
    }).filter(Boolean).join(", ") : applicationWords().empty;
  };

  function applicationSeriesPath(points, key, maximum) {
    if (!points.length || maximum <= 0) return "";
    return points.map((point, index) => {
      const value = Number(applicationValue(point, key)) || 0;
      const x = points.length === 1 ? 0 : (index / (points.length - 1)) * 640;
      const y = 76 - Math.min(1, value / maximum) * 68;
      return `${index ? "L" : "M"}${x.toFixed(2)} ${y.toFixed(2)}`;
    }).join(" ");
  }

  function applicationChart(title, points, series, formatter) {
    const values = points.flatMap(point => series.map(item => Number(applicationValue(point, item.key)) || 0));
    const maximum = Math.max(0, ...values);
    const legend = series.map(item => {
      const average = points.length
        ? points.reduce((total, point) => total + (Number(applicationValue(point, item.key)) || 0), 0) / points.length
        : 0;
      return `<span><i style="--series:${item.color}"></i>${escapeMarkup(item.label)} ${escapeMarkup(applicationWords().average)} ${escapeMarkup(formatter(average))}</span>`;
    }).join("");
    const paths = series.map(item =>
      `<path d="${applicationSeriesPath(points, item.key, maximum)}" style="--series:${item.color}"></path>`
    ).join("");
    return `<figure class="application-history-chart">
      <figcaption><strong>${escapeMarkup(title)}</strong><span>${legend}</span></figcaption>
      <svg viewBox="0 0 640 84" preserveAspectRatio="none" role="img" aria-label="${escapeMarkup(title)}">
        <line x1="0" y1="76" x2="640" y2="76"></line>
        <line x1="0" y1="42" x2="640" y2="42"></line>
        <line x1="0" y1="8" x2="640" y2="8"></line>
        ${paths}
      </svg>
      <small>${escapeMarkup(applicationWords().maximum)} ${escapeMarkup(formatter(maximum))}</small>
    </figure>`;
  }

  function renderApplicationHistory(payload) {
    const history = applicationValue(payload, "history") || {};
    const points = applicationValue(history, "points") || [];
    if (!points.length) return `<div class="application-detail-empty">${escapeMarkup(applicationWords().noHistory)}</div>`;
    return `<div class="application-history-grid">
      ${applicationChart(applicationWords().cpu, points, [
        { key: "cpuAverage", label: applicationWords().average, color: "var(--accent)" },
        { key: "cpuMaximum", label: applicationWords().maximum, color: "var(--faint)" }
      ], value => `${Number(value).toFixed(1)}%`)}
      ${applicationChart(applicationWords().memory, points, [
        { key: "memoryAverage", label: applicationWords().average, color: "var(--accent)" },
        { key: "memoryMaximum", label: applicationWords().maximum, color: "var(--faint)" }
      ], formatApplicationBytes)}
      ${applicationChart(applicationWords().disk, points, [
        { key: "readAverage", label: applicationWords().read, color: "var(--accent)" },
        { key: "writeAverage", label: applicationWords().write, color: "var(--warning)" }
      ], formatApplicationRate)}
    </div>`;
  }

  function renderApplicationRuntime(payload) {
    const runtime = applicationValue(payload, "runtime") || {};
    const state = applicationValue(runtime, "state") || "unavailable";
    const reasonCode = applicationValue(runtime, "code") || "";
    const reason = applicationWords().runtimeReasons[reasonCode]
      || (state === "partial" ? applicationWords().partial : applicationWords().unavailable);
    const facts = applicationValue(runtime, "kind") === "docker"
      ? applicationValue(runtime, "docker") || {}
      : applicationValue(runtime, "host") || {};
    const notice = state === "available" ? "" : `<div class="application-runtime-notice" data-state="${escapeMarkup(state)}">
      <span data-lucide="${state === "restricted" ? "shield-alert" : "info"}" aria-hidden="true"></span>
      <div><strong>${escapeMarkup(reason)}</strong>
      <p>${escapeMarkup(reasonCode)}</p></div>
    </div>`;
    const keys = applicationValue(runtime, "kind") === "docker"
      ? ["commandLine", "containerId", "hostPid", "containerPid", "startedAt", "durationSeconds", "health", "restartPolicy", "restartCount", "image", "workingDirectory", "ports", "networkMode", "mounts"]
      : ["commandLine", "pid", "parentPid", "user", "startedAt", "durationSeconds", "architecture", "threads", "handles", "executablePath", "workingDirectory", "listeningPorts", "connections", "startMethod"];
    const factsHTML = keys.map(key => {
      let value = applicationValue(facts, key);
      if (key === "startedAt") value = formatApplicationTime(value);
      else if (key === "durationSeconds") value = formatApplicationDuration(value);
      else if (Array.isArray(value)) value = formatApplicationList(value);
      else if (value === "" || value == null) value = applicationWords().empty;
      const command = key === "commandLine" && value !== applicationWords().empty
        ? `<button class="application-copy-command" type="button" data-copy-runtime-command data-copy-label="${escapeMarkup(applicationWords().copy)}" data-copied-label="${escapeMarkup(applicationWords().copied)}"><span data-lucide="copy" aria-hidden="true"></span>${escapeMarkup(applicationWords().copy)}</button>`
        : "";
      return `<div class="${key === "commandLine" ? "application-runtime-fact--wide" : ""}">
        <dt>${escapeMarkup(applicationWords()[key] || key)}</dt>
        <dd>${key === "commandLine" ? `<code data-runtime-command>${escapeMarkup(value)}</code>` : escapeMarkup(value)}${command}</dd>
      </div>`;
    }).join("");
    const related = applicationValue(facts, "relatedProcesses") || [];
    const relatedHTML = related.length
      ? `<section class="application-related-processes"><h4>${escapeMarkup(applicationWords().relatedProcesses)}</h4><div class="application-process-list">${
          related.map(process => `<div><strong>${escapeMarkup(applicationValue(process, "name") || applicationValue(process, "command") || applicationWords().empty)}</strong><span>PID ${escapeMarkup(applicationValue(process, "pid") ?? applicationWords().empty)}</span><code>${escapeMarkup(applicationValue(process, "commandLine") || applicationValue(process, "path") || "")}</code></div>`).join("")
        }</div></section>`
      : "";
    return `${notice}<dl class="application-runtime-facts">${factsHTML}</dl>${relatedHTML}`;
  }

  function applicationDetailLoading() {
    return `<div class="application-detail-loading" role="status"><span data-lucide="loader-circle" aria-hidden="true"></span><div><strong>${escapeMarkup(applicationWords().loading)}</strong><p>${escapeMarkup(applicationWords().loadingHint)}</p></div></div>`;
  }

  function initApplications(cleanups) {
    const root = document.querySelector("[data-applications-page]");
    if (!root) return;
    const switches = Array.from(root.querySelectorAll("[data-applications-refresh]"));
    const detailCache = new Map();
    const pendingDetails = new Map();
    let restorePinnedState = () => {};

    const replaceFromSnapshot = (snapshot, scope) => {
      const facts = snapshot.querySelector(".applications-fact-strip");
      const currentFacts = root.querySelector(".applications-fact-strip");
      if (facts && currentFacts) currentFacts.replaceWith(document.importNode(facts, true));
      const sourceTime = snapshot.querySelector(`[data-applications-refresh-time="${scope}"]`);
      const currentTime = root.querySelector(`[data-applications-refresh-time="${scope}"]`);
      if (sourceTime && currentTime) {
        currentTime.dateTime = sourceTime.dateTime;
        currentTime.textContent = sourceTime.textContent;
      }
      if (scope === "pinned") {
        detailCache.clear();
        const source = snapshot.querySelector("[data-pinned-applications]");
        const target = root.querySelector("[data-pinned-applications]");
        if (source && target) {
          const expanded = Array.from(target.querySelectorAll(".pinned-application[data-expanded='true']")).map(article => ({
            id: article.dataset.applicationId,
            range: article.querySelector("[data-application-range][aria-pressed='true']")?.dataset.applicationRange || "1h",
            tab: article.querySelector("[data-application-detail-tab][aria-selected='true']")?.dataset.applicationDetailTab || "history"
          }));
          target.replaceChildren(...Array.from(source.children, child => document.importNode(child, true)));
          restorePinnedState(expanded);
        }
      } else {
        const sourceBody = snapshot.querySelector(".applications-table tbody");
        const targetBody = root.querySelector(".applications-table tbody");
        if (sourceBody && targetBody) targetBody.replaceChildren(...Array.from(sourceBody.children, child => document.importNode(child, true)));
        const sourceSummary = snapshot.querySelector(".application-result-summary");
        const targetSummary = root.querySelector(".application-result-summary");
        if (sourceSummary && targetSummary) targetSummary.replaceChildren(...Array.from(sourceSummary.childNodes, child => document.importNode(child, true)));
      }
      renderIcons(root);
      localizeTimes(root);
    };

    const refresh = async (scope, state) => {
      if (!state.enabled || state.pending) return;
      state.pending = new AbortController();
      try {
        const result = await fetchDocument(location.href, { cache: "no-store", signal: state.pending.signal });
        if (result.response.ok && result.document && state.enabled) {
          replaceFromSnapshot(result.document, scope);
          state.failed = false;
          state.seconds = 5;
        }
      } catch (error) {
        if (error.name !== "AbortError") {
          state.failed = true;
          console.debug("Application snapshot update unavailable");
        }
      } finally {
        state.pending = null;
        state.render?.();
      }
    };

    switches.forEach(control => {
      const scope = control.dataset.applicationsRefresh;
      const label = control.closest(".applications-refresh")?.querySelector("span");
      const state = {
        enabled: control.getAttribute("aria-checked") === "true",
        pending: null,
        timer: null,
        seconds: 5,
        failed: false,
      };
      const renderState = () => {
        control.setAttribute("aria-checked", String(state.enabled));
        control.setAttribute("aria-label", state.enabled ? control.dataset.pauseLabel : control.dataset.startLabel);
        if (label) {
          const strong = label.querySelector("strong");
          const small = label.querySelector("small");
          if (strong) strong.textContent = state.enabled ? control.dataset.enabledLabel : control.dataset.disabledLabel;
          if (small) {
            if (state.failed) small.textContent = root.dataset.refreshUnavailable || control.dataset.disabledDescription;
            else if (state.enabled) small.textContent = `${control.dataset.enabledDescription} · ${state.seconds}s`;
            else small.textContent = control.dataset.disabledDescription;
          }
        }
        if (scope === "pinned") {
          const fact = root.querySelector("[data-pinned-live-fact]");
          if (fact) {
            fact.textContent = state.enabled ? control.dataset.enabledLabel : control.dataset.disabledLabel;
            fact.dataset.state = state.enabled ? "on" : "off";
          }
        }
      };
      state.render = renderState;
      const schedule = () => {
        if (state.timer) window.clearInterval(state.timer);
        state.seconds = 5;
        state.timer = state.enabled ? window.setInterval(() => {
          state.seconds -= 1;
          if (state.seconds <= 0) {
            state.seconds = 5;
            refresh(scope, state);
          }
          renderState();
        }, 1000) : null;
      };
      const onToggle = () => {
        state.enabled = !state.enabled;
        state.failed = false;
        if (!state.enabled) state.pending?.abort();
        renderState();
        schedule();
        if (state.enabled) refresh(scope, state);
      };
      control.addEventListener("click", onToggle);
      renderState();
      schedule();
      cleanups.push(() => {
        control.removeEventListener("click", onToggle);
        if (state.timer) window.clearInterval(state.timer);
        state.pending?.abort();
      });
    });

    const detailsURL = (element, range) => {
      const url = new URL(element.dataset.applicationDetailUrl, location.href);
      url.searchParams.set("range", range);
      return url.href;
    };
    const loadDetails = async (element, range = "1h", force = false) => {
      const url = detailsURL(element, range);
      if (!force && detailCache.has(url)) return detailCache.get(url);
      if (pendingDetails.has(url)) return pendingDetails.get(url);
      const controller = new AbortController();
      const request = fetch(url, {
        credentials: "same-origin",
        cache: "no-store",
        headers: { Accept: "application/json" },
        signal: controller.signal
      }).then(async response => {
        if (!response.ok) {
          const error = await response.json().catch(() => null);
          const code = error?.error?.code || "";
          throw new Error(applicationWords().requestErrors[code] || applicationWords().loadFailed);
        }
        const payload = await response.json();
        detailCache.set(url, payload);
        return payload;
      }).finally(() => pendingDetails.delete(url));
      request.controller = controller;
      pendingDetails.set(url, request);
      return request;
    };

    const selectPinnedTab = (article, tabName) => {
      article.querySelectorAll("[data-application-detail-tab]").forEach(tab => {
        const selected = tab.dataset.applicationDetailTab === tabName;
        tab.setAttribute("aria-selected", String(selected));
        tab.tabIndex = selected ? 0 : -1;
      });
      article.querySelectorAll("[data-application-detail-panel]").forEach(panel => {
        panel.hidden = panel.dataset.applicationDetailPanel !== tabName;
      });
    };

    const renderPinnedDetails = (article, payload, range) => {
      const historyPanel = article.querySelector("[data-application-history-output]");
      const runtimePanel = article.querySelector("[data-application-detail-panel='runtime']");
      if (historyPanel) historyPanel.innerHTML = renderApplicationHistory(payload);
      if (runtimePanel) runtimePanel.innerHTML = renderApplicationRuntime(payload);
      article.querySelectorAll("[data-application-range]").forEach(button => {
        button.setAttribute("aria-pressed", String(button.dataset.applicationRange === range));
      });
      renderIcons(article);
      localizeTimes(article);
    };

    const showPinnedError = (article, range, error) => {
      const target = article.querySelector("[data-application-history-output]");
      if (!target) return;
      target.innerHTML = `<div class="application-detail-error" role="alert"><span data-lucide="triangle-alert" aria-hidden="true"></span><div><strong>${escapeMarkup(applicationWords().loadFailed)}</strong><p>${escapeMarkup(error?.message || "")}</p><button class="button button--compact" type="button" data-application-detail-retry data-range="${escapeMarkup(range)}">${escapeMarkup(applicationWords().retry)}</button></div></div>`;
      renderIcons(target);
    };

    const expandPinned = async (article, range = "1h", tab = "history") => {
      const detail = article.querySelector("[data-application-detail]");
      const toggle = article.querySelector("[data-application-detail-toggle]");
      if (!detail || !toggle) return;
      article.dataset.expanded = "true";
      detail.hidden = false;
      toggle.setAttribute("aria-expanded", "true");
      selectPinnedTab(article, tab);
      const historyPanel = article.querySelector("[data-application-history-output]");
      const runtimePanel = article.querySelector("[data-application-detail-panel='runtime']");
      if (historyPanel) historyPanel.innerHTML = applicationDetailLoading();
      if (runtimePanel) runtimePanel.innerHTML = applicationDetailLoading();
      renderIcons(detail);
      try {
        const payload = await loadDetails(article, range);
        if (article.dataset.expanded === "true") renderPinnedDetails(article, payload, range);
      } catch (error) {
        showPinnedError(article, range, error);
        if (runtimePanel) runtimePanel.innerHTML = `<div class="application-detail-empty">${escapeMarkup(error?.message || applicationWords().unavailable)}</div>`;
      }
    };

    const collapsePinned = article => {
      article.dataset.expanded = "false";
      const detail = article.querySelector("[data-application-detail]");
      if (detail) detail.hidden = true;
      article.querySelector("[data-application-detail-toggle]")?.setAttribute("aria-expanded", "false");
    };

    restorePinnedState = expanded => {
      expanded.forEach(item => {
        const article = Array.from(root.querySelectorAll(".pinned-application")).find(candidate => candidate.dataset.applicationId === item.id);
        if (article) expandPinned(article, item.range, item.tab);
      });
    };

    const runningDetailRow = row => {
      const id = row.dataset.applicationId;
      return Array.from(root.querySelectorAll("[data-running-detail-for]")).find(detail => detail.dataset.runningDetailFor === id);
    };
    const closeRunningDetails = except => {
      root.querySelectorAll("[data-application-row][aria-expanded='true']").forEach(row => {
        if (row === except) return;
        row.setAttribute("aria-expanded", "false");
        const detail = runningDetailRow(row);
        if (detail) detail.hidden = true;
      });
    };
    const toggleRunningDetail = async row => {
      const detail = runningDetailRow(row);
      if (!detail) return;
      const expanded = row.getAttribute("aria-expanded") === "true";
      if (expanded) {
        row.setAttribute("aria-expanded", "false");
        detail.hidden = true;
        return;
      }
      closeRunningDetails(row);
      row.setAttribute("aria-expanded", "true");
      detail.hidden = false;
      const target = detail.querySelector("[data-running-detail-content]");
      if (target) target.innerHTML = applicationDetailLoading();
      renderIcons(detail);
      try {
        const payload = await loadDetails(row, "15m");
        if (row.getAttribute("aria-expanded") === "true" && target) {
          target.innerHTML = renderApplicationRuntime(payload);
          renderIcons(target);
        }
      } catch (error) {
        if (target) target.innerHTML = `<div class="application-detail-error" role="alert"><span data-lucide="triangle-alert" aria-hidden="true"></span><div><strong>${escapeMarkup(applicationWords().loadFailed)}</strong><p>${escapeMarkup(error?.message || "")}</p><button class="button button--compact" type="button" data-running-detail-retry>${escapeMarkup(applicationWords().retry)}</button></div></div>`;
        renderIcons(detail);
      }
    };

    const onApplicationsClick = async event => {
      const copyButton = event.target.closest("[data-copy-runtime-command]");
      if (copyButton) {
        const command = copyButton.closest("dd")?.querySelector("[data-runtime-command]")?.textContent || "";
        const original = copyButton.innerHTML;
        try {
          await navigator.clipboard.writeText(command.trim());
          copyButton.textContent = copyButton.dataset.copiedLabel;
          window.setTimeout(() => {
            copyButton.innerHTML = original;
          }, 1500);
        } catch {
          copyButton.focus();
        }
        return;
      }
      const retryPinned = event.target.closest("[data-application-detail-retry]");
      if (retryPinned) {
        const article = retryPinned.closest(".pinned-application");
        if (article) {
          const url = detailsURL(article, retryPinned.dataset.range || "1h");
          detailCache.delete(url);
          expandPinned(article, retryPinned.dataset.range || "1h", "history");
        }
        return;
      }
      const retryRunning = event.target.closest("[data-running-detail-retry]");
      if (retryRunning) {
        const detail = retryRunning.closest("[data-running-detail-for]");
        const row = Array.from(root.querySelectorAll("[data-application-row]")).find(candidate => candidate.dataset.applicationId === detail?.dataset.runningDetailFor);
        if (row) {
          detailCache.delete(detailsURL(row, "15m"));
          row.setAttribute("aria-expanded", "false");
          toggleRunningDetail(row);
        }
        return;
      }
      const range = event.target.closest("[data-application-range]");
      if (range) {
        const article = range.closest(".pinned-application");
        if (article) expandPinned(article, range.dataset.applicationRange, "history");
        return;
      }
      const tab = event.target.closest("[data-application-detail-tab]");
      if (tab) {
        const article = tab.closest(".pinned-application");
        if (article) selectPinnedTab(article, tab.dataset.applicationDetailTab);
        return;
      }
      const toggle = event.target.closest("[data-application-detail-toggle]");
      if (toggle) {
        const article = toggle.closest(".pinned-application");
        if (!article) return;
        if (article.dataset.expanded === "true") collapsePinned(article);
        else expandPinned(article);
        return;
      }
      const row = event.target.closest("[data-application-row]");
      if (row && !event.target.closest("a,button,input,select,textarea,form,summary,details")) toggleRunningDetail(row);
    };

    const onApplicationsKeydown = event => {
      const row = event.target.closest("[data-application-row]");
      if (row && event.target === row && (event.key === "Enter" || event.key === " ")) {
        event.preventDefault();
        toggleRunningDetail(row);
      }
      const tab = event.target.closest("[data-application-detail-tab]");
      if (tab && (event.key === "ArrowLeft" || event.key === "ArrowRight")) {
        const tabs = Array.from(tab.parentElement.querySelectorAll("[data-application-detail-tab]"));
        const next = tabs[(tabs.indexOf(tab) + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length];
        next.focus();
        next.click();
      }
    };
    root.addEventListener("click", onApplicationsClick);
    root.addEventListener("keydown", onApplicationsKeydown);
    cleanups.push(() => {
      root.removeEventListener("click", onApplicationsClick);
      root.removeEventListener("keydown", onApplicationsKeydown);
      pendingDetails.forEach(request => request.controller?.abort());
      pendingDetails.clear();
    });
  }

  function initRun(cleanups) {
    const root = document.querySelector("[data-run-events-url]");
    if (!root || !window.EventSource) return;
    const log = root.querySelector("[data-run-log]");
    const state = root.querySelector("[data-run-live-state]");
    const pause = root.querySelector("[data-run-pause]");
    const pauseLabel = pause?.querySelector("[data-run-pause-label]");
    let paused = false;
    let completed = false;
    let buffer = [];
    let lastSequence = Array.from(log.querySelectorAll("[data-sequence]")).reduce(
      (highest, entry) => Math.max(highest, Number(entry.dataset.sequence) || 0),
      0,
    );
    const append = payload => {
      const sequence = Number(payload.sequence);
      if (Number.isSafeInteger(sequence) && sequence > 0) {
        if (sequence <= lastSequence) return;
        lastSequence = sequence;
      }
      const span = document.createElement("span");
      if (Number.isSafeInteger(sequence) && sequence > 0) span.dataset.sequence = String(sequence);
      span.dataset.source = payload.source || "stdout";
      span.textContent = payload.text || "";
      log.append(span);
      log.scrollTop = log.scrollHeight;
    };
    const finishPauseControl = () => {
      pause?.remove();
      root._toggleLogPause = null;
    };
    const eventsURL = new URL(root.dataset.runEventsUrl, location.href);
    eventsURL.searchParams.set("after", String(lastSequence));
    const source = new EventSource(eventsURL);
    source.addEventListener("open", () => { if (state) state.textContent = words().connected; });
    source.addEventListener("output", event => {
      const payload = { ...JSON.parse(event.data), sequence: Number(event.lastEventId) };
      if (paused) buffer.push(payload); else append(payload);
    });
    source.addEventListener("complete", event => {
      completed = true;
      source.close();
      if (state) state.textContent = words().complete;
      const status = root.querySelector("[data-run-status]");
      if (status) {
        const runState = event.data.trim();
        const dot = document.createElement("span");
        dot.className = "status-dot";
        dot.setAttribute("aria-hidden", "true");
        status.dataset.state = runState;
        status.replaceChildren(dot, document.createTextNode(words().statuses[runState] || runState));
      }
      root.querySelector("[data-run-stop-form]")?.remove();
      if (!paused || buffer.length === 0) finishPauseControl();
    });
    source.addEventListener("error", () => { if (state && source.readyState !== EventSource.CLOSED) state.textContent = words().disconnected; });
    const toggle = () => {
      if (completed && !paused) return;
      paused = !paused;
      if (pause) {
        if (pauseLabel) pauseLabel.textContent = paused ? pause.dataset.resumeLabel : pause.dataset.pauseLabel;
        pause.querySelector("svg")?.replaceWith(makeIcon(paused ? "play" : "pause"));
      }
      if (!paused) {
        buffer.forEach(append);
        buffer = [];
        if (completed) finishPauseControl();
      }
    };
    pause?.addEventListener("click", toggle);
    root._toggleLogPause = toggle;
    cleanups.push(() => {
      source.close();
      pause?.removeEventListener("click", toggle);
      if (root._toggleLogPause === toggle) root._toggleLogPause = null;
    });
  }

  function initStatus() {
    const status = document.querySelector("[data-shell-status]");
    const websiteStatus = document.querySelector("[data-shell-website-status]");
    if (!status && !websiteStatus) return;
    const updateWebsiteStatus = data => {
      if (!websiteStatus) return;
      const down = Math.max(0, Number(data.websiteDown) || 0);
      const verifying = Math.max(0, Number(data.websiteVerifying) || 0);
      websiteStatus.dataset.state = data.websiteState;
      websiteStatus.dataset.down = String(down);
      websiteStatus.dataset.verifying = String(verifying);
      const primary = websiteStatus.querySelector("[data-shell-website-primary]");
      const secondary = websiteStatus.querySelector("[data-shell-website-secondary]");
      if (down > 0) {
        if (primary) primary.textContent = `${down} ${down === 1 ? words().websiteDownOne : words().websiteDownMany}`;
        if (secondary) secondary.textContent = `${verifying} ${verifying === 1 ? words().websiteVerifyingOne : words().websiteVerifyingMany}`;
      } else if (verifying > 0) {
        if (primary) primary.textContent = `${verifying} ${verifying === 1 ? words().websiteVerifyingOne : words().websiteVerifyingMany}`;
        if (secondary) secondary.textContent = words().websiteNoConfirmedFailure;
      } else {
        if (primary) primary.textContent = words().websiteNormal;
        if (secondary) secondary.textContent = words().websiteNoOpenIssues;
      }
    };
    const update = async () => {
      try {
        const response = await fetch("/monitor/status", { credentials: "same-origin", cache: "no-store" });
        if (!response.ok) return;
        const data = await response.json();
        if (status) {
          status.dataset.state = data.state;
          const strong = status.querySelector("strong");
          const small = status.querySelector("small");
          if (strong) strong.textContent = words()[data.state] || data.state;
          if (small) {
            const environment = small.textContent.split("·")[0].trim();
            small.textContent = `${environment} · ${data.activeRuns} ${words().activeRuns}`;
          }
        }
        updateWebsiteStatus(data);
      } catch { /* status remains at last known value */ }
    };
    const timer = window.setInterval(update, 30000);
    window.addEventListener("pagehide", () => window.clearInterval(timer), { once: true });
  }

  function setControlIcon(target, name) {
    if (target) target.replaceChildren(makeIcon(name));
  }

  function initPasswordControls(root = document, cleanups = []) {
    root.querySelectorAll("[data-password-value]").forEach(container => {
      const content = container.querySelector("[data-password-content]");
      const mask = container.querySelector("[data-password-mask]");
      const toggle = container.querySelector("[data-toggle-password]");
      const toggleIcon = container.querySelector("[data-password-toggle-icon]");
      if (!content || !mask || !toggle) return;

      container.hidden = false;

      const onToggle = () => {
        const reveal = content.hidden;
        content.hidden = !reveal;
        mask.hidden = reveal;
        toggle.setAttribute("aria-expanded", String(reveal));
        toggle.setAttribute("aria-label", reveal ? toggle.dataset.hideLabel : toggle.dataset.showLabel);
        toggle.dataset.tooltip = reveal ? toggle.dataset.hideTooltip : toggle.dataset.showTooltip;
        setControlIcon(toggleIcon, reveal ? "eye-off" : "eye");
      };
      toggle.addEventListener("click", onToggle);
      cleanups.push(() => toggle.removeEventListener("click", onToggle));
    });
  }

  function initCopyControls(root = document, cleanups = []) {
    const feedbackTimers = new Map();

    root.querySelectorAll("[data-copy-text]").forEach(copyButton => {
      const content = document.getElementById(copyButton.dataset.copyTarget);
      const copyIcon = copyButton.querySelector("[data-copy-icon]");
      const status = copyButton.closest("[data-copy-field]")?.querySelector("[data-copy-status]");
      if (!content || !copyIcon) return;

      copyButton.hidden = false;
      const onCopy = async () => {
        if (copyButton.dataset.copying === "true") return;
        const existingTimer = feedbackTimers.get(copyButton);
        if (existingTimer) {
          window.clearTimeout(existingTimer);
          feedbackTimers.delete(copyButton);
        }
        copyButton.dataset.copying = "true";
        copyButton.setAttribute("aria-busy", "true");

        try {
          if (!navigator.clipboard?.writeText) throw new Error("Clipboard API unavailable");
          await navigator.clipboard.writeText(content.textContent);
          copyButton.dataset.state = "success";
          copyButton.setAttribute("aria-label", copyButton.dataset.copiedLabel);
          copyButton.dataset.tooltip = copyButton.dataset.copiedLabel;
          setControlIcon(copyIcon, "check");
          if (status) status.textContent = copyButton.dataset.copiedLabel;
        } catch {
          copyButton.dataset.state = "error";
          copyButton.setAttribute("aria-label", copyButton.dataset.copyFailedLabel);
          copyButton.dataset.tooltip = copyButton.dataset.copyFailedLabel;
          setControlIcon(copyIcon, "triangle-alert");
          if (status) status.textContent = copyButton.dataset.copyFailedLabel;
        } finally {
          copyButton.removeAttribute("aria-busy");
          delete copyButton.dataset.copying;
        }

        const timer = window.setTimeout(() => {
          copyButton.removeAttribute("data-state");
          copyButton.setAttribute("aria-label", copyButton.dataset.copyLabel);
          copyButton.dataset.tooltip = copyButton.dataset.copyTooltip;
          setControlIcon(copyIcon, "copy");
          if (status) status.textContent = "";
          feedbackTimers.delete(copyButton);
        }, 1600);
        feedbackTimers.set(copyButton, timer);
      };
      copyButton.addEventListener("click", onCopy);
      cleanups.push(() => copyButton.removeEventListener("click", onCopy));
    });

    cleanups.push(() => feedbackTimers.forEach(timer => window.clearTimeout(timer)));
  }

  function initFileDropUpload(root = document, cleanups = []) {
    root.querySelectorAll("[data-file-drop-form]").forEach(form => {
      const zone = form.querySelector("[data-file-drop-zone]");
      const input = form.querySelector(".file-drop-input");
      const title = form.querySelector("[data-file-drop-title]");
      const status = form.querySelector("[data-file-drop-status]");
      if (!zone || !input || !title || !status) return;

      let dragDepth = 0;
      const isFileDrag = dataTransfer => Array.from(dataTransfer?.types || []).includes("Files");
      const setState = (state, nextTitle, nextDescription) => {
        if (state) zone.dataset.state = state;
        else zone.removeAttribute("data-state");
        title.textContent = nextTitle;
        status.textContent = nextDescription;
      };
      const resetState = () => setState("", form.dataset.defaultTitle, form.dataset.defaultDescription);
      const showError = message => setState("error", form.dataset.defaultTitle, message);
      const containsDirectory = dataTransfer => Array.from(dataTransfer?.items || []).some(item => {
        if (item.kind !== "file" || typeof item.webkitGetAsEntry !== "function") return false;
        return item.webkitGetAsEntry()?.isDirectory === true;
      });
      const submitFiles = files => {
        if (!files?.length) return;
        if (files.length > 100) {
          input.value = "";
          showError(form.dataset.countError);
          return;
        }
        setState("uploading", form.dataset.uploadingTitle, form.dataset.uploadingDescription);
        form.setAttribute("aria-busy", "true");
        window.requestAnimationFrame(() => form.requestSubmit());
      };

      const onDragEnter = event => {
        if (!isFileDrag(event.dataTransfer)) return;
        event.preventDefault();
        dragDepth += 1;
        setState("active", form.dataset.activeTitle, form.dataset.activeDescription);
      };
      const onDragOver = event => {
        if (!isFileDrag(event.dataTransfer)) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = "copy";
      };
      const onDragLeave = event => {
        if (!isFileDrag(event.dataTransfer)) return;
        event.preventDefault();
        dragDepth = Math.max(0, dragDepth - 1);
        if (dragDepth === 0) resetState();
      };
      const onDrop = event => {
        if (!isFileDrag(event.dataTransfer)) return;
        event.preventDefault();
        dragDepth = 0;
        if (containsDirectory(event.dataTransfer)) {
          showError(form.dataset.directoryError);
          return;
        }
        try {
          input.files = event.dataTransfer.files;
        } catch {
          showError(form.dataset.inputError);
          return;
        }
        if (input.files.length !== event.dataTransfer.files.length) {
          input.value = "";
          showError(form.dataset.inputError);
          return;
        }
        submitFiles(input.files);
      };
      const onChange = () => submitFiles(input.files);
      const preventFileNavigation = event => {
        if (!isFileDrag(event.dataTransfer) || zone.contains(event.target)) return;
        event.preventDefault();
      };

      zone.addEventListener("dragenter", onDragEnter);
      zone.addEventListener("dragover", onDragOver);
      zone.addEventListener("dragleave", onDragLeave);
      zone.addEventListener("drop", onDrop);
      input.addEventListener("change", onChange);
      document.addEventListener("dragover", preventFileNavigation);
      document.addEventListener("drop", preventFileNavigation);
      cleanups.push(() => {
        zone.removeEventListener("dragenter", onDragEnter);
        zone.removeEventListener("dragover", onDragOver);
        zone.removeEventListener("dragleave", onDragLeave);
        zone.removeEventListener("drop", onDrop);
        input.removeEventListener("change", onChange);
        document.removeEventListener("dragover", preventFileNavigation);
        document.removeEventListener("drop", preventFileNavigation);
      });
    });
  }

  function initGroupedRecords(cleanups) {
    document.querySelectorAll("[data-grouped-records]").forEach(page => {
      const namespace = page.dataset.groupedRecords;
      if (!namespace) return;
      const storageKey = `scriptboard.${namespace}.collapsed`;
      let collapsed = new Set();
      try {
        const stored = JSON.parse(localStorage.getItem(storageKey) || "[]");
        if (Array.isArray(stored)) collapsed = new Set(stored.filter(value => typeof value === "string"));
      } catch {
        collapsed = new Set();
      }

      const persist = () => {
        try {
          localStorage.setItem(storageKey, JSON.stringify([...collapsed]));
        } catch {
          // Storage may be unavailable in hardened or private browser sessions.
        }
      };

      page.querySelectorAll("[data-record-group]").forEach(group => {
        const id = group.dataset.recordGroup;
        const toggle = group.querySelector("[data-group-toggle]");
        const body = group.querySelector("[data-group-body]");
        if (!id || !toggle || !body) return;

        const render = isCollapsed => {
          group.classList.toggle("is-collapsed", isCollapsed);
          toggle.setAttribute("aria-expanded", String(!isCollapsed));
          body.hidden = isCollapsed;
        };
        render(collapsed.has(id));

        const onToggle = () => {
          if (collapsed.has(id)) collapsed.delete(id);
          else collapsed.add(id);
          render(collapsed.has(id));
          persist();
        };
        toggle.addEventListener("click", onToggle);
        cleanups.push(() => toggle.removeEventListener("click", onToggle));
      });
    });
  }

  function initScheduleCron(cleanups, root = document) {
    const form = root.querySelector("[data-schedule-form]");
    if (!form) return;
    const input = form.querySelector("[data-cron-expression]");
    const feedback = form.querySelector("[data-cron-feedback]");
    const presets = Array.from(form.querySelectorAll("[data-cron-preset]"));
    const guided = form.querySelector("[data-cron-guided]");
    const modeButtons = Array.from(form.querySelectorAll("[data-cron-mode]"));
    const sentenceLead = form.querySelector("[data-cron-sentence-lead]");
    const intervalInput = form.querySelector("[data-cron-interval]");
    const unitSelect = form.querySelector("[data-cron-unit]");
    const monthDayInput = form.querySelector("[data-cron-month-day]");
    const timeInput = form.querySelector("[data-cron-guided-time-input]");
    const weekdays = form.querySelector("[data-cron-weekdays]");
    const currentExpression = form.querySelector("[data-cron-current]");
    const guidedStatus = form.querySelector("[data-cron-guided-status]");
    const customNote = form.querySelector("[data-cron-custom-note]");
    const parseButton = form.querySelector("[data-cron-parse]");
    if (!input || !feedback) return;

    let debounceTimer = 0;
    let requestController = null;
    let requestSequence = 0;
    let guidedMode = "";

    const node = (tag, className, text) => {
      const element = document.createElement(tag);
      if (className) element.className = className;
      if (text !== undefined) element.textContent = text;
      return element;
    };
    const normalizedInput = () => input.value.trim().replace(/\s+/g, " ").toUpperCase();
    const setGuidedStatus = (message, state = "") => {
      if (!guidedStatus) return;
      guidedStatus.textContent = message;
      guidedStatus.dataset.state = state;
    };
    const setCurrentExpression = expression => {
      if (currentExpression) currentExpression.textContent = expression || "—";
    };
    const syncPresets = () => {
      const value = normalizedInput();
      presets.forEach(button => {
        const active = value === button.dataset.cronPreset.toUpperCase();
        button.classList.toggle("is-active", active);
        button.setAttribute("aria-pressed", String(active));
      });
    };
    const setGuidedMode = mode => {
      guidedMode = mode;
      modeButtons.forEach(button => {
        const active = button.dataset.cronMode === mode;
        button.classList.toggle("is-active", active);
        button.setAttribute("aria-pressed", String(active));
        if (active && sentenceLead) sentenceLead.textContent = button.textContent.trim();
      });
      form.querySelectorAll("[data-cron-field]").forEach(field => {
        const kind = field.dataset.cronField;
        const visible = (kind === "interval" || kind === "unit") ? mode === "interval" :
          kind === "month-day" ? mode === "monthly" :
            kind === "time" ? mode !== "interval" && mode !== "custom" : false;
        field.hidden = !visible;
      });
      if (weekdays) weekdays.hidden = mode !== "weekly";
      if (customNote) customNote.hidden = mode !== "custom";
      if (mode === "custom" && sentenceLead) sentenceLead.textContent = "Cron";
    };
    const setTime = (hour, minute) => {
      if (!timeInput) return;
      timeInput.value = `${String(hour).padStart(2, "0")}:${String(minute).padStart(2, "0")}`;
    };
    const cronWeekdayNames = ["SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"];
    const toWeekday = value => {
      if (cronWeekdayNames.includes(value)) return cronWeekdayNames.indexOf(value);
      if (!/^\d+$/.test(value)) return null;
      const numeric = Number(value);
      if (numeric === 7) return 0;
      return numeric >= 0 && numeric <= 6 ? numeric : null;
    };
    const expandWeekdays = field => {
      const result = new Set();
      for (const part of field.split(",")) {
        const range = part.split("-");
        if (range.length === 1) {
          const day = toWeekday(range[0]);
          if (day === null) return null;
          result.add(day);
          continue;
        }
        if (range.length !== 2) return null;
        const start = toWeekday(range[0]);
        const end = toWeekday(range[1]);
        if (start === null || end === null || start > end) return null;
        for (let day = start; day <= end; day += 1) result.add(day);
      }
      return Array.from(result).sort((left, right) => left - right);
    };
    const selectWeekdays = selectedDays => {
      if (!weekdays) return;
      weekdays.querySelectorAll("[data-cron-weekday]").forEach(button => {
        const selected = selectedDays.includes(Number(button.dataset.cronWeekday));
        button.classList.toggle("is-active", selected);
        button.setAttribute("aria-pressed", String(selected));
      });
    };
    const syncGuidedFromExpression = expression => {
      if (!guided) return false;
      const fields = expression.trim().replace(/\s+/g, " ").toUpperCase().split(" ");
      if (fields.length !== 5) return false;
      const [minuteField, hourField, dayField, monthField, weekdayField] = fields;
      const minuteInterval = minuteField.match(/^\*\/(\d+)$/);
      if (minuteInterval && Number(minuteInterval[1]) <= 59 &&
          hourField === "*" && dayField === "*" && monthField === "*" && weekdayField === "*") {
        setGuidedMode("interval");
        intervalInput.value = minuteInterval[1];
        unitSelect.value = "minute";
      } else if (minuteField === "*" && hourField === "*" && dayField === "*" && monthField === "*" && weekdayField === "*") {
        setGuidedMode("interval");
        intervalInput.value = "1";
        unitSelect.value = "minute";
      } else {
        const hourInterval = hourField.match(/^\*\/(\d+)$/);
        if (minuteField === "0" && hourInterval && Number(hourInterval[1]) <= 23 &&
            dayField === "*" && monthField === "*" && weekdayField === "*") {
          setGuidedMode("interval");
          intervalInput.value = hourInterval[1];
          unitSelect.value = "hour";
        } else if (minuteField === "0" && hourField === "*" && dayField === "*" && monthField === "*" && weekdayField === "*") {
          setGuidedMode("interval");
          intervalInput.value = "1";
          unitSelect.value = "hour";
        } else if (/^\d+$/.test(minuteField) && /^\d+$/.test(hourField) && monthField === "*") {
          setTime(Number(hourField), Number(minuteField));
          if (dayField === "*" && weekdayField === "*") {
            setGuidedMode("daily");
          } else if (dayField === "*" && weekdayField !== "*") {
            const parsedDays = expandWeekdays(weekdayField);
            if (!parsedDays?.length) {
              setGuidedMode("custom");
              setCurrentExpression(expression);
              setGuidedStatus(form.dataset.cronGuidedCustom);
              return false;
            }
            selectWeekdays(parsedDays);
            setGuidedMode("weekly");
          } else if (/^\d+$/.test(dayField) && weekdayField === "*") {
            monthDayInput.value = String(Number(dayField));
            setGuidedMode("monthly");
          } else {
            setGuidedMode("custom");
            setCurrentExpression(expression);
            setGuidedStatus(form.dataset.cronGuidedCustom);
            return false;
          }
        } else {
          setGuidedMode("custom");
          setCurrentExpression(expression);
          setGuidedStatus(form.dataset.cronGuidedCustom);
          return false;
        }
      }
      setCurrentExpression(expression);
      setGuidedStatus(form.dataset.cronGuidedParsed);
      return true;
    };
    const buildGuidedExpression = () => {
      if (guidedMode === "interval") {
        const amount = Math.max(1, Number(intervalInput.value) || 1);
        return unitSelect.value === "hour" ? `0 */${amount} * * *` : `*/${amount} * * * *`;
      }
      const time = timeInput.value || "02:00";
      const [hour, minute] = time.split(":").map(Number);
      if (guidedMode === "weekly") {
        let selected = Array.from(weekdays.querySelectorAll("[data-cron-weekday][aria-pressed='true']"))
          .map(button => button.dataset.cronWeekday);
        if (!selected.length) {
          selected = ["1"];
          selectWeekdays([1]);
        }
        return `${minute} ${hour} * * ${selected.join(",")}`;
      }
      if (guidedMode === "monthly") {
        const day = Math.min(31, Math.max(1, Number(monthDayInput.value) || 1));
        return `${minute} ${hour} ${day} * *`;
      }
      return `${minute} ${hour} * * *`;
    };
    const renderMessage = (state, iconName, message, alert = false) => {
      feedback.dataset.state = state;
      const wrapper = node("div", "cron-feedback__message");
      if (alert) wrapper.setAttribute("role", "alert");
      const icon = node("span");
      icon.dataset.lucide = iconName;
      icon.setAttribute("aria-hidden", "true");
      wrapper.append(icon, node("p", "", message));
      feedback.replaceChildren(wrapper);
      renderIcons(feedback);
    };
    const renderValid = payload => {
      feedback.dataset.state = "valid";
      input.removeAttribute("aria-invalid");
      const result = node("div", "cron-feedback__result");
      const header = node("header");
      const lead = node("div");
      const icon = node("span");
      icon.dataset.lucide = "calendar-clock";
      icon.setAttribute("aria-hidden", "true");
      const copy = node("div");
      copy.append(node("strong", "", payload.summary), node("small", "", payload.timezone));
      lead.append(icon, copy);
      header.append(lead, node("span", "", form.dataset.cronNextLabel));
      result.append(header);
      if (payload.day_or_warning) {
        const warning = node("p", "cron-day-warning");
        const warningIcon = node("span");
        warningIcon.dataset.lucide = "triangle-alert";
        warningIcon.setAttribute("aria-hidden", "true");
        warning.append(warningIcon, document.createTextNode(payload.day_or_warning));
        result.append(warning);
      }
      const times = node("ol");
      (payload.next || []).forEach(item => {
        const listItem = node("li");
        const time = node("time", "", item.label);
        time.dateTime = item.datetime;
        time.dataset.cronTime = "";
        listItem.append(time);
        times.append(listItem);
      });
      result.append(times);
      feedback.replaceChildren(result);
      renderIcons(feedback);
    };
    const renderIdle = () => {
      input.removeAttribute("aria-invalid");
      renderMessage("idle", "info", form.dataset.cronIdle);
    };

    const preview = async ({ normalize = false } = {}) => {
      window.clearTimeout(debounceTimer);
      const expression = input.value.trim();
      if (!expression) {
        requestController?.abort();
        renderIdle();
        syncPresets();
        return;
      }
      requestController?.abort();
      requestController = new AbortController();
      const sequence = ++requestSequence;
      renderMessage("pending", "calendar-clock", form.dataset.cronPending);
      try {
        const response = await fetch(form.dataset.previewAction, {
          method: "POST",
          body: new FormData(form),
          credentials: "same-origin",
          cache: "no-store",
          headers: { "Accept": "application/json" },
          signal: requestController.signal,
        });
        const payload = await response.json();
        if (sequence !== requestSequence) return;
        if (response.ok && payload.valid) {
          if (normalize && payload.normalized_expression) input.value = payload.normalized_expression;
          syncPresets();
          const normalized = payload.normalized_expression || normalizedInput();
          setCurrentExpression(normalized);
          syncGuidedFromExpression(normalized);
          renderValid(payload);
          return payload;
        }
        input.setAttribute("aria-invalid", "true");
        setGuidedStatus(payload.error || form.dataset.cronUnavailable, "error");
        renderMessage("invalid", "circle-x", payload.error || form.dataset.cronUnavailable, true);
      } catch (error) {
        if (error.name === "AbortError" || sequence !== requestSequence) return;
        input.removeAttribute("aria-invalid");
        renderMessage("unavailable", "triangle-alert", form.dataset.cronUnavailable);
      }
    };

    const onInput = () => {
      requestController?.abort();
      requestController = null;
      requestSequence += 1;
      syncPresets();
      setCurrentExpression(normalizedInput());
      setGuidedStatus(form.dataset.cronGuidedWaiting);
      window.clearTimeout(debounceTimer);
      if (!input.value.trim()) {
        renderIdle();
        return;
      }
      renderMessage("pending", "calendar-clock", form.dataset.cronPending);
      debounceTimer = window.setTimeout(() => preview(), 300);
    };
    const onBlur = () => preview({ normalize: true });
    const onSubmit = event => {
      if (!event.submitter?.matches("[data-cron-preview-submit]")) return;
      event.preventDefault();
      preview({ normalize: true });
    };
    const onPreset = event => {
      input.value = event.currentTarget.dataset.cronPreset;
      input.focus();
      syncPresets();
      preview({ normalize: true });
    };
    const onMode = event => {
      setGuidedMode(event.currentTarget.dataset.cronMode);
      const expression = buildGuidedExpression();
      input.value = expression;
      setCurrentExpression(expression);
      setGuidedStatus(form.dataset.cronGuidedSynced);
      preview({ normalize: true });
    };
    const onGuidedControl = () => {
      if (!guidedMode || guidedMode === "custom") return;
      const expression = buildGuidedExpression();
      input.value = expression;
      setCurrentExpression(expression);
      setGuidedStatus(form.dataset.cronGuidedSynced);
      preview({ normalize: true });
    };
    const onWeekday = event => {
      const selected = event.currentTarget.getAttribute("aria-pressed") !== "true";
      event.currentTarget.classList.toggle("is-active", selected);
      event.currentTarget.setAttribute("aria-pressed", String(selected));
      onGuidedControl();
    };
    const onParse = () => {
      input.focus();
      preview({ normalize: true });
    };

    input.addEventListener("input", onInput);
    input.addEventListener("blur", onBlur);
    form.addEventListener("submit", onSubmit);
    presets.forEach(button => button.addEventListener("click", onPreset));
    modeButtons.forEach(button => button.addEventListener("click", onMode));
    [intervalInput, unitSelect, monthDayInput, timeInput].filter(Boolean).forEach(control => {
      control.addEventListener("input", onGuidedControl);
      control.addEventListener("change", onGuidedControl);
    });
    weekdays?.querySelectorAll("[data-cron-weekday]").forEach(button => button.addEventListener("click", onWeekday));
    parseButton?.addEventListener("click", onParse);
    syncPresets();
    if (guided) {
      guided.hidden = false;
      if (!syncGuidedFromExpression(normalizedInput())) setGuidedMode(normalizedInput() ? "custom" : "daily");
    }
    if (input.value.trim() && feedback.dataset.state === "idle") preview();

    cleanups.push(() => {
      window.clearTimeout(debounceTimer);
      requestController?.abort();
      input.removeEventListener("input", onInput);
      input.removeEventListener("blur", onBlur);
      form.removeEventListener("submit", onSubmit);
      presets.forEach(button => button.removeEventListener("click", onPreset));
      modeButtons.forEach(button => button.removeEventListener("click", onMode));
      [intervalInput, unitSelect, monthDayInput, timeInput].filter(Boolean).forEach(control => {
        control.removeEventListener("input", onGuidedControl);
        control.removeEventListener("change", onGuidedControl);
      });
      weekdays?.querySelectorAll("[data-cron-weekday]").forEach(button => button.removeEventListener("click", onWeekday));
      parseButton?.removeEventListener("click", onParse);
    });
  }

  function pingPayloadSize(format,value){
    try{
      if(format==='none')return 0;
      if(format==='text')return new TextEncoder().encode(value).byteLength;
      if(format==='hex'){
        const compact=value.replace(/\s/g,'');
        if(compact.length%2||!/^[0-9a-f]*$/i.test(compact))return null;
        return compact.length/2;
      }
      if(format==='base64')return Uint8Array.from(atob(value.trim()),character=>character.charCodeAt(0)).byteLength;
    }catch(_){}
    return null;
  }

  function initWebsiteMonitorForm(form){
    const kind=form.querySelector('[data-monitor-kind]');
    const frequency=form.querySelector('[data-monitor-frequency]');
    const httpFields=form.querySelector('[data-http-monitor-fields]');
    const websocketFields=form.querySelector('[data-websocket-monitor-fields]');
    const url=form.querySelector('[data-monitor-url]');
    const urlHint=form.querySelector('[data-monitor-url-hint]');
    const httpMethod=form.querySelector('[data-http-method]');
    const httpBody=form.querySelector('[data-http-body-field]');
    const httpPostField=form.querySelector('[data-http-post-field]');
    const httpSuccessMode=form.querySelector('[data-http-success-mode]');
    const exactStatuses=form.querySelector('[data-exact-statuses-field]');
    const websocketSuccess=form.querySelector('[data-websocket-success]');
    const messageFields=form.querySelector('[data-websocket-message-fields]');
    const pingFields=form.querySelector('[data-websocket-ping-fields]');
    const sendType=form.querySelector('[data-websocket-send-type]');
    const sendPayload=form.querySelector('[data-websocket-send-payload]');
    const sendPayloadLabel=form.querySelector('[data-send-payload-label]');
    const sendPayloadHint=form.querySelector('[data-send-payload-hint]');
    const receiveType=form.querySelector('[name="receive_type"]');
    const expectedMessage=form.querySelector('[name="expected_message"]');
    const expectedMessageLabel=form.querySelector('[data-expected-message-label]');
    const expectedMessageHint=form.querySelector('[data-expected-message-hint]');
    const pingFormat=form.querySelector('[name="ping_payload_format"]');
    const pingPayload=form.querySelector('[name="ping_payload"]');
    const pingPayloadField=form.querySelector('[data-ping-payload-field]');
    const pingCount=form.querySelector('[data-ping-byte-count]');
    const setSection=(section,visible)=>{
      if(!section)return;
      section.hidden=!visible;
      for(const control of section.querySelectorAll('input,select,textarea'))control.disabled=!visible;
    };
    const updatePingCount=()=>{
      if(!pingCount||!pingFormat||!pingPayload)return;
      const size=pingPayloadSize(pingFormat.value,pingPayload.value);
      pingCount.textContent=size==null?` · ${form.dataset.pingInvalid||'Invalid input format'}`:` · ${form.dataset.pingDecoded||'Decoded'} ${size} / 125 ${form.dataset.pingBytes||'bytes'}`;
      pingCount.classList.toggle('field-error',size==null||size>125);
      pingPayload.setCustomValidity(size==null?(form.dataset.pingFormatMismatch||'The Ping payload does not match the selected input format'):size>125?(form.dataset.pingTooLarge||'The decoded Ping payload cannot exceed 125 bytes'):'');
    };
    const sync=()=>{
      const isWebSocket=kind?.value==='websocket';
      setSection(httpFields,!isWebSocket);
      setSection(websocketFields,isWebSocket);
      if(url)url.placeholder=isWebSocket?(form.dataset.websocketPlaceholder||'wss://example.com/socket'):(form.dataset.httpPlaceholder||'https://example.com/health');
      if(urlHint)urlHint.textContent=isWebSocket?(form.dataset.websocketUrlHint||'Use ws:// or wss://'):(form.dataset.httpUrlHint||'Use http:// or https://');
      if(!isWebSocket){
        const isPost=httpMethod?.value==='POST';
        setSection(httpPostField,isPost);
        setSection(httpBody,isPost);
        setSection(exactStatuses,httpSuccessMode?.value==='exact');
      }else{
        const condition=websocketSuccess?.value;
        const messageCondition=condition==='any-message'||condition==='matching-message';
        setSection(messageFields,messageCondition);
        setSection(pingFields,condition==='ping-pong');
        setSection(sendPayload,messageCondition&&sendType?.value!=='none');
        setSection(expectedMessage?.closest('label'),messageCondition&&condition==='matching-message');
        setSection(pingPayloadField,condition==='ping-pong'&&pingFormat?.value!=='none');
        if(expectedMessage)expectedMessage.required=condition==='matching-message';
        if(receiveType)receiveType.required=condition==='matching-message';
        const sendsBinary=sendType?.value==='binary';
        const receivesBinary=receiveType?.value==='binary';
        if(sendPayloadLabel)sendPayloadLabel.textContent=sendsBinary?(form.dataset.sendBinaryLabel||'Binary payload'):(form.dataset.sendTextLabel||'Text payload');
        if(sendPayloadHint)sendPayloadHint.textContent=sendsBinary?(form.dataset.sendBinaryHint||'Enter Base64-encoded bytes.'):(form.dataset.sendTextHint||'Enter UTF-8 text.');
        if(expectedMessageLabel)expectedMessageLabel.textContent=receivesBinary?(form.dataset.expectedBinaryLabel||'Expected binary message'):(form.dataset.expectedTextLabel||'Expected text message');
        if(expectedMessageHint)expectedMessageHint.textContent=receivesBinary?(form.dataset.expectedBinaryHint||'Enter Base64-encoded bytes.'):(form.dataset.expectedTextHint||'Enter UTF-8 text.');
      }
      updatePingCount();
    };
    const kindChanged=()=>{
      if(frequency){
        if(kind.value==='websocket'&&frequency.value==='60')frequency.value='300';
        else if(kind.value==='http'&&frequency.value==='300')frequency.value='60';
      }
      sync();
    };
    kind?.addEventListener('change',kindChanged);
    httpMethod?.addEventListener('change',sync);
    httpSuccessMode?.addEventListener('change',sync);
    websocketSuccess?.addEventListener('change',sync);
    sendType?.addEventListener('change',sync);
    receiveType?.addEventListener('change',sync);
    pingFormat?.addEventListener('change',sync);
    pingPayload?.addEventListener('input',updatePingCount);
    sync();
    return ()=>{
      kind?.removeEventListener('change',kindChanged);
      httpMethod?.removeEventListener('change',sync);
      httpSuccessMode?.removeEventListener('change',sync);
      websocketSuccess?.removeEventListener('change',sync);
      sendType?.removeEventListener('change',sync);
      receiveType?.removeEventListener('change',sync);
      pingFormat?.removeEventListener('change',sync);
      pingPayload?.removeEventListener('input',updatePingCount);
    };
  }

  function initWebsiteNginx(root){
    const form=root.querySelector('form[action="/monitor/websites/nginx/import"]');
    if(!form)return ()=>{};
    const selectAll=form.querySelector('[data-nginx-select-all]');
    const candidates=[...form.querySelectorAll('[data-nginx-candidate]:not(:disabled)')];
    const count=form.querySelector('[data-nginx-selected-count]');
    const button=form.querySelector('[data-nginx-import-button]');
    const buttonCount=form.querySelector('[data-nginx-import-count]');
    const sync=()=>{
      const selected=candidates.filter(candidate=>candidate.checked).length;
      if(count)count.textContent=String(selected);
      if(buttonCount)buttonCount.textContent=`(${selected})`;
      if(button)button.disabled=selected===0;
      if(selectAll){
        selectAll.checked=candidates.length>0&&selected===candidates.length;
        selectAll.indeterminate=selected>0&&selected<candidates.length;
        selectAll.disabled=candidates.length===0;
      }
    };
    const onSelectAll=()=>{
      candidates.forEach(candidate=>{candidate.checked=selectAll.checked});
      sync();
    };
    const onChange=event=>{
      if(event.target.matches('[data-nginx-candidate]'))sync();
    };
    const onSubmit=async event=>{
      if(!window.fetch||button?.disabled)return;
      event.preventDefault();
      const original=button?.innerHTML;
      if(button){
        button.disabled=true;
        button.setAttribute('aria-busy','true');
        button.textContent=button.dataset.pendingLabel||'';
      }
      try{
        const response=await fetch(form.action,{
          method:'POST',
          body:new FormData(form),
          credentials:'same-origin',
          headers:{Accept:'application/json'}
        });
        const payload=await response.json().catch(()=>null);
        if(!response.ok)throw new Error(payload?.error?.message||'Import failed');
        const imported=payload?.importedCount??0;
        const success=document.createElement('section');
        success.className='nginx-import-success';
        success.setAttribute('role','status');
        success.innerHTML=`<span data-lucide="check" aria-hidden="true"></span><div><h2>${escapeMarkup(root.dataset.successTitle||'Websites added')}</h2><p>${escapeMarkup(imported)} ${escapeMarkup(root.dataset.successDescription||'')}</p></div><a class="button button--primary" href="${escapeMarkup(payload?.redirectURL||'/monitor/websites')}">${escapeMarkup(root.dataset.viewLedger||'View website ledger')}</a>`;
        form.previousElementSibling?.matches('.nginx-warnings')&&form.previousElementSibling.remove();
        form.replaceWith(success);
        renderIcons(success);
        success.querySelector('a')?.focus();
      }catch(error){
        let alert=root.querySelector('.page-error[data-nginx-import-error]');
        if(!alert){
          alert=document.createElement('p');
          alert.className='page-error';
          alert.dataset.nginxImportError='';
          alert.setAttribute('role','alert');
          form.before(alert);
        }
        alert.textContent=error?.message||'Import failed';
        if(button){
          button.innerHTML=original;
          button.removeAttribute('aria-busy');
        }
        sync();
      }
    };
    selectAll?.addEventListener('change',onSelectAll);
    form.addEventListener('change',onChange);
    form.addEventListener('submit',onSubmit);
    sync();
    return ()=>{
      selectAll?.removeEventListener('change',onSelectAll);
      form.removeEventListener('change',onChange);
      form.removeEventListener('submit',onSubmit);
    };
  }

  function websiteSnapshotChanged(root,payload){
    if(root.matches('[data-website-detail]')){
      return root.dataset.monitorState!==payload.State||
        root.dataset.monitorLatest!==payload.LatestLabel||
        root.dataset.monitorSummary!==payload.LatestSummary||
        root.dataset.monitorLatency!==payload.LatencyLabel||
        root.dataset.monitorChecked!==String(payload.CheckedToken);
    }
    const monitors=payload.monitors||payload.Monitors||[];
    const counts=payload.counts||payload.Counts||{};
    const alerts=payload.alerts||payload.Alerts||[];
    const countsToken=[
      counts.up??counts.Up??0,
      counts.verifying??counts.Verifying??0,
      counts.down??counts.Down??0,
      counts.paused??counts.Paused??0,
      payload.needsCare??payload.NeedsCare??0,
      alerts.length
    ].join(':');
    if(root.dataset.countsToken&&root.dataset.countsToken!==countsToken)return true;
    const rows=[...root.querySelectorAll('[data-monitor-id]')];
    if(rows.length!==monitors.length)return true;
    const current=new Map(rows.map(row=>[row.dataset.monitorId,row]));
    return monitors.some(monitor=>{
      const row=current.get(monitor.ID);
      return !row||row.dataset.monitorState!==monitor.State||
        row.dataset.monitorLatest!==monitor.LatestLabel||
        row.dataset.monitorSummary!==monitor.LatestSummary||
        row.dataset.monitorLatency!==monitor.LatencyLabel||
        row.dataset.monitorChecked!==String(monitor.CheckedToken);
    });
  }

  function initWebsiteMonitoring(root){
    let stopped=false;
    let busy=false;
    let dragged;
    let checkController;
    const status=root.querySelector('[data-reorder-status]');
    const refreshStatus=root.querySelector('[data-website-refresh-status]');
    const ledger=root.querySelector('[data-website-ledger]');
    const initialOrder=[...root.querySelectorAll('[data-monitor-id]')].map(row=>row.dataset.monitorId);
    const rows=()=>[...root.querySelectorAll('[data-monitor-id]')];
    const updateMoveButtons=()=>{
      const current=rows();
      current.forEach((row,index)=>{
        const buttons=row.querySelectorAll('[data-reorder-move-form] button');
        if(buttons[0])buttons[0].disabled=index===0;
        if(buttons[1])buttons[1].disabled=index===current.length-1;
      });
    };
    const markOrderChanged=()=>{
      updateMoveButtons();
      if(status)status.textContent='';
    };
    const saveOrder=async()=>{
      if(!root.dataset.reorderUrl)return;
      const body=new URLSearchParams({csrf_token:root.dataset.csrfToken||''});
      for(const row of rows())body.append('id',row.dataset.monitorId);
      if(status)status.textContent=root.dataset.reorderSaving||'Saving order…';
      try{
        const response=await fetch(root.dataset.reorderUrl,{method:'POST',body,headers:{Accept:'text/plain'}});
        if(!response.ok)throw new Error(await response.text());
        if(status)status.textContent=root.dataset.reorderSaved||'Order saved';
        await navigate('/monitor/websites',true);
      }catch(_){
        if(status)status.textContent=root.dataset.reorderFailed||'Order was not saved. Restoring the list.';
        initialOrder.forEach(id=>{
          const row=rows().find(candidate=>candidate.dataset.monitorId===id);
          if(row)ledger?.append(row);
        });
        updateMoveButtons();
      }
    };
    if(root.dataset.reorderUrl){
      const onMove=event=>{
        const form=event.target.closest('[data-reorder-move-form]');
        if(!form)return;
        event.preventDefault();
        const row=form.closest('[data-monitor-id]');
        const direction=event.submitter?.value;
        if(!row||!direction)return;
        if(direction==='up'&&row.previousElementSibling?.matches('[data-monitor-id]'))ledger.insertBefore(row,row.previousElementSibling);
        if(direction==='down'&&row.nextElementSibling?.matches('[data-monitor-id]'))ledger.insertBefore(row.nextElementSibling,row);
        row.focus();
        markOrderChanged();
      };
      const onFinish=()=>saveOrder();
      const onCancel=()=>navigate('/monitor/websites',true);
      const onReorderKey=event=>{
        const row=event.target.closest('[data-monitor-id]');
        if(!row||!['ArrowUp','ArrowDown'].includes(event.key))return;
        event.preventDefault();
        if(event.key==='ArrowUp'&&row.previousElementSibling?.matches('[data-monitor-id]'))ledger.insertBefore(row,row.previousElementSibling);
        if(event.key==='ArrowDown'&&row.nextElementSibling?.matches('[data-monitor-id]'))ledger.insertBefore(row.nextElementSibling,row);
        row.focus();
        markOrderChanged();
      };
      root.addEventListener('submit',onMove);
      root.querySelector('[data-reorder-finish]')?.addEventListener('click',onFinish);
      root.querySelector('[data-reorder-cancel]')?.addEventListener('click',onCancel);
      root.addEventListener('keydown',onReorderKey);
      for(const row of rows()){
        row.addEventListener('dragstart',event=>{
          dragged=row;
          row.classList.add('is-dragging');
          event.dataTransfer.effectAllowed='move';
          event.dataTransfer.setData('text/plain',row.dataset.monitorId);
        });
        row.addEventListener('dragend',()=>{
          row.classList.remove('is-dragging');
          dragged=undefined;
        });
        row.addEventListener('dragover',event=>{
          if(!dragged||dragged===row)return;
          event.preventDefault();
          const before=event.clientY<row.getBoundingClientRect().top+row.offsetHeight/2;
          row.parentElement.insertBefore(dragged,before?row:row.nextSibling);
        });
        row.addEventListener('drop',event=>{event.preventDefault();markOrderChanged()});
      }
      updateMoveButtons();
      return ()=>{
        stopped=true;
        root.removeEventListener('submit',onMove);
        root.querySelector('[data-reorder-finish]')?.removeEventListener('click',onFinish);
        root.querySelector('[data-reorder-cancel]')?.removeEventListener('click',onCancel);
        root.removeEventListener('keydown',onReorderKey);
      };
    }
    const surfaceTaskURL=root.closest('.task-panel')?taskPanelState?.taskURL:'';
    const reloadCurrentSurface=async()=>{
      if(stopped||!root.isConnected)return;
      if(surfaceTaskURL){
        if(taskPanelState?.taskURL===surfaceTaskURL&&taskPanelState.host.contains(root))await openTask(surfaceTaskURL,false);
        return;
      }
      await navigate(location.href,false);
    };
    const openRowDetail=target=>{
      const row=target.closest('[data-detail-href]');
      if(!row||target.closest('a,button,input,select,textarea,form'))return;
      if(matchMedia('(min-width: 761px)').matches)openTask(row.dataset.detailHref,true,row);
      else navigate(row.dataset.detailHref,true);
    };
    const onLedgerClick=event=>openRowDetail(event.target);
    const onLedgerKey=event=>{
      if(!event.target.matches('[data-detail-href]')||!['Enter',' '].includes(event.key))return;
      event.preventDefault();
      openRowDetail(event.target);
    };
    root.addEventListener('click',onLedgerClick);
    root.addEventListener('keydown',onLedgerKey);
    const checkForm=root.querySelector('[data-website-check-form]');
    const onCheck=async event=>{
      event.preventDefault();
      if(busy)return;
      const button=event.submitter||checkForm.querySelector('button');
      const original=button.textContent;
      const startingToken=root.dataset.monitorChecked;
      const controller=new AbortController();
      checkController?.abort();
      checkController=controller;
      busy=true;
      button.disabled=true;
      button.setAttribute('aria-busy','true');
      button.textContent=checkForm.dataset.checkingLabel||original;
      try{
        const response=await fetch(checkForm.action,{method:'POST',body:new FormData(checkForm),credentials:'same-origin',signal:controller.signal});
        if(!response.ok)throw new Error(await response.text());
        const configuredTimeout=Number(checkForm.dataset.checkTimeoutMs)||30000;
        const deadline=Date.now()+Math.max(15000,configuredTimeout+10000);
        let completed=false;
        while(Date.now()<deadline){
          await new Promise(resolve=>setTimeout(resolve,500));
          if(stopped||controller.signal.aborted)return;
          const result=await fetch(root.dataset.statusUrl,{headers:{Accept:'application/json'},cache:'no-store',signal:controller.signal});
          if(!result.ok)continue;
          const snapshot=await result.json();
          const token=String(snapshot.CheckedToken??snapshot.checkedToken??'');
          if(token&&token!==startingToken){
            completed=true;
            break;
          }
        }
        if(!completed)throw new Error(root.dataset.refreshFailed||'The latest check result is not available yet.');
        if(stopped||controller.signal.aborted||!root.isConnected)return;
        button.textContent=checkForm.dataset.completeLabel||original;
        await new Promise(resolve=>setTimeout(resolve,650));
        if(stopped||controller.signal.aborted||!root.isConnected)return;
        busy=false;
        await reloadCurrentSurface();
      }catch(error){
        if(error?.name==='AbortError')return;
        busy=false;
        button.disabled=false;
        button.removeAttribute('aria-busy');
        button.textContent=original;
        if(refreshStatus)refreshStatus.textContent=error?.message||root.dataset.refreshFailed||'';
      }
    };
    checkForm?.addEventListener('submit',onCheck);
    const refresh=async()=>{
      if(stopped||busy||document.hidden)return;
      busy=true;
      try{
        const response=await fetch(root.dataset.statusUrl,{headers:{Accept:'application/json'},cache:'no-store'});
        if(!response.ok)throw new Error('status refresh failed');
        const payload=await response.json();
        if(refreshStatus)refreshStatus.textContent='';
        if(!stopped&&websiteSnapshotChanged(root,payload))await reloadCurrentSurface();
      }catch(_){
        if(refreshStatus)refreshStatus.textContent=root.dataset.refreshFailed||'';
      }
      busy=false;
    };
    const timer=setInterval(refresh,10000);
    const visibility=()=>{if(!document.hidden)refresh()};
    document.addEventListener('visibilitychange',visibility);
    return ()=>{
      stopped=true;
      checkController?.abort();
      clearInterval(timer);
      document.removeEventListener('visibilitychange',visibility);
      root.removeEventListener('click',onLedgerClick);
      root.removeEventListener('keydown',onLedgerKey);
      checkForm?.removeEventListener('submit',onCheck);
    };
  }

  function initPage() {
    const cleanups = [];
    cleanupPage = () => cleanups.splice(0).forEach(cleanup => cleanup());
    renderIcons();
    localizeTimes();
    initMarkdownPreview();
    initScriptPreview();
    initPasswordControls(document, cleanups);
    initCopyControls(document, cleanups);
    initFileDropUpload(document, cleanups);
    initOverview(cleanups);
    initApplications(cleanups);
    initRun(cleanups);
    initGroupedRecords(cleanups);
    initScheduleCron(cleanups);
    const websiteForm = document.querySelector("[data-website-monitor-form]");
    if (websiteForm) cleanups.push(initWebsiteMonitorForm(websiteForm));
    const websiteMonitoring = document.querySelector("[data-website-monitoring],[data-website-detail]");
    if (websiteMonitoring) cleanups.push(initWebsiteMonitoring(websiteMonitoring));
    const websiteNginx = document.querySelector("[data-website-nginx]");
    if (websiteNginx) cleanups.push(initWebsiteNginx(websiteNginx));
  }

  document.addEventListener("click", event => {
    const sidebarControl = event.target.closest("[data-sidebar-toggle]");
    if (sidebarControl) {
      setSidebar(!document.body.classList.contains("sidebar-open"));
      return;
    }
    if (event.target.closest("[data-sidebar-close]")) {
      setSidebar(false);
      return;
    }
    if (taskPanelState && event.target.closest(".task-panel-scrim,[data-task-panel-close]")) {
      closeTaskPanel(true);
      return;
    }
    const link = event.target.closest("a[href]");
    if (!link || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const destination = new URL(link.href, location.href);
    if (isNativeLink(link, destination)) return;
    event.preventDefault();
    if (link.getAttribute("aria-disabled") === "true") return;
    if (taskPanelState && link.closest(".task-panel")) {
      const returnURL = taskPanelState.returnURL;
      if (destination.href === returnURL) {
        closeTaskPanel(true);
      } else if (link.matches("[data-task-link]") && matchMedia("(min-width: 761px)").matches) {
        openTask(destination.href, false, link);
      } else {
        closeTaskPanel(false);
        history.replaceState({ pjax: true }, "", destination.href);
        navigate(destination.href, false);
      }
      return;
    }
    if (link.matches("[data-task-link]") && matchMedia("(min-width: 761px)").matches) {
      openTask(destination.href, true, link);
    } else {
      const mainNavigation = link.matches(".sidebar-nav a");
      navigate(destination.href, true, {
        deferredData: isDeferredDataURL(destination.href),
        immediate: mainNavigation,
        title: mainNavigation ? navigationTitle(link) : undefined,
        focusSelector: link.dataset.focusAfterNavigation,
      });
    }
  });

  document.addEventListener("click", event => {
    const menu = event.target.closest(".action-menu");
    document.querySelectorAll(".action-menu[open]").forEach(open => {
      if (open !== menu) open.removeAttribute("open");
    });
    const fileSort = event.target.closest(".file-sort");
    document.querySelectorAll(".file-sort[open]").forEach(open => {
      if (open !== fileSort) open.removeAttribute("open");
    });
  });

  document.addEventListener("submit", event => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || event.defaultPrevented) return;
    if (form.dataset.confirm && !window.confirm(form.dataset.confirm)) {
      event.preventDefault();
      return;
    }
    const submitter = event.submitter || (form.matches("[data-file-search]") ? form.querySelector("[data-search-submit]") : null);
    if (submitter) {
      if (submitter.name) {
        const mirror = document.createElement("input");
        mirror.type = "hidden";
        mirror.name = submitter.name;
        mirror.value = submitter.value;
        mirror.dataset.submitterMirror = "";
        form.append(mirror);
      }
      submitter.dataset.submitOriginal = submitter.innerHTML;
      submitter.dataset.submitOriginalMinWidth = submitter.style.minWidth;
      submitter.dataset.submitOriginalWidth = submitter.style.width;
      const submitterWidth = `${submitter.getBoundingClientRect().width}px`;
      submitter.style.minWidth = submitterWidth;
      submitter.style.width = submitterWidth;
      submitter.textContent = submitter.dataset.pendingLabel || words().processing;
      submitter.disabled = true;
      submitter.setAttribute("aria-busy", "true");
    }
    form.setAttribute("aria-busy", "true");
    if (form.matches("[data-login-form]")) {
      event.preventDefault();
      submitLogin(form, submitter);
    } else if (form.hasAttribute("data-update-apply")) {
      event.preventDefault();
      submitUpdateApply(form, submitter);
    } else if (form.hasAttribute("data-async")) {
      event.preventDefault();
      submitAsync(form, submitter);
    }
  });

  document.addEventListener("keydown", event => {
    const editing = event.target.matches("input,textarea,select,[contenteditable='true']");
    if (taskPanelState && event.key === "Tab") {
      const panel = taskPanelState.host.querySelector("[data-task-panel]");
      const focusable = [...panel.querySelectorAll("a[href],button:not([disabled]),input:not([disabled]):not([type='hidden']),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex='-1'])")]
        .filter(element => !element.hidden && element.getClientRects().length > 0);
      if (!focusable.length) {
        event.preventDefault();
        panel.focus();
      } else {
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (event.shiftKey && (event.target === first || !panel.contains(event.target))) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && event.target === last) {
          event.preventDefault();
          first.focus();
        }
      }
    }
    if (event.key === "Escape") {
      if (taskPanelRequest) {
        event.preventDefault();
        cancelTaskPanelRequest();
        return;
      }
      if (taskPanelState) {
        event.preventDefault();
        closeTaskPanel(true);
        return;
      }
      if (document.body.classList.contains("sidebar-open")) {
        event.preventDefault();
        setSidebar(false);
        return;
      }
      const openMenu = document.querySelector(".action-menu[open],.ledger-disclosure[open],.file-sort[open]");
      if (openMenu) {
        event.preventDefault();
        openMenu.removeAttribute("open");
      }
    }
    if (event.key === "/" && !editing) {
      const search = document.querySelector('main input[type="search"]');
      if (search) {
        event.preventDefault();
        search.focus();
      }
    }
    if (event.key.toLowerCase() === "p" && !editing) {
      const run = document.querySelector("[data-run-events-url]");
      if (run?._toggleLogPause) {
        event.preventDefault();
        run._toggleLogPause();
      }
    }
  });

  window.addEventListener("popstate", event => {
    if (taskPanelState) {
      closeTaskPanel(false);
      return;
    }
    if (event.state?.task) {
      openTask(event.state.taskURL || location.href, false);
      return;
    }
    const navigationLink = mainNavigationLink(location.href, true);
    navigate(location.href, false, {
      deferredData: isDeferredDataURL(location.href),
      title: navigationLink ? navigationTitle(navigationLink) : undefined,
    });
  });

  initPage();
  initStatus();
})();
