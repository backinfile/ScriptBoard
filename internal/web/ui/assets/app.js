(() => {
  "use strict";

  const iconPaths = {
    "activity": '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>',
    "archive": '<rect width="20" height="5" x="2" y="3" rx="1"/><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8"/><path d="M10 12h4"/>',
    "app-window": '<rect width="20" height="16" x="2" y="4" rx="2"/><path d="M2 8h20"/><path d="M6 6h.01"/><path d="M10 6h.01"/>',
    "arrow-down": '<path d="m6 9 6 6 6-6"/><path d="M12 3v12"/>',
    "arrow-down-to-line": '<path d="M12 17V3"/><path d="m6 11 6 6 6-6"/><path d="M19 21H5"/>',
    "arrow-left": '<path d="m12 19-7-7 7-7"/><path d="M19 12H5"/>',
    "arrow-right": '<path d="m12 5 7 7-7 7"/><path d="M5 12h14"/>',
    "arrow-up": '<path d="m18 15-6-6-6 6"/><path d="M12 21V9"/>',
    "arrow-up-down": '<path d="m21 16-4 4-4-4"/><path d="M17 20V4"/><path d="m3 8 4-4 4 4"/><path d="M7 4v16"/>',
    "arrow-up-to-line": '<path d="M5 3h14"/><path d="m18 13-6-6-6 6"/><path d="M12 7v14"/>',
    "at-sign": '<circle cx="12" cy="12" r="4"/><path d="M16 8v5a3 3 0 0 0 6 0v-1a10 10 0 1 0-4 8"/>',
    "bookmark-plus": '<path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h5"/><path d="M19 3v6"/><path d="M16 6h6"/>',
    "box": '<path d="m21 8-9-5-9 5 9 5 9-5Z"/><path d="m3 8 9 5 9-5"/><path d="M3 8v8l9 5 9-5V8"/><path d="M12 13v8"/>',
    "braces": '<path d="M8 3H7a2 2 0 0 0-2 2v5a2 2 0 0 1-2 2 2 2 0 0 1 2 2v5c0 1.1.9 2 2 2h1"/><path d="M16 21h1a2 2 0 0 0 2-2v-5c0-1.1.9-2 2-2a2 2 0 0 1-2-2V5a2 2 0 0 0-2-2h-1"/>',
    "calendar-clock": '<path d="M16 2v4"/><path d="M3 10h18"/><path d="M8 2v4"/><rect x="3" y="4" width="18" height="18" rx="2"/><path d="M17 14v2l1 1"/>',
    "calendar-plus": '<path d="M16 2v4"/><path d="M3 10h18"/><path d="M8 2v4"/><rect x="3" y="4" width="18" height="18" rx="2"/><path d="M12 14v4"/><path d="M10 16h4"/>',
    "check": '<path d="m20 6-11 11-5-5"/>',
    "circle-check": '<circle cx="12" cy="12" r="10"/><path d="m9 12 2 2 4-4"/>',
    "chevron-down": '<path d="m6 9 6 6 6-6"/>',
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
    "eraser": '<path d="m7 21-4-4a2.8 2.8 0 0 1 0-4L13 3a2.8 2.8 0 0 1 4 0l4 4a2.8 2.8 0 0 1 0 4L11 21"/><path d="m5 11 9 9"/><path d="M5 21h14"/>',
    "eye": '<path d="M2.1 12a10.7 10.7 0 0 1 19.8 0 10.7 10.7 0 0 1-19.8 0"/><circle cx="12" cy="12" r="3"/>',
    "eye-off": '<path d="m2 2 20 20"/><path d="M6.7 6.7A11.7 11.7 0 0 0 2.1 12a10.7 10.7 0 0 0 14.1 5.2"/><path d="M10.7 10.7a3 3 0 0 0 4.2 4.2"/><path d="M14.3 5.2A10.7 10.7 0 0 1 21.9 12a11.8 11.8 0 0 1-2.2 3.2"/>',
    "external-link": '<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>',
    "file": '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/>',
    "file-cog": '<path d="M14 2H6a2 2 0 0 0-2 2v8"/><path d="M14 2v6h6"/><circle cx="13" cy="18" r="3"/><path d="m15.6 16.5.9-.5M10.4 19.5l-.9.5M15.6 19.5l.9.5M10.4 16.5l-.9-.5"/>',
    "file-lock-2": '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h7"/><path d="M14 2v6h6"/><rect x="14" y="15" width="8" height="6" rx="1"/><path d="M16 15v-2a2 2 0 0 1 4 0v2"/>',
    "file-terminal": '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/><path d="m8 13 2 2-2 2"/><path d="M12 17h4"/>',
    "files": '<path d="M20 7h-3a2 2 0 0 1-2-2V2"/><path d="M9 18a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h7l4 4v10a2 2 0 0 1-2 2Z"/><path d="M3 7v13a2 2 0 0 0 2 2h9"/>',
    "folder": '<path d="M3 6h5l2 2h11v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
    "folder-code": '<path d="M10 10.5 8 12l2 1.5M14 10.5l2 1.5-2 1.5"/><path d="M2 6h5l2 2h13"/><path d="M2 6v12a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V8"/>',
    "folder-open": '<path d="M6 14l1.5-3h13l-2 7a2 2 0 0 1-2 1H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2v1"/>',
    "folder-plus": '<path d="M12 10v6M9 13h6"/><path d="M3 6h5l2 2h11v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
    "globe-2": '<circle cx="12" cy="12" r="10"/><path d="M2 12h20"/><path d="M12 2a15.3 15.3 0 0 1 0 20"/><path d="M12 2a15.3 15.3 0 0 0 0 20"/>',
    "hard-drive": '<path d="M5.5 5.1 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.8 4H7.2a2 2 0 0 0-1.7 1.1z"/><path d="M2 12h20"/><path d="M6 16h.01M10 16h.01"/>',
    "image": '<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3-3a2 2 0 0 0-3 0l-9 9"/>',
    "info": '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>',
    "key-round": '<path d="M2.6 16.8a6 6 0 1 0 8.6-8.3A6 6 0 0 0 2.6 16.8"/><path d="m15 9 6-6M17 5l2 2M14 8l2 2"/>',
    "languages": '<path d="m5 8 6 6"/><path d="m4 14 6-6 2-3"/><path d="M2 5h12"/><path d="M7 2h1"/><path d="m22 22-5-10-5 10"/><path d="M14 18h6"/>',
    "loader-circle": '<path d="M21 12a9 9 0 1 1-6.2-8.6"/>',
    "lock": '<rect width="18" height="11" x="3" y="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
    "log-out": '<path d="M10 17l5-5-5-5"/><path d="M15 12H3"/><path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"/>',
    "memory-stick": '<path d="M6 19v-3M10 19v-3M14 19v-3M18 19v-3M8 11V9M16 11V9"/><rect x="2" y="5" width="20" height="11" rx="2"/>',
    "minus": '<path d="M5 12h14"/>',
    "message-square": '<path d="M21 15a4 4 0 0 1-4 4H8l-5 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4z"/>',
    "network": '<rect x="16" y="16" width="6" height="6" rx="1"/><rect x="2" y="16" width="6" height="6" rx="1"/><rect x="9" y="2" width="6" height="6" rx="1"/><path d="M5 16v-3h14v3M12 12V8"/>',
    "panel-left-open": '<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/><path d="m14 9 3 3-3 3"/>',
    "panel-left-close": '<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/><path d="m17 9-3 3 3 3"/>',
	"package": '<path d="m7.5 4.27 9 5.15"/><path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z"/><path d="m3.3 7 8.7 5 8.7-5"/><path d="M12 22V12"/>',
    "pause": '<rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/>',
    "pencil": '<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L8 18l-4 1 1-4z"/>',
    "pin": '<path d="M12 17v5"/><path d="M5 17h14"/><path d="M15 3.5c0 2 1 3.5 2 4.5H7c1-1 2-2.5 2-4.5"/><path d="M9 3h6"/>',
    "pin-off": '<path d="M12 17v5"/><path d="M5 17h12"/><path d="M4 4 20 20"/><path d="M9 3h6"/><path d="M15 3.5c0 2 1 3.5 2 4.5h-4"/><path d="M7 8c.7-.7 1.4-1.8 1.8-3"/>',
    "play": '<path d="m6 3 14 9-14 9z"/>',
    "plus": '<path d="M5 12h14M12 5v14"/>',
    "power": '<path d="M12 2v10"/><path d="M18.4 6.6a9 9 0 1 1-12.8 0"/>',
    "refresh-cw": '<path d="M21 12a9 9 0 0 0-15.2-6.5L3 8"/><path d="M3 3v5h5"/><path d="M3 12a9 9 0 0 0 15.2 6.5L21 16"/><path d="M16 16h5v5"/>',
    "rotate-cw": '<path d="M21 12a9 9 0 1 1-2.64-6.36L21 8"/><path d="M21 3v5h-5"/>',
    "list-filter": '<path d="M3 6h18"/><path d="M7 12h10"/><path d="M10 18h4"/>',
    "rotate-ccw": '<path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/>',
    "scroll-text": '<path d="M15 12h-5M15 8h-5"/><path d="M19 17V5a2 2 0 0 0-2-2H4"/><path d="M8 21h12a2 2 0 0 0 2-2v-1H11v1a2 2 0 1 1-4 0V5a2 2 0 1 0-4 0v2h4"/>',
    "search": '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
    "scan-search": '<path d="M3 7V5a2 2 0 0 1 2-2h2"/><path d="M17 3h2a2 2 0 0 1 2 2v2"/><path d="M21 17v2a2 2 0 0 1-2 2h-2"/><path d="M7 21H5a2 2 0 0 1-2-2v-2"/><circle cx="12" cy="12" r="3"/><path d="m16 16-1.9-1.9"/>',
    "search-x": '<path d="m13.5 8.5-5 5"/><path d="m8.5 8.5 5 5"/><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
    "send": '<path d="m22 2-7 20-4-9-9-4z"/><path d="M22 2 11 13"/>',
    "settings": '<path d="M12.2 2h-.4a2 2 0 0 0-2 2v.2a2 2 0 0 1-1 1.7l-.4.2a2 2 0 0 1-2 0l-.1-.1a2 2 0 0 0-2.7.7l-.2.4a2 2 0 0 0 .7 2.7l.1.1a2 2 0 0 1 1 1.7v.5a2 2 0 0 1-1 1.7l-.1.1a2 2 0 0 0-.7 2.7l.2.4a2 2 0 0 0 2.7.7l.1-.1a2 2 0 0 1 2 0l.4.2a2 2 0 0 1 1 1.7v.2a2 2 0 0 0 2 2h.4a2 2 0 0 0 2-2v-.2a2 2 0 0 1 1-1.7l.4-.2a2 2 0 0 1 2 0l.1.1a2 2 0 0 0 2.7-.7l.2-.4a2 2 0 0 0-.7-2.7l-.1-.1a2 2 0 0 1-1-1.7v-.5a2 2 0 0 1 1-1.7l.1-.1a2 2 0 0 0 .7-2.7l-.2-.4a2 2 0 0 0-2.7-.7l-.1.1a2 2 0 0 1-2 0l-.4-.2a2 2 0 0 1-1-1.7V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>',
    "shield-check": '<path d="M20 13c0 5-3.5 7.5-8 9-4.5-1.5-8-4-8-9V5l8-3 8 3z"/><path d="m9 12 2 2 4-4"/>',
    "shield-alert": '<path d="M20 13c0 5-3.5 7.5-8 9-4.5-1.5-8-4-8-9V5l8-3 8 3z"/><path d="M12 8v4"/><path d="M12 16h.01"/>',
    "sparkles": '<path d="m12 3-1.5 4.5L6 9l4.5 1.5L12 15l1.5-4.5L18 9l-4.5-1.5z"/><path d="M5 3v4M3 5h4M19 16v5M16.5 18.5h5"/>',
    "square": '<rect x="3" y="3" width="18" height="18" rx="2"/>',
    "square-pen": '<path d="M12 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.4 2.6a2.1 2.1 0 0 1 3 3L12 15l-4 1 1-4z"/>',
    "square-terminal": '<path d="m7 7 3 3-3 3M13 13h4"/><rect x="3" y="3" width="18" height="18" rx="2"/>',
    "trash-2": '<path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6M10 11v5M14 11v5"/>',
    "triangle-alert": '<path d="m21.7 18-8-14a2 2 0 0 0-3.4 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.7-3z"/><path d="M12 9v4M12 17h.01"/>',
    "unlock": '<rect width="18" height="11" x="3" y="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 9.9-1"/>',
    "upload": '<path d="M12 3v12M17 8l-5-5-5 5"/><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>',
    "user-round": '<circle cx="12" cy="8" r="5"/><path d="M20 21a8 8 0 0 0-16 0"/>',
    "users": '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>',
    "x": '<path d="M18 6 6 18M6 6l12 12"/>',
    "zap": '<path d="M4 14a1 1 0 0 1-.8-1.6l9-11a.5.5 0 0 1 .9.4L12 8.5A1 1 0 0 0 13 10h7a1 1 0 0 1 .8 1.6l-9 11a.5.5 0 0 1-.9-.4L12 15.5A1 1 0 0 0 11 14z"/>'
  };

  const appScript = document.currentScript || document.querySelector('script[src*="/assets/app-v2.js"]');
  const appAssetVersion = appScript ? new URL(appScript.src, location.href).searchParams.get("v") : "";
  const versionedAssetURL = path => appAssetVersion
    ? `${path}?v=${encodeURIComponent(appAssetVersion)}`
    : path;
  const websiteFaultColorKey = "scriptboard.websiteFaultColor";
  const sidebarCollapsedKey = "scriptboard.sidebarCollapsed";
  const readWebsiteFaultColor = () => {
    try {
      const value = localStorage.getItem(websiteFaultColorKey);
      return value === "magenta" ? "magenta" : "red";
    } catch {
      return "red";
    }
  };
  const applyWebsiteFaultColor = color => {
    document.documentElement.dataset.websiteFaultColor = color === "magenta" ? "magenta" : "red";
  };
  applyWebsiteFaultColor(readWebsiteFaultColor());

  let cleanupPage = () => {};
  let markdownLibrariesPromise = null;
  const scriptAssetPromises = new Map();
  let navigationBusy = false;
  let navigationSequence = 0;
  let navigationRequest = null;
  let taskPanelState = null;
  let taskPanelRequest = null;
  let taskPanelHistoryClosePending = false;
  let taskPanelRefreshAfterCloseURL = "";
  let taskPanelGroupRefreshAfterClose = null;
  let activeFileConflictDialog = null;
  let activeServerErrorDialog = null;
  let activeActionDialog = null;
  let activeUploadResultsDialog = null;

  const locale = () => document.documentElement.lang === "zh-CN" ? "zh-CN" : "en-US";
  const copy = {
    "zh-CN": {
      current: "数据正常", attention: "需要关注", stale: "数据已过期",
      activeRuns: "个活动 Run", processing: "处理中…", complete: "实时输出已结束",
      connected: "实时输出已连接", disconnected: "实时输出连接中断",
      loading: "加载中…", loadFailed: "页面加载失败", retry: "重试",
      submitFailed: "操作未完成，请检查网络后重试。为避免重复操作，ScriptBoard 不会自动再次提交。",
      serverErrorClose: "关闭", serverErrorRetry: "重新载入", serverErrorRetryTask: "重新打开", serverErrorRetryAction: "重新提交",
      serverErrorRefresh: "刷新当前页面", serverErrorDetails: "技术详情", serverErrorRequest: "请求",
      serverErrorPreserved: "当前工作区已保留", serverErrorNoResubmit: "未自动重试，避免重复执行写操作",
      confirmTitle: "确认操作", confirmDescription: "请确认是否继续执行此操作。", confirmCancel: "取消", confirmAction: "确认",
      websiteNormal: "网站监控正常", websiteNoOpenIssues: "没有故障或待复核项",
      websiteDownOne: "个网站故障", websiteDownMany: "个网站故障",
      websiteVerifyingOne: "个正在复核", websiteVerifyingMany: "个正在复核",
      websiteNoConfirmedFailure: "没有已确认的网站故障",
      conflictTitle: "已有同名文件", conflictDescription: "选择如何处理同名文件。默认不会覆盖任何内容。",
      conflictBatchDescription: "这个选择将应用到本次上传中的所有同名文件。",
      conflictSkip: "跳过", conflictOverwrite: "覆盖", conflictRename: "重命名", conflictClose: "关闭",
      conflictOverwriteNote: "覆盖前，当前文件会先移入回收站。",
      conflictOverwriteUnavailable: "部分条目正在使用或不是普通文件，不能覆盖。",
      conflictMore: "个其他同名文件",
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
      serverErrorClose: "Close", serverErrorRetry: "Reload", serverErrorRetryTask: "Reopen", serverErrorRetryAction: "Submit again",
      serverErrorRefresh: "Refresh current page", serverErrorDetails: "Technical details", serverErrorRequest: "Request",
      serverErrorPreserved: "The current workspace is preserved", serverErrorNoResubmit: "Not retried automatically to avoid repeating a write",
      confirmTitle: "Confirm action", confirmDescription: "Confirm that you want to continue with this action.", confirmCancel: "Cancel", confirmAction: "Confirm",
      websiteNormal: "Website monitoring normal", websiteNoOpenIssues: "No failures or pending verifications",
      websiteDownOne: "website down", websiteDownMany: "websites down",
      websiteVerifyingOne: "website under verification", websiteVerifyingMany: "websites under verification",
      websiteNoConfirmedFailure: "No confirmed website failures",
      conflictTitle: "A file with this name already exists",
      conflictDescription: "Choose how to handle name conflicts. Nothing is overwritten by default.",
      conflictBatchDescription: "Your choice applies to every name conflict in this upload.",
      conflictSkip: "Skip", conflictOverwrite: "Overwrite", conflictRename: "Rename", conflictClose: "Close",
      conflictOverwriteNote: "Before overwriting, the current file is moved to Trash.",
      conflictOverwriteUnavailable: "Some items are in use or are not regular files and cannot be overwritten.",
      conflictMore: "more name conflicts",
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

  async function copyTextToClipboard(text) {
    const value = String(text ?? "");
    let clipboardError = null;
    if (navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(value);
        return;
      } catch (error) {
        clipboardError = error;
      }
    }

    // Remote plain-HTTP origins do not expose Async Clipboard, so retain a
    // user-gesture fallback for LAN deployments and restore the page state.
    const activeElement = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const selection = document.getSelection();
    const selectedRanges = selection
      ? Array.from({ length: selection.rangeCount }, (_, index) => selection.getRangeAt(index).cloneRange())
      : [];
    const fallback = document.createElement("textarea");
    fallback.value = value;
    fallback.setAttribute("readonly", "");
    fallback.style.position = "fixed";
    fallback.style.left = "-9999px";
    fallback.style.top = "0";
    fallback.style.opacity = "0";
    document.body.append(fallback);
    fallback.focus({ preventScroll: true });
    fallback.select();
    fallback.setSelectionRange(0, fallback.value.length);

    let copied = false;
    try {
      copied = document.execCommand("copy");
    } finally {
      fallback.remove();
      if (selection) {
        selection.removeAllRanges();
        selectedRanges.forEach(range => selection.addRange(range));
      }
      activeElement?.focus({ preventScroll: true });
    }
    if (!copied) throw clipboardError || new Error("Clipboard copy failed");
  }
  window.ScriptBoardCopyText = copyTextToClipboard;

  function renderIcons(root = document) {
    root.querySelectorAll("[data-lucide]:not([data-icon-ready])").forEach(target => {
      target.prepend(makeIcon(target.dataset.lucide));
      target.dataset.iconReady = "";
    });
  }
  window.ScriptBoardRenderIcons = renderIcons;

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

  function renderScriptPreview(source) {
    const language = source.dataset.highlightLanguage || "";
    const container = source.closest("pre");
    container?.setAttribute("aria-busy", "true");
    return loadHighlightLanguages([language]).then(() => {
      if (source.isConnected) highlightElement(source, language);
      container?.removeAttribute("aria-busy");
    }).catch(() => {
      container?.removeAttribute("aria-busy");
    });
  }

  function initScriptPreview() {
    const source = document.querySelector("[data-script-preview]");
    if (source) renderScriptPreview(source);
  }

  function hostMarkdownPath(reference, basePath) {
    if (!reference || reference.startsWith("#") || /^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(reference)) return null;
    const [withoutHash, hash = ""] = reference.split("#", 2);
    const [rawPath] = withoutHash.split("?", 1);
    let decoded;
    try { decoded = decodeURIComponent(rawPath); } catch { return null; }
    const windows = /^[a-z]:[\\/]/i.test(basePath);
    const separator = windows ? "\\" : "/";
    const absolute = windows ? /^[a-z]:[\\/]/i.test(decoded) : decoded.startsWith("/");
    const base = absolute ? decoded : `${basePath}${basePath.endsWith(separator) ? "" : separator}${decoded}`;
    const normalized = base.replaceAll(windows ? "/" : "\\", separator);
    const prefix = windows ? normalized.slice(0, 3) : separator;
    const components = normalized.slice(prefix.length).split(separator);
    const result = [];
    for (const component of components) {
      if (!component || component === ".") continue;
      if (component === "..") {
        if (result.length === 0) return null;
        result.pop();
      } else {
        result.push(component);
      }
    }
    return { path: prefix + result.join(separator), hash: hash ? `#${hash}` : "", directory: /[\\/]$/.test(rawPath) };
  }

  function hostFileRoute(endpoint, hostPath, hash = "") {
    const target = new URL(endpoint, location.origin);
    target.searchParams.set("path", hostPath);
    target.hash = hash;
    return `${target.pathname}${target.search}${target.hash}`;
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
      const hostFile = hostMarkdownPath(reference, baseURL);
      if (hostFile) {
        const extension = hostFile.path.slice(hostFile.path.lastIndexOf(".")).toLowerCase();
        if (hostFile.directory) {
          link.href = hostFileRoute("/resources/files", hostFile.path, hostFile.hash);
        } else if (extension === ".md") {
          link.href = hostFileRoute("/resources/files/view", hostFile.path, hostFile.hash);
        } else {
          link.href = hostFileRoute("/resources/files/download", hostFile.path, hostFile.hash);
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
      const hostFile = hostMarkdownPath(reference, baseURL);
      if (hostFile) {
        image.src = hostFileRoute("/resources/files/preview", hostFile.path);
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

  function renderMarkdownPreview(preview, source) {
    preview.setAttribute("aria-busy", "true");
    return loadMarkdownLibraries().then(async () => {
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
      rewriteMarkdownResources(fragment, preview.dataset.markdownBase || "");
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

  function initMarkdownPreview() {
    const preview = document.querySelector("[data-markdown-preview]");
    const source = document.querySelector("[data-markdown-source]");
    if (preview && source) renderMarkdownPreview(preview, source);
  }

  function initTextPreviewPager(cleanups) {
    const root = document.querySelector("[data-text-preview-pager]");
    if (!root) return;
    const markdownPreview = root.querySelector("[data-markdown-preview]");
    const source = root.querySelector("[data-markdown-source],[data-script-preview],[data-plain-text-preview]");
    const status = root.querySelector("[data-text-preview-status]");
    if (!source) return;
    let content = source.textContent || "";
    let nextOffset = root.dataset.textPreviewNext || "";
    let version = root.dataset.textPreviewVersion || "";
    let hasMore = root.dataset.textPreviewHasMore === "true";
    let loading = false;
    let disposed = false;

    const render = async () => {
      source.textContent = content;
      if (source.matches("[data-script-preview]")) await renderScriptPreview(source);
      if (markdownPreview) await renderMarkdownPreview(markdownPreview, source);
    };
    const nearEnd = () => {
      const documentBottom = document.documentElement.scrollHeight - window.scrollY - window.innerHeight;
      const surface = markdownPreview && !markdownPreview.hidden ? markdownPreview : source.closest("pre") || source;
      const surfaceBottom = surface.scrollHeight - surface.scrollTop - surface.clientHeight;
      const surfaceScrollable = surface.scrollHeight > surface.clientHeight + 1;
      return documentBottom < 480 || surfaceScrollable && surfaceBottom < 240;
    };
    const loadMore = async () => {
      if (disposed || loading || !hasMore || !nextOffset) return;
      loading = true;
      if (status) {
        status.hidden = false;
        status.dataset.state = "loading";
      }
      try {
        const url = new URL(root.dataset.textPreviewUrl, location.href);
        url.searchParams.set("offset", nextOffset);
        url.searchParams.set("version", version);
        const response = await fetch(url, { credentials: "same-origin", cache: "no-store", headers: { Accept: "application/json" } });
        if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
        const page = await response.json();
        content += page.content || "";
        nextOffset = page.nextOffset || "";
        version = page.version || version;
        hasMore = Boolean(page.hasMore);
        await render();
        if (status) status.hidden = !hasMore;
      } catch (error) {
        hasMore = false;
        if (status) {
          status.hidden = false;
          status.dataset.state = "error";
          status.textContent = error.message;
        }
      } finally {
        loading = false;
        if (!disposed && hasMore && nearEnd()) requestAnimationFrame(loadMore);
      }
    };
    const onScroll = () => { if (nearEnd()) loadMore(); };
    window.addEventListener("scroll", onScroll, { passive: true });
    const surface = source.closest("pre");
    surface?.addEventListener("scroll", onScroll, { passive: true });
    if (hasMore && nearEnd()) requestAnimationFrame(loadMore);
    cleanups.push(() => {
      disposed = true;
      window.removeEventListener("scroll", onScroll);
      surface?.removeEventListener("scroll", onScroll);
    });
  }

  function setSidebar(open) {
    document.body.classList.toggle("sidebar-open", open);
    document.querySelector("[data-sidebar-toggle]")?.setAttribute("aria-expanded", String(open));
    const scrim = document.querySelector(".sidebar-scrim");
    if (scrim) scrim.hidden = !open;
  }

  function replaceIconHost(host, name) {
    if (!host) return;
    host.querySelector("svg")?.remove();
    host.dataset.lucide = name;
    delete host.dataset.iconReady;
    renderIcons(host.parentElement || document);
  }

  function readSidebarCollapsed() {
    try { return localStorage.getItem(sidebarCollapsedKey) === "true"; }
    catch { return false; }
  }

  function applySidebarCollapsed(collapsed, persist = false) {
    document.body.classList.toggle("sidebar-collapsed", collapsed);
    const control = document.querySelector("[data-sidebar-collapse]");
    if (control) {
      const label = collapsed ? control.dataset.collapsedLabel : control.dataset.expandedLabel;
      control.setAttribute("aria-expanded", String(!collapsed));
      control.setAttribute("aria-label", label || "");
      control.title = label || "";
      replaceIconHost(control.querySelector("[data-sidebar-collapse-icon]"), collapsed ? "panel-left-open" : "panel-left-close");
    }
    if (persist) {
      try { localStorage.setItem(sidebarCollapsedKey, String(collapsed)); } catch { /* preference remains in memory */ }
    }
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
    if (!type.includes("text/html")) return { response, document: null, text: await response.text() };
    const text = await response.text();
    return { response, document: new DOMParser().parseFromString(text, "text/html") };
  }

  function closeServerErrorDialog(restoreFocus = true) {
    const state = activeServerErrorDialog;
    if (!state) return;
    activeServerErrorDialog = null;
    if (state.dialog.open) state.dialog.close();
    state.dialog.remove();
    if (restoreFocus && state.returnFocus instanceof HTMLElement && state.returnFocus.isConnected) {
      const returnFocus = state.returnFocus;
      // Chromium can finish its native <dialog> focus restoration after
      // close() returns and overwrite a synchronous focus() call. Restore on
      // the next frame so the initiating control reliably wins that race.
      requestAnimationFrame(() => {
        if (returnFocus.isConnected) returnFocus.focus({ preventScroll: true });
      });
    }
  }

  function showServerError(result, options = {}) {
    const status = Number(result?.response?.status || 0);
    const minimumStatus = options.includeClientErrors ? 400 : 500;
    const isHTTPFailure = status >= minimumStatus && status <= 599;
    if (!isHTTPFailure && options.force !== true) return false;
    closeServerErrorDialog(false);

    const source = result.document;
    const title = source?.querySelector(".error-page h1")?.textContent.trim() ||
      (locale() === "zh-CN" ? "操作未完成" : "Operation not completed");
    const summary = source?.querySelector(".error-page .page-error")?.textContent.trim() ||
      result?.text?.trim() || options.fallbackMessage ||
      (options.method === "POST" ? words().submitFailed : words().loadFailed);
    const technical = source?.querySelector(".error-page .ledger-disclosure .disclosure-body")?.textContent.trim() || "";
    const destination = new URL(options.url || result.response.url || location.href, location.href);
    const method = String(options.method || "GET").toUpperCase();
    const returnFocus = options.returnFocus || document.activeElement;

    const dialog = document.createElement("dialog");
    dialog.className = "server-error-dialog";
    dialog.setAttribute("aria-labelledby", "server-error-dialog-title");
    dialog.setAttribute("aria-describedby", "server-error-dialog-summary");

    const sheet = document.createElement("section");
    sheet.className = "server-error-sheet";
    const header = document.createElement("header");
    const mark = document.createElement("span");
    mark.className = "server-error-mark";
    mark.append(makeIcon("triangle-alert"));
    const heading = document.createElement("div");
    const titleElement = document.createElement("h2");
    titleElement.id = "server-error-dialog-title";
    titleElement.textContent = title;
    const summaryElement = document.createElement("p");
    summaryElement.id = "server-error-dialog-summary";
    summaryElement.textContent = summary;
    heading.append(titleElement, summaryElement);
    const close = document.createElement("button");
    close.className = "icon-button icon-button--quiet";
    close.type = "button";
    close.setAttribute("aria-label", words().serverErrorClose);
    close.append(makeIcon("x"));
    header.append(mark, heading, close);

    const facts = document.createElement("dl");
    const statusFact = document.createElement("div");
    const statusLabel = document.createElement("dt");
    statusLabel.textContent = "HTTP";
    const statusValue = document.createElement("dd");
    statusValue.textContent = status >= 400 ? String(status) : "—";
    statusFact.append(statusLabel, statusValue);
    const requestFact = document.createElement("div");
    const requestLabel = document.createElement("dt");
    requestLabel.textContent = words().serverErrorRequest;
    const requestValue = document.createElement("dd");
    const requestCode = document.createElement("code");
    requestCode.textContent = `${method} ${destination.pathname}`;
    requestValue.append(requestCode);
    requestFact.append(requestLabel, requestValue);
    facts.append(statusFact, requestFact);

    if (technical) {
      const details = document.createElement("details");
      const detailsSummary = document.createElement("summary");
      detailsSummary.append(makeIcon("chevron-right"), document.createTextNode(words().serverErrorDetails));
      const detailBody = document.createElement("pre");
      detailBody.textContent = technical;
      details.append(detailsSummary, detailBody);
      sheet.append(header, facts, details);
    } else {
      sheet.append(header, facts);
    }

    const footer = document.createElement("footer");
    const note = document.createElement("span");
    note.className = "server-error-note";
    note.append(makeIcon(method === "GET" ? "map-pin" : "shield-check"), document.createTextNode(
      method === "GET" ? words().serverErrorPreserved : words().serverErrorNoResubmit,
    ));
    const actions = document.createElement("div");
    const closeAction = document.createElement("button");
    closeAction.className = "button";
    closeAction.type = "button";
    closeAction.textContent = words().serverErrorClose;
    actions.append(closeAction);
    if (typeof options.retry === "function") {
      const retry = document.createElement("button");
      retry.className = "button button--primary";
      retry.type = "button";
      retry.append(makeIcon(options.retryIcon || "rotate-ccw"), document.createTextNode(options.retryLabel || words().serverErrorRetry));
      retry.addEventListener("click", () => {
        closeServerErrorDialog(false);
        options.retry();
      });
      actions.append(retry);
    }
    footer.append(note, actions);
    sheet.append(footer);
    dialog.append(sheet);
    document.body.append(dialog);

    const dismiss = () => closeServerErrorDialog(true);
    close.addEventListener("click", dismiss);
    closeAction.addEventListener("click", dismiss);
    dialog.addEventListener("cancel", event => {
      event.preventDefault();
      dismiss();
    });
    activeServerErrorDialog = { dialog, returnFocus };
    dialog.showModal();
    close.focus();
    return true;
  }

  function closeActionDialog(result = false, restoreFocus = true) {
    const state = activeActionDialog;
    if (!state) return;
    activeActionDialog = null;
    if (state.dialog.open) state.dialog.close();
    state.dialog.remove();
    state.resolve(Boolean(result));
    if (restoreFocus && state.returnFocus instanceof HTMLElement && state.returnFocus.isConnected) {
      requestAnimationFrame(() => state.returnFocus?.isConnected && state.returnFocus.focus({ preventScroll: true }));
    }
  }

  function showActionDialog(options = {}) {
    closeActionDialog(false, false);
    const title = String(options.title || words().confirmTitle);
    const message = String(options.message || words().confirmDescription);
    const closeOnly = options.closeOnly === true;
    const dangerous = options.dangerous === true;
    const returnFocus = options.returnFocus || document.activeElement;

    return new Promise(resolve => {
      const dialog = document.createElement("dialog");
      const identity = `action-dialog-${Date.now()}`;
      dialog.className = `action-dialog${dangerous ? " action-dialog--danger" : ""}`;
      dialog.setAttribute("aria-labelledby", `${identity}-title`);
      dialog.setAttribute("aria-describedby", `${identity}-message`);

      const sheet = document.createElement("section");
      sheet.className = "action-dialog__sheet";
      const header = document.createElement("header");
      const mark = document.createElement("span");
      mark.className = "action-dialog__mark";
      mark.append(makeIcon(dangerous ? "triangle-alert" : "shield-check"));
      const copy = document.createElement("div");
      const heading = document.createElement("h2");
      heading.id = `${identity}-title`;
      heading.textContent = title;
      const description = document.createElement("p");
      description.id = `${identity}-message`;
      description.textContent = message;
      copy.append(heading, description);
      const close = document.createElement("button");
      close.className = "icon-button icon-button--quiet";
      close.type = "button";
      close.setAttribute("aria-label", words().serverErrorClose);
      close.append(makeIcon("x"));
      header.append(mark, copy, close);

      const footer = document.createElement("footer");
      if (!closeOnly) {
        const cancel = document.createElement("button");
        cancel.className = "button button--quiet";
        cancel.type = "button";
        cancel.textContent = options.cancelLabel || words().confirmCancel;
        cancel.addEventListener("click", () => closeActionDialog(false));
        footer.append(cancel);
      }
      const confirm = document.createElement("button");
      confirm.className = dangerous ? "button button--danger" : "button button--primary";
      confirm.type = "button";
      confirm.textContent = options.confirmLabel || (closeOnly ? words().serverErrorClose : words().confirmAction);
      confirm.addEventListener("click", () => closeActionDialog(true, closeOnly));
      footer.append(confirm);

      sheet.append(header, footer);
      dialog.append(sheet);
      document.body.append(dialog);
      close.addEventListener("click", () => closeActionDialog(false));
      dialog.addEventListener("cancel", event => {
        event.preventDefault();
        closeActionDialog(false);
      });
      dialog.addEventListener("click", event => {
        if (event.target === dialog) closeActionDialog(false);
      });
      activeActionDialog = { dialog, resolve, returnFocus };
      dialog.showModal();
      (closeOnly ? confirm : close).focus();
    });
  }

  function confirmationLabel(submitter) {
    return submitter?.getAttribute("aria-label")?.trim() || submitter?.textContent?.trim() || words().confirmAction;
  }

  function confirmationIsDangerous(form, submitter) {
    const action = new URL(form.action || location.href, location.href).pathname;
    return Boolean(submitter?.matches(".button--danger,.danger-action") || /\/(delete|purge|discard|remove|stop)(\/|$)/.test(action));
  }

  function confirmFormSubmission(event, form, submitter) {
    const message = form.dataset.confirm || form.dataset.kubernetesConfirm;
    if (!message) return false;
    if (form.dataset.actionDialogConfirmed === "true") {
      delete form.dataset.actionDialogConfirmed;
      return false;
    }
    event.preventDefault();
    showActionDialog({
      message,
      confirmLabel: confirmationLabel(submitter),
      dangerous: confirmationIsDangerous(form, submitter),
      returnFocus: submitter || document.activeElement,
    }).then(confirmed => {
      if (!confirmed || !form.isConnected) return;
      if (form.hasAttribute("data-container-operation") && !form.elements.confirmed) {
        const value = document.createElement("input");
        value.type = "hidden";
        value.name = "confirmed";
        value.value = "yes";
        form.append(value);
      }
      form.dataset.actionDialogConfirmed = "true";
      form.requestSubmit(submitter instanceof HTMLElement && submitter.isConnected ? submitter : undefined);
    });
    return true;
  }

  function navigationOwnsPath(linkPath, path) {
    if (linkPath === "/monitor") return path === "/monitor";
    if (linkPath === "/resources/files") {
      return path === "/resources/files" || path === "/resources/trash";
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
    if (path === "/monitor/security") return true;
    if (path === "/resources/variables" ||
        path === "/config/quick-runs" ||
        path === "/config/schedules" ||
        path === "/history/runs" ||
        path === "/monitor/runs" ||
        path === "/history/audit" ||
        path === "/resources/trash") {
      return true;
    }
    return path === "/resources/files";
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

  function setNavigationProgress(busy) {
    const progress = document.querySelector("[data-navigation-progress]");
    if (!progress) return;
    progress.hidden = !busy;
    progress.toggleAttribute("aria-busy", busy);
  }

  function cancelTaskPanelRequest() {
    const request = taskPanelRequest;
    if (!request) return;
    taskPanelRequest = null;
    request.controller.abort();
    setTaskLinkBusy(request.trigger, false);
  }

  function syncPageTheme(nextDocument) {
    const pageThemes = ["custom-dashboard-admin", "custom-dashboard-public", "custom-dashboard-monitor"];
    pageThemes.forEach(className => document.body.classList.toggle(className, nextDocument.body.classList.contains(className)));
  }

  async function navigate(url, push = true, options = {}) {
    closeFileConflictDialog();
    cancelTaskPanelRequest();
    navigationRequest?.controller.abort();
    const request = {
      sequence: ++navigationSequence,
      controller: new AbortController(),
    };
    navigationRequest = request;
    navigationBusy = true;
    document.documentElement.setAttribute("aria-busy", "true");
    setNavigationProgress(true);
    const deferredData = options.deferredData ?? isDeferredDataURL(url);
    const immediate = deferredData && options.immediate === true;
    const title = options.title || navigationTitle(mainNavigationLink(url));
    const previousScrollY = window.scrollY;
    const returnFocus = options.returnFocus || document.activeElement;
    let shellCommitted = false;
    if (immediate) {
      if (push) history.pushState({ pjax: true }, "", url);
      document.title = title;
      updateShellLocation(url);
      setSidebar(false);
      window.scrollTo({ top: options.preserveScroll ? previousScrollY : 0, behavior: "auto" });
    }
    try {
      const result = await fetchDocument(url, {
        signal: request.controller.signal,
        headers: deferredData ? { "X-ScriptBoard-Data": "shell" } : {},
      });
      if (navigationRequest !== request || request.sequence !== navigationSequence) return;
      const responseURL = new URL(result.response.url || url, location.href);
      if (responseURL.pathname === "/login") {
        location.assign(result.response.url || url);
        return;
      }
      if (!result.response.ok) {
        if (showServerError(result, {
          url, method: "GET", returnFocus,
          includeClientErrors: true,
          retry: () => navigate(url, push, { ...options, returnFocus }),
        })) return;
        return;
      }
      if (!result.document) {
        showServerError(result, {
          url, method: "GET", returnFocus, force: true,
          retry: () => navigate(url, push, { ...options, returnFocus }),
        });
        return;
      }
      const nextMain = result.document.querySelector("main");
      const currentMain = document.querySelector("main");
      if (!nextMain || !currentMain) {
        showServerError(result, {
          url, method: "GET", returnFocus, force: true,
          retry: () => navigate(url, push, { ...options, returnFocus }),
        });
        return;
      }
      cleanupPage();
      syncPageTheme(result.document);
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
      window.scrollTo({ top: options.preserveScroll ? previousScrollY : 0, behavior: "auto" });
      initPage({ openFileQuickAccess: options.openFileQuickAccess === true });

      if (deferredData) {
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        if (navigationRequest !== request || request.sequence !== navigationSequence) return;
        const dataResult = await fetchDocument(result.response.url || url, { signal: request.controller.signal });
        if (navigationRequest !== request || request.sequence !== navigationSequence) return;
        const dataResponseURL = new URL(dataResult.response.url || url, location.href);
        if (dataResponseURL.pathname === "/login") {
          location.assign(dataResult.response.url || url);
          return;
        }
        if (!dataResult.response.ok) {
          if (showServerError(dataResult, {
            url, method: "GET", returnFocus,
            includeClientErrors: true,
            retry: () => navigate(url, false, { ...options, deferredData: true, returnFocus }),
          })) {
            showDeferredDataFailure(url, title);
            return;
          }
          showDeferredDataFailure(url, title);
          return;
        }
        if (!dataResult.document) {
          showServerError(dataResult, {
            url, method: "GET", returnFocus, force: true,
            retry: () => navigate(url, false, { ...options, deferredData: true, returnFocus }),
          });
          showDeferredDataFailure(url, title);
          return;
        }
        const nextRegion = dataResult.document.querySelector("[data-deferred-region]");
        const currentRegion = document.querySelector("[data-deferred-region]");
        if (!nextRegion || !currentRegion) {
          showServerError(dataResult, {
            url, method: "GET", returnFocus, force: true,
            retry: () => navigate(url, false, { ...options, deferredData: true, returnFocus }),
          });
          showDeferredDataFailure(url, title);
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
        initPage({ openFileQuickAccess: options.openFileQuickAccess === true });
      }

      if (options.focusSelector) {
        const focusTarget = document.querySelector(options.focusSelector);
        focusTarget?.focus();
        if (focusTarget instanceof HTMLInputElement && ["text", "search", "tel", "url", "password"].includes(focusTarget.type)) {
          const end = focusTarget.value.length;
          focusTarget.setSelectionRange(end, end);
        }
      }
    } catch (error) {
      if (error?.name === "AbortError" || navigationRequest !== request) return;
      if (shellCommitted) {
        showDeferredDataFailure(url, title);
      } else {
        showServerError({ response: { status: 0, url }, text: words().loadFailed }, {
          url, method: "GET", returnFocus, force: true,
          retry: () => navigate(url, push, { ...options, returnFocus }),
        });
      }
    } finally {
      if (navigationRequest === request) {
        navigationRequest = null;
        navigationBusy = false;
        document.documentElement.removeAttribute("aria-busy");
        setNavigationProgress(false);
      }
    }
  }

  function initTaskPanelMain(main, cleanups) {
    renderIcons(main);
    localizeTimes(main);
    initDirectoryPickers(main, cleanups);
    initQuickCreateDefaults(main, cleanups);
    initScheduleCron(cleanups, main);
    initExternalEntryForm(main, cleanups);
	initCopyControls(main, cleanups);
    const websiteForm = main.querySelector("[data-website-monitor-form]");
    if (websiteForm) cleanups.push(initWebsiteMonitorForm(websiteForm));
    const websiteMonitoring = main.matches("[data-website-monitoring],[data-website-detail]")
      ? main
      : main.querySelector("[data-website-monitoring],[data-website-detail]");
    if (websiteMonitoring) cleanups.push(initWebsiteMonitoring(websiteMonitoring));
    const websiteNginx = main.matches("[data-website-nginx]")
      ? main
      : main.querySelector("[data-website-nginx]");
    if (websiteNginx) cleanups.push(initWebsiteNginx(websiteNginx));
    const websiteConfigTransfer = main.matches("[data-website-config-transfer]")
      ? main
      : main.querySelector("[data-website-config-transfer]");
    if (websiteConfigTransfer) cleanups.push(initWebsiteConfigTransfer(websiteConfigTransfer));
  }

  function initExternalEntryForm(root = document, cleanups = []) {
    const form = root.querySelector("[data-external-entry-form]");
    if (!form || form.dataset.externalReady) return;
    form.dataset.externalReady = "true";
    const action = form.querySelector("[data-external-action-type]");
    const variableType = form.querySelector("[data-external-variable-type]");
    const logTargetModes = [...form.querySelectorAll("[data-external-log-target-mode]")];
    const logCustomFile = form.querySelector("[data-external-log-custom-file]");
    const logRotation = form.querySelector("[data-external-log-rotate]");
    const logRotationFields = form.querySelector("[data-external-log-rotation-fields]");
    const refresh = () => {
      form.querySelectorAll("[data-external-action-fields]").forEach(section => {
        const active = section.dataset.externalActionFields === action?.value;
        section.hidden = !active;
        section.querySelectorAll("input,select,textarea").forEach(field => field.disabled = !active);
      });
      form.querySelectorAll("[data-external-variable-fields]").forEach(section => {
        const active = action?.value === "variable" && section.dataset.externalVariableFields === variableType?.value;
        section.hidden = !active;
        section.querySelectorAll("input,select,textarea").forEach(field => field.disabled = !active);
      });
      const logActive = action?.value === "log";
      const customTarget = logTargetModes.find(field => field.checked)?.value !== "managed";
      if (logCustomFile) {
        logCustomFile.hidden = !customTarget;
        const input = logCustomFile.querySelector('input[name="log_file"]');
        if (input) {
          input.disabled = !logActive || !customTarget;
          input.required = logActive && customTarget;
        }
      }
      if (logRotationFields) {
        const rotationActive = logActive && Boolean(logRotation?.checked);
        logRotationFields.hidden = !rotationActive;
        logRotationFields.querySelectorAll("input").forEach(field => field.disabled = !rotationActive);
      }
    };
    action?.addEventListener("change", refresh);
    variableType?.addEventListener("change", refresh);
    logTargetModes.forEach(field => field.addEventListener("change", refresh));
    logRotation?.addEventListener("change", refresh);
    cleanups.push(() => action?.removeEventListener("change", refresh));
    cleanups.push(() => variableType?.removeEventListener("change", refresh));
    cleanups.push(() => logTargetModes.forEach(field => field.removeEventListener("change", refresh)));
    cleanups.push(() => logRotation?.removeEventListener("change", refresh));
    refresh();
  }

  function initDirectoryPickers(scope, cleanups) {
    scope.querySelectorAll("[data-directory-picker]").forEach(root => {
      const input = root.querySelector('input[name="working_directory"]');
      const tree = root.querySelector("[data-directory-tree]");
      const selection = root.querySelector("[data-directory-selection]");
      if (!input || !tree || !selection) return;
      let controller = null;
      const nodes = new Map();
      const normalizePath = value => String(value || "");
      let selectedPath = normalizePath(input.value);

      const setSelected = (path, focus = false) => {
        selectedPath = normalizePath(path);
        input.value = selectedPath;
        selection.textContent = selectedPath || (root.dataset.rootLabel || "This host");
        nodes.forEach(node => node.row.setAttribute("aria-selected", String(node.path === selectedPath)));
        if (focus) nodes.get(selectedPath)?.row.focus({ preventScroll: true });
      };

      const setExpanded = (node, expanded) => {
        if (!node.loaded || node.leaf) return;
        node.row.setAttribute("aria-expanded", String(expanded));
        node.group.hidden = !expanded;
      };

      const createNode = (path, label) => {
        const item = document.createElement("li");
        item.setAttribute("role", "none");

        const row = document.createElement("button");
        row.type = "button";
        row.className = "directory-tree-picker__item";
        row.setAttribute("role", "treeitem");
        row.setAttribute("aria-selected", String(path === selectedPath));
        row.setAttribute("aria-expanded", "false");
        row.dataset.directoryPath = path;

        const chevron = document.createElement("span");
        chevron.className = "directory-tree-picker__chevron";
        chevron.dataset.lucide = "chevron-right";
        chevron.setAttribute("aria-hidden", "true");
        const folder = document.createElement("span");
        folder.dataset.lucide = path === "" ? "hard-drive" : "folder";
        folder.setAttribute("aria-hidden", "true");
        const name = document.createElement("span");
        name.textContent = label;
        const check = document.createElement("span");
        check.className = "directory-tree-picker__check";
        check.dataset.lucide = "check";
        check.setAttribute("aria-hidden", "true");
        row.append(chevron, folder, name, check);

        const group = document.createElement("ul");
        group.setAttribute("role", "group");
        group.hidden = true;
        item.append(row, group);

        const node = { path, item, row, group, loaded: false, leaf: false };
        nodes.set(path, node);
        row.addEventListener("click", () => {
          if (path) setSelected(path);
          if (!node.loaded) {
            loadNode(node, true);
            return;
          }
          setExpanded(node, row.getAttribute("aria-expanded") !== "true");
        });
        return node;
      };

      const loadNode = async (node, expand) => {
        if (node.loaded) {
          setExpanded(node, expand);
          return true;
        }
        controller?.abort();
        controller = new AbortController();
        node.row.setAttribute("aria-busy", "true");
        node.group.hidden = false;
        const loading = document.createElement("li");
        loading.className = "directory-tree-picker__status";
        loading.textContent = root.dataset.loadingLabel || "Loading directories…";
        node.group.replaceChildren(loading);
        try {
          const endpoint = new URL(root.dataset.endpoint, location.href);
          endpoint.searchParams.set("path", node.path);
          if (root.dataset.excludePath) endpoint.searchParams.set("exclude", root.dataset.excludePath);
          const response = await fetch(endpoint, { headers: { Accept: "application/json" }, cache: "no-store", signal: controller.signal });
          if (!response.ok) throw new Error(await response.text());
          const payload = await response.json();
          const directories = Array.isArray(payload.directories) ? payload.directories : [];
          node.group.replaceChildren();
          directories.forEach(directory => {
            if (!directory || typeof directory.path !== "string") return;
            node.group.append(createNode(directory.path, directory.name || directory.path).item);
          });
          node.loaded = true;
          node.leaf = directories.length === 0;
          if (node.leaf) {
            node.row.removeAttribute("aria-expanded");
            node.group.hidden = true;
          } else {
            setExpanded(node, expand);
          }
          renderIcons(node.item);
          return true;
        } catch (error) {
          if (error?.name === "AbortError") return false;
          const failure = document.createElement("li");
          failure.className = "directory-tree-picker__status";
          failure.textContent = error?.message || words().loadFailed;
          node.group.replaceChildren(failure);
          node.group.hidden = false;
          return false;
        } finally {
          node.row.removeAttribute("aria-busy");
        }
      };

      const list = document.createElement("ul");
      list.setAttribute("role", "tree");
      list.setAttribute("aria-label", tree.getAttribute("aria-label") || "");
      const rootNode = createNode("", root.dataset.rootLabel || "This host");
      list.append(rootNode.item);
      tree.replaceChildren(list);
      renderIcons(tree);
      setSelected(selectedPath);

      const revealSelection = async () => {
        let currentNode = rootNode;
        if (!await loadNode(currentNode, true)) return;
        if (!selectedPath) return;
        const comparable = value => String(value || "").replaceAll("\\", "/").replace(/\/+$/, "").toLocaleLowerCase();
        while (!nodes.has(selectedPath)) {
          const selectedKey = comparable(selectedPath);
          const candidates = [...currentNode.group.querySelectorAll(":scope > li > [data-directory-path]")]
            .map(row => nodes.get(row.dataset.directoryPath))
            .filter(Boolean);
          const nextNode = candidates.find(node => {
            const key = comparable(node.path);
            return selectedKey === key || selectedKey.startsWith(`${key}/`) || key === "";
          });
          if (!nextNode || nextNode === currentNode || !await loadNode(nextNode, true)) return;
          currentNode = nextNode;
        }
        setSelected(selectedPath, root.hasAttribute("data-autofocus"));
      };

      const onKeydown = event => {
        const row = event.target.closest('[role="treeitem"]');
        if (!row || !tree.contains(row)) return;
        const visibleRows = Array.from(tree.querySelectorAll('[role="treeitem"]')).filter(item => item.offsetParent !== null);
        const index = visibleRows.indexOf(row);
        if (event.key === "ArrowDown" && index < visibleRows.length - 1) {
          event.preventDefault();
          visibleRows[index + 1].focus();
        } else if (event.key === "ArrowUp" && index > 0) {
          event.preventDefault();
          visibleRows[index - 1].focus();
        } else if (event.key === "Home") {
          event.preventDefault();
          visibleRows[0]?.focus();
        } else if (event.key === "End") {
          event.preventDefault();
          visibleRows.at(-1)?.focus();
        } else if (event.key === "ArrowRight") {
          event.preventDefault();
          const node = nodes.get(row.dataset.directoryPath);
          if (node && !node.leaf) loadNode(node, true);
        } else if (event.key === "ArrowLeft") {
          const node = nodes.get(row.dataset.directoryPath);
          if (!node) return;
          event.preventDefault();
          if (row.getAttribute("aria-expanded") === "true") {
            setExpanded(node, false);
          } else {
            node.item.parentElement?.closest("li")?.querySelector(':scope > [role="treeitem"]')?.focus();
          }
        }
      };
      tree.addEventListener("keydown", onKeydown);
      revealSelection();
      cleanups.push(() => {
        controller?.abort();
        tree.removeEventListener("keydown", onKeydown);
      });
    });
  }

  function initQuickCreateDefaults(scope, cleanups) {
    const roots = [
      ...(scope.matches?.('[data-task-kind="quick-create"]') ? [scope] : []),
      ...scope.querySelectorAll('[data-task-kind="quick-create"]'),
    ];
    roots.forEach(root => {
      const fileName = root.querySelector('input[name="file_name"]');
      const quickName = root.querySelector("[data-quick-name]");
      if (!fileName || !quickName) return;
      let followsFileName = quickName.value.trim() === "";
      const onFileName = () => {
        if (followsFileName) quickName.value = fileName.value.trim();
      };
      const onQuickName = () => {
        followsFileName = quickName.value.trim() === "" || quickName.value === fileName.value.trim();
      };
      fileName.addEventListener("input", onFileName);
      quickName.addEventListener("input", onQuickName);
      onFileName();
      cleanups.push(() => {
        fileName.removeEventListener("input", onFileName);
        quickName.removeEventListener("input", onQuickName);
      });
    });
  }

  function removeWebsiteTaskPanelBackLink(taskMain) {
    if (!taskMain.dataset.taskKind?.startsWith("website-")) return;
    taskMain.querySelector(':scope > .website-task-heading > a[href="/monitor/websites"], .website-detail-back')?.remove();
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
    const outerClose = host.querySelector(".task-panel-close");
    outerClose.setAttribute("aria-label", closeLabel);
    const panel = host.querySelector(".task-panel");
    if (main.dataset.taskKind) panel.classList.add(`task-panel--${main.dataset.taskKind}`);
    const panelMain = document.importNode(main, true);
    const innerClose = panelMain.querySelector(".task-sheet > header > .icon-button");
    innerClose?.remove();
    removeWebsiteTaskPanelBackLink(panelMain);
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
    outerClose.focus();
    background.forEach(({ child }) => {
      child.inert = true;
      child.setAttribute("aria-hidden", "true");
    });
    renderIcons(outerClose);
    initTaskPanelMain(panelMain, cleanups);
  }

  async function refreshTaskPanelMain(url, expectedKind, preferredFocusKey = "") {
    const state = taskPanelState;
    const panel = state?.host.querySelector("[data-task-panel]");
    const currentMain = panel?.querySelector("main[data-task-page]");
    if (!state || !panel || !currentMain || (expectedKind && currentMain.dataset.taskKind !== expectedKind)) return null;

    const currentScroller = currentMain.querySelector("[data-website-detail-scroll]") || panel;
    const scrollTop = currentScroller.scrollTop;
    const focused = document.activeElement;
    const focusKey = preferredFocusKey || (focused instanceof HTMLElement && currentMain.contains(focused)
      ? focused.dataset.websiteFocusKey || ""
      : "");
    const refreshError = currentMain.dataset.refreshFailed || words().loadFailed;
    const result = await fetchDocument(url, { cache: "no-store" });
    if (taskPanelState !== state || !result.response.ok) throw new Error(refreshError);
    const nextMain = result.document?.querySelector(`main[data-task-page]${expectedKind ? `[data-task-kind="${expectedKind}"]` : ""}`);
    if (!nextMain) throw new Error(refreshError);

    state.cleanups.splice(0).forEach(cleanup => cleanup());
    const imported = document.importNode(nextMain, true);
    removeWebsiteTaskPanelBackLink(imported);
    const heading = imported.querySelector("h1");
    if (heading) {
      heading.id ||= `task-panel-heading-${Date.now()}`;
      panel.setAttribute("aria-labelledby", heading.id);
    }
    currentMain.replaceWith(imported);
    initTaskPanelMain(imported, state.cleanups);
    const nextScroller = imported.querySelector("[data-website-detail-scroll]") || panel;
    nextScroller.scrollTop = scrollTop;
    if (focusKey) {
      const focusTarget = imported.querySelector(`[data-website-focus-key="${CSS.escape(focusKey)}"]`);
      focusTarget?.focus({ preventScroll: true });
    }
    return imported;
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
          if (showServerError(result, {
            url: destination,
            method: "GET",
            returnFocus: trigger,
            includeClientErrors: true,
            force: result.response.ok,
            retryLabel: words().serverErrorRetryTask,
            retry: () => openTask(destination, push, trigger),
          })) return;
          return;
        }
        if (taskPanelRequest !== request) return;
        buildTaskPanel(main, result.response.url, push);
      } catch (error) {
        if (error?.name !== "AbortError" && taskPanelRequest === request) {
          showServerError({ response: { status: 0, url: destination }, text: words().loadFailed }, {
            url: destination,
            method: "GET",
            returnFocus: trigger,
            force: true,
            retryLabel: words().serverErrorRetryTask,
            retry: () => openTask(destination, push, trigger),
          });
        }
      } finally {
        setTaskLinkBusy(trigger, false);
        if (taskPanelRequest === request) taskPanelRequest = null;
      }
    })();
    return request.promise;
  }

  async function refreshExternalGroup(groupID, options = {}) {
    if (!groupID) return false;
    const selector = `[data-external-group-id="${CSS.escape(groupID)}"]`;
    const currentGroup = document.querySelector(selector);
    if (!currentGroup) return false;
    const returnFocus = options.returnFocus || document.activeElement;
    try {
      const result = await fetchDocument("/config/external-interfaces", { cache: "no-store" });
      const nextGroup = result.document?.querySelector(selector);
      if (!result.response.ok || !nextGroup) {
        showServerError(result, {
          url: "/config/external-interfaces", method: "GET", returnFocus,
          includeClientErrors: true, force: true,
          retry: () => refreshExternalGroup(groupID, options),
        });
        return false;
      }
      const imported = document.importNode(nextGroup, true);
      imported.open = currentGroup.open || options.reopenManager === true;
      currentGroup.replaceWith(imported);
      renderIcons(imported);
      localizeTimes(imported);
      if (options.reopenManager) {
        const manager = imported.querySelector("details[data-external-key-manager]");
        if (manager) {
          manager.open = true;
          manager.querySelector(":scope > summary")?.setAttribute("aria-expanded", "true");
          document.body.style.overflow = "hidden";
          requestAnimationFrame(() => manager.querySelector(".external-key-manager__sheet")?.focus());
        }
      } else {
        (options.returnFocusSelector ? imported.querySelector(options.returnFocusSelector) : imported.querySelector(":scope > summary"))?.focus({ preventScroll: true });
      }
      return true;
    } catch {
      showServerError({ response: { status: 0, url: "/config/external-interfaces" }, text: words().loadFailed }, {
        url: "/config/external-interfaces", method: "GET", returnFocus, force: true,
        retry: () => refreshExternalGroup(groupID, options),
      });
      return false;
    }
  }

  function closeTaskPanel(useHistory = true, restoreFocus = true) {
    if (!taskPanelState) return;
    const { host, cleanups = [], background = [], returnFocus, returnURL } = taskPanelState;
    const refreshAfterClose = useHistory && host.querySelector("main[data-task-refresh-on-close]");
    const taskMain = host.querySelector("main[data-task-page]");
    const groupID = refreshAfterClose ? taskMain?.dataset.externalGroupId || "" : "";
    const reopenManager = Boolean(groupID && taskMain?.dataset.taskKind?.startsWith("external-key-"));
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
    if (useHistory && history.state?.task) {
      if (groupID) taskPanelGroupRefreshAfterClose = { groupID, reopenManager };
      else if (refreshAfterClose) taskPanelRefreshAfterCloseURL = returnURL;
      taskPanelHistoryClosePending = true;
      history.back();
    } else if (groupID) {
      refreshExternalGroup(groupID, { reopenManager });
    } else if (refreshAfterClose) {
      navigate(returnURL, false);
    }
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

  function closeFileConflictDialog() {
    const dialog = activeFileConflictDialog;
    if (!dialog) return;
    activeFileConflictDialog = null;
    if (dialog.open) dialog.close();
    dialog.remove();
  }

  function openDocumentFileConflict(main) {
    closeFileConflictDialog();
    const sheet = main.querySelector(".file-conflict-sheet");
    if (!sheet) return false;
    const dialog = document.createElement("dialog");
    dialog.className = "file-conflict-dialog";
    dialog.append(document.importNode(sheet, true));
    activeFileConflictDialog = dialog;
    document.body.append(dialog);
    const skip = dialog.querySelector("[data-conflict-skip]");
    skip?.addEventListener("click", event => {
      event.preventDefault();
      closeFileConflictDialog();
    });
    dialog.addEventListener("close", () => {
      if (activeFileConflictDialog === dialog) {
        activeFileConflictDialog = null;
        dialog.remove();
      }
    }, { once: true });
    renderIcons(dialog);
    dialog.showModal();
    skip?.focus();
    return true;
  }

  function chooseUploadConflict(conflicts) {
    closeFileConflictDialog();
    return new Promise(resolve => {
      const dialog = document.createElement("dialog");
      dialog.className = "file-conflict-dialog";
      const sheet = document.createElement("section");
      sheet.className = "file-conflict-sheet";
      const header = document.createElement("header");
      const icon = document.createElement("span");
      icon.className = "file-conflict-icon";
      icon.dataset.lucide = "files";
      icon.setAttribute("aria-hidden", "true");
      const copy = document.createElement("div");
      const heading = document.createElement("h2");
      heading.id = `file-conflict-title-${Date.now()}`;
      heading.textContent = words().conflictTitle;
      const description = document.createElement("p");
      description.textContent = `${words().conflictDescription} ${words().conflictBatchDescription}`;
      copy.append(heading, description);
      const close = document.createElement("button");
      close.className = "icon-button";
      close.type = "button";
      close.setAttribute("aria-label", words().conflictClose);
      close.append(makeIcon("x"));
      header.append(icon, copy, close);

      const list = document.createElement("ul");
      list.className = "file-conflict-list";
      conflicts.slice(0, 6).forEach(conflict => {
        const item = document.createElement("li");
        const current = document.createElement("code");
        current.textContent = conflict.name;
        const arrow = makeIcon("arrow-right");
        const suggested = document.createElement("code");
        suggested.textContent = conflict.suggested;
        item.append(current, arrow, suggested);
        list.append(item);
      });
      if (conflicts.length > 6) {
        const more = document.createElement("li");
        more.className = "file-conflict-list__more";
        more.textContent = `+${conflicts.length - 6} ${words().conflictMore}`;
        list.append(more);
      }

      const note = document.createElement("p");
      note.className = "file-conflict-note";
      const noteCopy = document.createElement("span");
      noteCopy.textContent = words().conflictOverwriteNote;
      note.append(makeIcon("archive-restore"), noteCopy);
      const canOverwrite = conflicts.every(conflict => conflict.canOverwrite);
      if (!canOverwrite) {
        const unavailable = document.createElement("span");
        unavailable.className = "file-conflict-note__detail";
        unavailable.textContent = words().conflictOverwriteUnavailable;
        noteCopy.append(unavailable);
      }

      const footer = document.createElement("footer");
      const skip = document.createElement("button");
      skip.className = "button button--quiet";
      skip.type = "button";
      skip.textContent = words().conflictSkip;
      const overwrite = document.createElement("button");
      overwrite.className = "button button--danger";
      overwrite.type = "button";
      overwrite.disabled = !canOverwrite;
      overwrite.append(makeIcon("archive-restore"), document.createTextNode(words().conflictOverwrite));
      const rename = document.createElement("button");
      rename.className = "button button--primary";
      rename.type = "button";
      rename.append(makeIcon("file-pen-line"), document.createTextNode(words().conflictRename));
      footer.append(skip, overwrite, rename);
      sheet.append(header, list, note, footer);
      dialog.append(sheet);
      dialog.setAttribute("aria-labelledby", heading.id);
      document.body.append(dialog);
      activeFileConflictDialog = dialog;

      let settled = false;
      const finish = value => {
        if (settled) return;
        settled = true;
        activeFileConflictDialog = null;
        if (dialog.open) dialog.close();
        dialog.remove();
        resolve(value);
      };
      close.addEventListener("click", () => finish(""));
      skip.addEventListener("click", () => finish("skip"));
      overwrite.addEventListener("click", () => finish("overwrite"));
      rename.addEventListener("click", () => finish("rename"));
      dialog.addEventListener("cancel", event => {
        event.preventDefault();
        finish("");
      });
      renderIcons(dialog);
      dialog.showModal();
      skip.focus();
    });
  }

  function closeUploadResultsDialog(refresh = true) {
    const state = activeUploadResultsDialog;
    if (!state) return;
    activeUploadResultsDialog = null;
    if (state.dialog.open) state.dialog.close();
    state.dialog.remove();
    if (refresh) navigate(state.refreshURL, false, { preserveScroll: true });
  }

  function showUploadResults(main) {
    const source = main?.querySelector(".upload-results-sheet");
    const refreshURL = main?.dataset.refreshUrl;
    if (!source || !refreshURL) return false;
    closeUploadResultsDialog(false);
    const dialog = document.createElement("dialog");
    dialog.className = "upload-results-dialog";
    dialog.setAttribute("aria-labelledby", "upload-results-title");
    dialog.append(document.importNode(source, true));
    document.body.append(dialog);
    activeUploadResultsDialog = { dialog, refreshURL };
    const close = () => closeUploadResultsDialog(true);
    dialog.querySelectorAll("[data-upload-results-close]").forEach(control => {
      control.addEventListener("click", event => {
        event.preventDefault();
        close();
      });
    });
    dialog.addEventListener("cancel", event => {
      event.preventDefault();
      close();
    });
    dialog.addEventListener("close", () => {
      if (activeUploadResultsDialog?.dialog === dialog) close();
    }, { once: true });
    renderIcons(dialog);
    dialog.showModal();
    dialog.querySelector("[data-upload-results-close]")?.focus();
    return true;
  }

  async function submitFileUpload(form) {
    const data = new FormData(form);
    const files = data.getAll("files").filter(value => value instanceof File && value.name);
    const preflight = new URLSearchParams();
    preflight.set("csrf_token", String(data.get("csrf_token") || ""));
    preflight.set("path", String(data.get("path") || ""));
    files.forEach(file => preflight.append("name", file.name));
    let action = "skip";
    try {
      const response = await fetch("/resources/files/conflicts", {
        method: "POST",
        body: preflight,
        credentials: "same-origin",
        headers: { "Accept": "application/json" },
      });
      if (response.ok) {
        const payload = await response.json();
        const conflicts = Array.isArray(payload.conflicts) ? payload.conflicts : [];
        if (conflicts.length) {
          action = await chooseUploadConflict(conflicts);
          if (!action) {
            resetSubmit(form);
            form.dispatchEvent(new CustomEvent("file-upload-cancelled"));
            return;
          }
        }
      }
    } catch { /* the upload remains safe because the server defaults to skip */ }
    const actionInput = form.querySelector('input[name="conflict_action"]');
    if (actionInput) actionInput.value = action;
    data.set("conflict_action", action);
    try {
      const result = await fetchDocument(form.action, { method: "POST", body: data });
      const resultsMain = result.document?.querySelector("main[data-upload-results]");
      if (resultsMain && result.response.ok) {
        const submittedFromTask = taskPanelState?.host.contains(form);
        if (submittedFromTask) closeTaskPanel(false, false);
        form.querySelectorAll('input[type="file"]').forEach(file => { file.value = ""; });
        resetSubmit(form);
        if (showUploadResults(resultsMain)) return;
      }
      if (showServerError(result, {
        url: form.action,
        method: "POST",
        returnFocus: form.querySelector('button[type="submit"]') || form,
        retryLabel: words().serverErrorRetryAction,
        retryIcon: "rotate-ccw",
        retry: () => submitFileUpload(form),
      })) return;
      throw new Error(`Upload failed (${result.response.status})`);
    } catch {
      form.dispatchEvent(new CustomEvent("file-upload-failed", { detail: { message: words().submitFailed } }));
      if (!form.hasAttribute("data-file-drop-form")) {
        form.querySelector("[data-async-submit-error]")?.remove();
        const message = document.createElement("p");
        message.className = "async-submit-error";
        message.dataset.asyncSubmitError = "";
        message.setAttribute("role", "alert");
        message.tabIndex = -1;
        message.textContent = words().submitFailed;
        form.prepend(message);
        message.focus();
      }
    } finally {
      resetSubmit(form);
    }
  }

  async function submitAsync(form, submitter, options = {}) {
    form.querySelector("[data-async-submit-error]")?.remove();
    const submittingTaskState = taskPanelState?.host.contains(form) ? taskPanelState : null;
    const externalGroupID = form.closest("[data-external-group-id]")?.dataset.externalGroupId || "";
    const reopenExternalKeyManager = Boolean(
      form.closest("[data-external-key-manager]") ||
      form.closest('main[data-task-kind^="external-key-"]'),
    );
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
      const fileConflict = result.document?.querySelector("main[data-file-conflict]");
      if (fileConflict && openDocumentFileConflict(fileConflict)) return;
      const responseMain = result.document?.querySelector("main");
      const isTaskValidation = result.response.status === 422 && responseMain &&
        !responseMain.matches(".error-page");
      if (!result.response.ok && !isTaskValidation) {
        const retryable = form.hasAttribute("data-server-error-retry");
        showServerError(result, {
          url: action,
          method: form.method,
          returnFocus: submitter || form,
          includeClientErrors: true,
          force: true,
          fallbackMessage: words().submitFailed,
          retryLabel: retryable ? words().serverErrorRetryAction : words().serverErrorRefresh,
          retryIcon: retryable ? "rotate-ccw" : "refresh-cw",
          retry: retryable
            ? () => submitAsync(form, submitter, options)
            : () => navigate(location.href, false),
        });
        return;
      }
      if (result.response.redirected && result.response.ok) {
        const destination = result.response.url;
        if (options.fullNavigationOnSuccess) {
          location.assign(destination);
          return;
        }
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
          if (externalGroupID && new URL(destination, location.href).pathname === "/config/external-interfaces") {
            taskPanelGroupRefreshAfterClose = { groupID: externalGroupID, reopenManager: reopenExternalKeyManager };
            closeTaskPanel(true);
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
        } else if (externalGroupID && new URL(destination, location.href).pathname === "/config/external-interfaces") {
          await refreshExternalGroup(externalGroupID, { reopenManager: reopenExternalKeyManager });
        } else {
          await navigate(destination, true);
        }
        return;
      }
      const nextMain = responseMain;
      if (nextMain) {
        if (options.fullNavigationOnSuccess && submittingTaskState && !nextMain.matches("[data-task-page]")) {
          closeTaskPanel(false);
          document.querySelector("main")?.replaceWith(document.importNode(nextMain, true));
          document.title = result.document.title;
          history.replaceState({ pjax: true }, "", result.response.url || submittingTaskState.returnURL);
          initPage();
          return;
        }
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

  function showUpdateReconnect(main, title, description) {
    if (!main) return;
    main.querySelector(".update-reconnect")?.remove();
    const notice = document.createElement("aside");
    notice.className = "update-reconnect";
    notice.setAttribute("role", "status");
    notice.setAttribute("aria-live", "polite");
    notice.innerHTML = '<span data-lucide="loader-circle" aria-hidden="true"></span><div><strong></strong><p></p></div>';
    notice.querySelector("strong").textContent = title;
    notice.querySelector("p").textContent = description;
    main.prepend(notice);
    renderIcons(notice);
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
      showUpdateReconnect(main, main?.dataset.updateInstallingTitle, main?.dataset.updateInstallingDescription);
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
      showActionDialog({
        title,
        message: error?.message || words().submitFailed,
        confirmLabel: words().serverErrorClose,
        closeOnly: true,
        dangerous: true,
      });
      resetSubmit(form);
    }
  }

  async function submitServiceRestart(form, submitter) {
    const data = new FormData(form);
    if (submitter?.name) data.set(submitter.name, submitter.value);
    const main = document.querySelector("[data-updates-page]");
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
      showUpdateReconnect(main, main?.dataset.restartTitle, main?.dataset.restartDescription);
      let delay = 750;
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
            if (snapshot.instance_id && snapshot.instance_id !== payload.instance_id) {
              location.assign("/settings/updates");
              return;
            }
          }
        } catch {
          // The expected disconnect is resolved by the next poll.
        }
        delay = Math.min(5000, delay + 750);
      }
      location.assign("/settings/updates");
    } catch (error) {
      const title = main?.dataset.restartError || "Unable to restart the service.";
      showActionDialog({
        title,
        message: error?.message || words().submitFailed,
        confirmLabel: words().serverErrorClose,
        closeOnly: true,
        dangerous: true,
      });
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
      runtimeReasons: {
        not_running: "应用当前未运行",
        detail_probe_unavailable: "运行详情采集器不可用",
        detail_unavailable: "运行详情未返回可读结果",
        unsupported_application_kind: "暂不支持此类应用的运行详情",
        process_exited: "进程已在采集期间退出",
        container_exited: "容器已在采集期间退出",
        identity_restricted: "进程身份受系统权限限制",
        permission_denied: "当前服务账户无权读取这些事实",
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
      runtimeReasons: {
        not_running: "The application is not running",
        detail_probe_unavailable: "The runtime detail collector is unavailable",
        detail_unavailable: "The runtime detail collector returned no readable result",
        unsupported_application_kind: "Runtime details are not supported for this application kind",
        process_exited: "The process exited while details were collected",
        container_exited: "The container exited while details were collected",
        identity_restricted: "The process identity is restricted by the operating system",
        permission_denied: "The service account cannot read these runtime facts",
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
  const safeInternalURL = (value, fallback) => {
    try {
      const parsed = new URL(String(value || fallback), location.origin);
      return parsed.origin === location.origin ? `${parsed.pathname}${parsed.search}${parsed.hash}` : fallback;
    } catch (_) {
      return fallback;
    }
  };
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
      return `<span><i class="application-series application-series--${item.color}"></i>${escapeMarkup(item.label)} ${escapeMarkup(formatter(average))}</span>`;
    }).join("");
    const paths = series.map(item =>
      `<path class="application-series application-series--${item.color}" d="${applicationSeriesPath(points, item.key, maximum)}"></path>`
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
        { key: "cpuAverage", label: applicationWords().average, color: "accent" },
        { key: "cpuMaximum", label: applicationWords().maximum, color: "faint" }
      ], value => `${Number(value).toFixed(1)}%`)}
      ${applicationChart(applicationWords().memory, points, [
        { key: "memoryAverage", label: applicationWords().average, color: "accent" },
        { key: "memoryMaximum", label: applicationWords().maximum, color: "faint" }
      ], formatApplicationBytes)}
      ${applicationChart(applicationWords().disk, points, [
        { key: "readAverage", label: applicationWords().read, color: "accent" },
        { key: "writeAverage", label: applicationWords().write, color: "warning" }
      ], formatApplicationRate)}
    </div>`;
  }

  function renderApplicationRuntime(payload) {
    const runtime = applicationValue(payload, "runtime") || {};
    const state = applicationValue(runtime, "state") || "unavailable";
    const reasonCode = applicationValue(runtime, "code") || "";
    const showNotice = state !== "available" && state !== "partial";
    const reason = showNotice
      ? applicationWords().runtimeReasons[reasonCode] || applicationWords().unavailable
      : "";
    const facts = applicationValue(runtime, "kind") === "docker"
      ? applicationValue(runtime, "docker") || {}
      : applicationValue(runtime, "host") || {};
    const notice = showNotice ? `<div class="application-runtime-notice" data-state="${escapeMarkup(state)}">
      <span data-lucide="${state === "restricted" ? "shield-alert" : "info"}" aria-hidden="true"></span>
      <div><strong>${escapeMarkup(reason)}</strong>
      <p>${escapeMarkup(reasonCode)}</p></div>
    </div>` : "";
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
    const drawerHost = root.querySelector("[data-application-drawer]");
    const drawer = drawerHost?.querySelector(".application-drawer");
    const drawerNavigation = drawerHost?.querySelector("[data-application-drawer-navigation]");
    const detailTabs = Array.from(drawerHost?.querySelectorAll("[data-application-detail-tab]") || []);
    const detailPanels = Array.from(drawerHost?.querySelectorAll("[data-application-detail-panel]") || []);
    const historyOutput = drawerHost?.querySelector("[data-application-history-output]");
    const runtimeOutput = drawerHost?.querySelector("[data-application-runtime-output]");
    const detailTimes = Array.from(drawerHost?.querySelectorAll("[data-application-detail-time]") || []);
    let opener = null;
    let activeRange = "1h";
    let activeMode = "pinned";
    let drawerRequestToken = 0;

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
        const source = snapshot.querySelector("[data-pinned-applications]");
        const target = root.querySelector("[data-pinned-applications]");
        if (source && target) {
          target.replaceChildren(...Array.from(source.children, child => document.importNode(child, true)));
        }
      } else {
        const sourceList = snapshot.querySelector("[data-running-applications-list]");
        const targetList = root.querySelector("[data-running-applications-list]");
        if (sourceList && targetList) targetList.replaceChildren(...Array.from(sourceList.children, child => document.importNode(child, true)));
        const sourceSummary = snapshot.querySelector(".application-result-summary");
        const targetSummary = root.querySelector(".application-result-summary");
        if (sourceSummary && targetSummary) targetSummary.replaceChildren(...Array.from(sourceSummary.childNodes, child => document.importNode(child, true)));
      }
      if (drawerHost?.classList.contains("is-open")) {
        opener = Array.from(root.querySelectorAll("[data-application-row]")).find(candidate =>
          candidate.dataset.applicationId === drawerHost.dataset.applicationId &&
          candidate.dataset.applicationMode === drawerHost.dataset.applicationMode
        ) || opener;
        opener?.classList.add("is-selected");
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

    const selectDetailTab = tabName => {
      detailTabs.forEach(tab => {
        const selected = tab.dataset.applicationDetailTab === tabName;
        tab.setAttribute("aria-selected", String(selected));
        tab.tabIndex = selected ? 0 : -1;
      });
      detailPanels.forEach(panel => {
        panel.hidden = panel.dataset.applicationDetailPanel !== tabName;
      });
    };

    const markDetailFetched = () => {
      const value = new Intl.DateTimeFormat(locale(), {
        hour: "2-digit", minute: "2-digit", second: "2-digit"
      }).format(new Date());
      detailTimes.forEach(time => {
        time.dateTime = new Date().toISOString();
        time.textContent = value;
      });
    };

    const renderDrawerDetails = (payload, range) => {
      if (activeMode === "pinned" && historyOutput) historyOutput.innerHTML = renderApplicationHistory(payload);
      if (runtimeOutput) runtimeOutput.innerHTML = renderApplicationRuntime(payload);
      drawerHost.querySelectorAll("[data-application-range]").forEach(button => {
        button.setAttribute("aria-pressed", String(button.dataset.applicationRange === range));
      });
      markDetailFetched();
      renderIcons(drawerHost);
      localizeTimes(drawerHost);
    };

    const drawerErrorMarkup = error =>
      `<div class="application-detail-error" role="alert"><span data-lucide="triangle-alert" aria-hidden="true"></span><div><strong>${escapeMarkup(applicationWords().loadFailed)}</strong><p>${escapeMarkup(error?.message || "")}</p><button class="button button--compact" type="button" data-application-detail-retry>${escapeMarkup(applicationWords().retry)}</button></div></div>`;

    const showDrawerError = error => {
      const markup = drawerErrorMarkup(error);
      if (activeMode === "pinned" && historyOutput) historyOutput.innerHTML = markup;
      if (runtimeOutput) runtimeOutput.innerHTML = markup;
      renderIcons(drawerHost);
    };

    const setDrawerLoading = () => {
      if (activeMode === "pinned" && historyOutput) historyOutput.innerHTML = applicationDetailLoading();
      if (runtimeOutput) runtimeOutput.innerHTML = applicationDetailLoading();
      renderIcons(drawerHost);
    };

    const updateDrawerHeader = row => {
      const values = {
        "[data-application-drawer-name]": row.dataset.applicationName,
        "[data-application-drawer-kind]": row.dataset.applicationKindLabel,
        "[data-application-drawer-technical]": row.dataset.applicationTechnical,
        "[data-application-drawer-status]": row.dataset.applicationStatus,
        "[data-application-drawer-cpu]": row.dataset.applicationCpu,
        "[data-application-drawer-memory]": row.dataset.applicationMemory,
        "[data-application-drawer-io]": row.dataset.applicationIo,
      };
      Object.entries(values).forEach(([selector, value]) => {
        const target = drawerHost.querySelector(selector);
        if (target) target.textContent = value || applicationWords().empty;
      });
      const status = drawerHost.querySelector("[data-application-drawer-status]");
      if (status) status.dataset.state = row.dataset.applicationRunning === "true" ? "running" : "stopped";
      const logsLink = drawerHost.querySelector("[data-application-drawer-logs]");
      if (logsLink) {
        logsLink.hidden = !row.dataset.applicationLogUrl;
        if (row.dataset.applicationLogUrl) logsLink.href = row.dataset.applicationLogUrl;
      }
    };

    const loadDrawerDetails = async (force = false) => {
      if (!opener || !drawerHost?.classList.contains("is-open")) return;
      const requestToken = ++drawerRequestToken;
      const refreshButtons = drawerHost.querySelectorAll("[data-application-detail-refresh]");
      refreshButtons.forEach(button => {
        button.disabled = true;
        button.setAttribute("aria-busy", "true");
      });
      setDrawerLoading();
      try {
        const payload = await loadDetails(opener, activeRange, force);
        if (requestToken === drawerRequestToken && drawerHost.classList.contains("is-open")) {
          renderDrawerDetails(payload, activeRange);
        }
      } catch (error) {
        if (requestToken === drawerRequestToken && drawerHost.classList.contains("is-open")) showDrawerError(error);
      } finally {
        if (requestToken === drawerRequestToken) {
          refreshButtons.forEach(button => {
            button.disabled = false;
            button.removeAttribute("aria-busy");
          });
        }
      }
    };

    const openDrawer = row => {
      if (!drawerHost || !drawer) return;
      opener = row;
      activeMode = row.dataset.applicationMode === "runtime" ? "runtime" : "pinned";
      activeRange = activeMode === "runtime" ? "15m" : "1h";
      drawerHost.dataset.applicationId = row.dataset.applicationId;
      drawerHost.dataset.applicationMode = activeMode;
      updateDrawerHeader(row);
      if (drawerNavigation) drawerNavigation.hidden = activeMode === "runtime";
      selectDetailTab(activeMode === "runtime" ? "runtime" : "history");
      root.querySelectorAll("[data-application-row]").forEach(candidate => candidate.classList.toggle("is-selected", candidate === row));
      drawerHost.setAttribute("aria-hidden", "false");
      drawerHost.classList.add("is-open");
      const scrollRegion = drawerHost.querySelector(".application-drawer__scroll");
      if (scrollRegion) scrollRegion.scrollTop = 0;
      document.body.style.overflow = "hidden";
      loadDrawerDetails(true);
      window.setTimeout(() => drawerHost.querySelector(".application-drawer__close")?.focus(), 180);
    };

    const closeDrawer = (restoreFocus = true) => {
      if (!drawerHost?.classList.contains("is-open")) return;
      drawerRequestToken += 1;
      drawerHost.classList.remove("is-open");
      drawerHost.setAttribute("aria-hidden", "true");
      document.body.style.overflow = "";
      root.querySelectorAll("[data-application-row]").forEach(candidate => candidate.classList.remove("is-selected"));
      if (restoreFocus && opener?.isConnected) opener.focus();
    };

    const onApplicationsClick = async event => {
      const copyButton = event.target.closest("[data-copy-runtime-command]");
      if (copyButton) {
        const command = copyButton.closest("dd")?.querySelector("[data-runtime-command]")?.textContent || "";
        const original = copyButton.innerHTML;
        try {
          await copyTextToClipboard(command.trim());
          copyButton.textContent = copyButton.dataset.copiedLabel;
          window.setTimeout(() => {
            copyButton.innerHTML = original;
          }, 1500);
        } catch {
          copyButton.focus();
        }
        return;
      }
      if (event.target.closest("[data-application-drawer-close]")) {
        closeDrawer();
        return;
      }
      if (event.target.closest("[data-application-detail-retry]")) {
        loadDrawerDetails(true);
        return;
      }
      if (event.target.closest("[data-application-detail-refresh]")) {
        loadDrawerDetails(true);
        return;
      }
      const range = event.target.closest("[data-application-range]");
      if (range) {
        activeRange = range.dataset.applicationRange;
        selectDetailTab("history");
        loadDrawerDetails(true);
        return;
      }
      const tab = event.target.closest("[data-application-detail-tab]");
      if (tab) {
        selectDetailTab(tab.dataset.applicationDetailTab);
        return;
      }
      const row = event.target.closest("[data-application-row]");
      if (row && !event.target.closest("a,button,input,select,textarea,form,summary,details")) openDrawer(row);
    };

    const onApplicationsKeydown = event => {
      if (event.key === "Escape" && drawerHost?.classList.contains("is-open")) {
        event.preventDefault();
        closeDrawer();
        return;
      }
      if (event.key === "Tab" && drawerHost?.classList.contains("is-open")) {
        const focusable = Array.from(drawer.querySelectorAll("button:not([disabled]),a[href],[tabindex]:not([tabindex='-1'])"))
          .filter(element => !element.hidden && element.getClientRects().length > 0);
        if (focusable.length) {
          const first = focusable[0];
          const last = focusable[focusable.length - 1];
          if (event.shiftKey && (event.target === first || !drawer.contains(event.target))) {
            event.preventDefault();
            last.focus();
          } else if (!event.shiftKey && event.target === last) {
            event.preventDefault();
            first.focus();
          }
        }
      }
      const row = event.target.closest("[data-application-row]");
      if (row && event.target === row && (event.key === "Enter" || event.key === " ")) {
        event.preventDefault();
        openDrawer(row);
      }
      const tab = event.target.closest("[data-application-detail-tab]");
      if (tab && (event.key === "ArrowLeft" || event.key === "ArrowRight")) {
        const tabs = Array.from(tab.parentElement.querySelectorAll("[data-application-detail-tab]"));
        const next = tabs[(tabs.indexOf(tab) + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length];
        next.focus();
        next.click();
      }
    };
    const onApplicationsChange = event => {
      const kindFilter = event.target.closest("[data-applications-kind-filter]");
      if (kindFilter?.checked) kindFilter.form?.requestSubmit();
    };
    root.addEventListener("click", onApplicationsClick);
    root.addEventListener("change", onApplicationsChange);
    root.addEventListener("keydown", onApplicationsKeydown);
    cleanups.push(() => {
      root.removeEventListener("click", onApplicationsClick);
      root.removeEventListener("change", onApplicationsChange);
      root.removeEventListener("keydown", onApplicationsKeydown);
      closeDrawer(false);
      pendingDetails.forEach(request => request.controller?.abort());
      pendingDetails.clear();
    });
  }

  function initRun(cleanups) {
    const root = document.querySelector("[data-run-events-url]");
    if (!root) return;
    const log = root.querySelector("[data-run-log]");
    const historySentinel = root.querySelector("[data-run-history-sentinel]");
    const jumpToTop = root.querySelector("[data-run-jump-top]");
    const jumpToBottom = root.querySelector("[data-run-jump-bottom]");
    const scrollLog = (event, top) => {
      event.preventDefault();
      if (!log) return;
      log.scrollTo({ top, behavior: "auto" });
      log.focus({ preventScroll: true });
    };
    const handleJumpToTop = event => {
      scrollLog(event, 0);
      loadHistory(false, true);
    };
    const handleJumpToBottom = event => scrollLog(event, log?.scrollHeight || 0);
    const showLatestLog = () => { if (log) log.scrollTop = log.scrollHeight; };
    jumpToTop?.addEventListener("click", handleJumpToTop);
    jumpToBottom?.addEventListener("click", handleJumpToBottom);
    cleanups.push(() => {
      jumpToTop?.removeEventListener("click", handleJumpToTop);
      jumpToBottom?.removeEventListener("click", handleJumpToBottom);
    });
    const state = root.querySelector("[data-run-live-state]");
    const pause = root.querySelector("[data-run-pause]");
    const pauseLabel = pause?.querySelector("[data-run-pause-label]");
    let paused = false;
    let completed = false;
    let buffer = [];
    let lastSequence = 0;
    let before = "";
    let hasMore = true;
    let historyLoading = false;
    let source = null;
    let disposed = false;
    const createEntry = payload => {
      const sequence = Number(payload.sequence);
      const span = document.createElement("span");
      if (Number.isSafeInteger(sequence) && sequence > 0) span.dataset.sequence = String(sequence);
      span.dataset.source = payload.source || "stdout";
      if (payload.encodingError) span.dataset.encodingError = "true";
      span.textContent = payload.text || "";
      return span;
    };
    const append = payload => {
      const sequence = Number(payload.sequence);
      if (Number.isSafeInteger(sequence) && sequence > 0) {
        if (sequence <= lastSequence) return;
        lastSequence = sequence;
      }
      log.append(createEntry(payload));
      showLatestLog();
    };
    const loadHistory = async (initial, pinToTop = false) => {
      if (!log || !historySentinel || historyLoading || (!initial && !hasMore)) return;
      historyLoading = true;
      log.setAttribute("aria-busy", "true");
      try {
        const url = new URL(root.dataset.runHistoryUrl, location.href);
        if (!initial && before) url.searchParams.set("before", before);
        const response = await fetch(url, { credentials: "same-origin", cache: "no-store", headers: { Accept: "application/json" } });
        if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
        const page = await response.json();
        const entries = Array.isArray(page.events) ? page.events : [];
        before = page.before ? String(page.before) : "";
        hasMore = Boolean(page.hasMore);
        const fragment = document.createDocumentFragment();
        entries.forEach(entry => fragment.append(createEntry(entry)));
        if (initial) {
          historySentinel.after(fragment);
          if (entries.length) lastSequence = Number(entries[entries.length - 1].sequence) || 0;
          showLatestLog();
        } else if (entries.length) {
          const previousHeight = log.scrollHeight;
          historySentinel.after(fragment);
          if (pinToTop) log.scrollTop = 0;
          else log.scrollTop += log.scrollHeight - previousHeight;
        }
      } finally {
        historyLoading = false;
        log.removeAttribute("aria-busy");
      }
    };
    const finishPauseControl = () => {
      pause?.remove();
      root._toggleLogPause = null;
    };
    const connect = () => {
      if (disposed || !window.EventSource) return;
      const eventsURL = new URL(root.dataset.runEventsUrl, location.href);
      eventsURL.searchParams.set("after", String(lastSequence));
      source = new EventSource(eventsURL);
      source.addEventListener("open", () => { if (state) state.textContent = words().connected; });
      source.addEventListener("output", event => {
        const payload = { ...JSON.parse(event.data), sequence: Number(event.lastEventId) };
        if (paused) buffer.push(payload); else append(payload);
      });
      source.addEventListener("complete", event => {
        completed = true;
        source?.close();
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
      source.addEventListener("error", () => { if (state && source?.readyState !== EventSource.CLOSED) state.textContent = words().disconnected; });
    };
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
    const onScroll = event => { if (event.isTrusted && log.scrollTop < 48) loadHistory(false); };
    log?.addEventListener("scroll", onScroll, { passive: true });
    root._toggleLogPause = toggle;
    loadHistory(true).then(connect).catch(() => {
      if (state) state.textContent = words().disconnected;
      log?.removeAttribute("aria-busy");
    });
    cleanups.push(() => {
      disposed = true;
      source?.close();
      log?.removeEventListener("scroll", onScroll);
      pause?.removeEventListener("click", toggle);
      if (root._toggleLogPause === toggle) root._toggleLogPause = null;
    });
  }

  function initStatus() {
    const attention = document.querySelector("[data-shell-attention]");
    if (!attention) return;
    const empty = attention.querySelector("[data-shell-attention-empty]");
    const host = attention.querySelector('[data-shell-attention-item="host"]');
    const runs = attention.querySelector('[data-shell-attention-item="runs"]');
    const websites = attention.querySelector('[data-shell-attention-item="websites"]');
    const applications = attention.querySelector('[data-shell-attention-item="applications"]');
    const issueSummary = attention.querySelector("[data-shell-issue-summary]");
    const issueCount = issueSummary?.querySelector("[data-shell-issue-count]");
    const issueIcon = issueSummary?.querySelector("[data-shell-issue-icon]");
    const setVisible = (item, visible) => {
      if (item) item.hidden = !visible;
      return visible;
    };
    const updateWebsiteStatus = data => {
      const down = Math.max(0, Number(data.websiteDown) || 0);
      const verifying = Math.max(0, Number(data.websiteVerifying) || 0);
      if (!websites) return down > 0 || verifying > 0;
      websites.dataset.state = data.websiteState;
      websites.dataset.down = String(down);
      websites.dataset.verifying = String(verifying);
      const primary = websites.querySelector("[data-shell-website-primary]");
      const secondary = websites.querySelector("[data-shell-website-secondary]");
      if (down > 0) {
        if (primary) primary.textContent = `${down} ${down === 1 ? words().websiteDownOne : words().websiteDownMany}`;
        if (secondary) secondary.textContent = `${verifying} ${verifying === 1 ? words().websiteVerifyingOne : words().websiteVerifyingMany}`;
      } else if (verifying > 0) {
        if (primary) primary.textContent = `${verifying} ${verifying === 1 ? words().websiteVerifyingOne : words().websiteVerifyingMany}`;
        if (secondary) secondary.textContent = words().websiteNoConfirmedFailure;
      }
      return setVisible(websites, down > 0 || verifying > 0);
    };
    const update = async () => {
      try {
        const response = await fetch("/monitor/status", { credentials: "same-origin", cache: "no-store" });
        if (!response.ok) return;
        const data = await response.json();
        const currentIssueCount = Math.max(0, Number(data.issueCount) || 0)
          + Math.max(0, Number(data.websiteDown) || 0)
          + Math.max(0, Number(data.stoppedPinnedApplications) || 0)
          + Math.max(0, Number(data.applicationIssueCount) || 0);
        if (issueSummary) {
          const state = currentIssueCount > 0 ? "attention" : "current";
          const label = `${attention.dataset.currentErrorsLabel || "Current errors"}: ${currentIssueCount}`;
          issueSummary.dataset.state = state;
          issueSummary.setAttribute("aria-label", label);
          issueSummary.title = label;
          if (issueCount) {
            issueCount.textContent = String(currentIssueCount);
            issueCount.hidden = currentIssueCount === 0;
          }
          if (issueIcon?.dataset.lucide !== (currentIssueCount > 0 ? "triangle-alert" : "circle-check")) {
            replaceIconHost(issueIcon, currentIssueCount > 0 ? "triangle-alert" : "circle-check");
          }
        }
        let visibleCount = 0;
        if (host) {
          const hostVisible = data.state !== "current";
          host.dataset.state = data.state;
          const strong = host.querySelector("[data-shell-host-primary]");
          const small = host.querySelector("[data-shell-host-secondary]");
          if (strong) strong.textContent = words()[data.state] || data.state;
          if (small) small.textContent = attention.dataset.environment || small.textContent;
          if (setVisible(host, hostVisible)) visibleCount += 1;
        }
        const activeRuns = Math.max(0, Number(data.activeRuns) || 0);
        if (runs) {
          const primary = runs.querySelector("[data-shell-runs-primary]");
          if (primary) primary.textContent = `${activeRuns} ${attention.dataset.activeRunsLabel || words().activeRuns}`;
          if (setVisible(runs, activeRuns > 0)) visibleCount += 1;
        }
        if (updateWebsiteStatus(data)) visibleCount += 1;
        if (applications) {
          const stopped = Math.max(0, Number(data.stoppedPinnedApplications) || 0);
          const issues = Math.max(0, Number(data.applicationIssueCount) || 0);
          const primary = applications.querySelector("[data-shell-applications-primary]");
          const secondary = applications.querySelector("[data-shell-applications-secondary]");
          applications.dataset.state = issues > 0 ? "attention" : "stale";
          if (primary) {
            primary.textContent = stopped > 0
              ? `${attention.dataset.stoppedPinnedLabel || "Stopped pinned applications"} ${stopped}`
              : attention.dataset.applicationAttentionLabel || "Application observation needs attention";
          }
          if (secondary) {
            secondary.textContent = issues > 0
              ? `${attention.dataset.applicationIssuesLabel || "Application collection issues"} ${issues}`
              : attention.dataset.reviewApplicationsLabel || "Review pinned applications";
          }
          if (setVisible(applications, stopped > 0 || issues > 0)) visibleCount += 1;
        }
        if (empty) empty.hidden = visibleCount > 0;
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

    root.querySelectorAll("[data-copy-value]:not([data-copy-text])").forEach(copyButton => {
      const copyIcon = copyButton.querySelector("[data-copy-icon]");
      const label = copyButton.querySelector("[data-copy-value-label]");
      const status = copyButton.parentElement?.querySelector("[data-copy-value-status]");
      if (!copyIcon) return;

      copyButton.hidden = false;
      const reset = () => {
        copyButton.removeAttribute("data-state");
        if (label) label.textContent = copyButton.dataset.copyLabel;
        else {
          copyButton.setAttribute("aria-label", copyButton.dataset.copyLabel);
          copyButton.dataset.tooltip = copyButton.dataset.copyTooltip || copyButton.dataset.copyLabel;
        }
        setControlIcon(copyIcon, "copy");
        if (status) status.textContent = "";
        feedbackTimers.delete(copyButton);
      };
      const onCopyValue = async () => {
        if (copyButton.dataset.copying === "true") return;
        const existingTimer = feedbackTimers.get(copyButton);
        if (existingTimer) window.clearTimeout(existingTimer);
        copyButton.dataset.copying = "true";
        copyButton.setAttribute("aria-busy", "true");

        try {
          await copyTextToClipboard(copyButton.dataset.copyValue || "");
          copyButton.dataset.state = "success";
          if (label) label.textContent = copyButton.dataset.copiedLabel;
          else {
            copyButton.setAttribute("aria-label", copyButton.dataset.copiedLabel);
            copyButton.dataset.tooltip = copyButton.dataset.copiedLabel;
          }
          setControlIcon(copyIcon, "check");
          if (status) status.textContent = copyButton.dataset.copiedLabel;
        } catch {
          copyButton.dataset.state = "error";
          if (label) label.textContent = copyButton.dataset.copyFailedLabel;
          else {
            copyButton.setAttribute("aria-label", copyButton.dataset.copyFailedLabel);
            copyButton.dataset.tooltip = copyButton.dataset.copyFailedLabel;
          }
          setControlIcon(copyIcon, "triangle-alert");
          if (status) status.textContent = copyButton.dataset.copyFailedLabel;
        } finally {
          copyButton.removeAttribute("aria-busy");
          delete copyButton.dataset.copying;
        }

        const timer = window.setTimeout(reset, 1600);
        feedbackTimers.set(copyButton, timer);
      };
      copyButton.addEventListener("click", onCopyValue);
      cleanups.push(() => copyButton.removeEventListener("click", onCopyValue));
    });

    root.querySelectorAll("[data-one-time-secret]").forEach(secret => {
      const scrub = () => secret.replaceChildren();
      window.addEventListener("pagehide", scrub, { once: true });
      cleanups.push(() => window.removeEventListener("pagehide", scrub));
    });

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
          await copyTextToClipboard(content.textContent);
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
        }, 2400);
        feedbackTimers.set(copyButton, timer);
      };
      copyButton.addEventListener("click", onCopy);
      cleanups.push(() => copyButton.removeEventListener("click", onCopy));
    });

    cleanups.push(() => feedbackTimers.forEach(timer => window.clearTimeout(timer)));
  }

  function initFileDropUpload(root = document, cleanups = []) {
    root.querySelectorAll("[data-file-drop-form]").forEach(form => {
      const zone = document.getElementById(form.dataset.fileDropSurface || "");
      const input = form.querySelector(".file-drop-input");
      const title = zone?.querySelector("[data-file-drop-title]");
      const status = zone?.querySelector("[data-file-drop-status]");
      if (!zone || !input || !title || !status) return;

      let dragDepth = 0;
      const isFileDrag = dataTransfer => Array.from(dataTransfer?.types || []).includes("Files");
      const setState = (state, nextTitle, nextDescription) => {
        if (state) zone.dataset.state = state;
        else zone.removeAttribute("data-state");
        title.textContent = nextTitle;
        status.textContent = nextDescription;
      };
      const resetState = () => setState("", "", "");
      const showError = message => setState("error", form.dataset.errorTitle, message);
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
      const onCancelled = () => {
        input.value = "";
        resetState();
      };
      const onFailed = event => showError(event.detail?.message || form.dataset.inputError);
      const preventFileNavigation = event => {
        if (!isFileDrag(event.dataTransfer) || zone.contains(event.target)) return;
        event.preventDefault();
      };

      zone.addEventListener("dragenter", onDragEnter);
      zone.addEventListener("dragover", onDragOver);
      zone.addEventListener("dragleave", onDragLeave);
      zone.addEventListener("drop", onDrop);
      input.addEventListener("change", onChange);
      form.addEventListener("file-upload-cancelled", onCancelled);
      form.addEventListener("file-upload-failed", onFailed);
      document.addEventListener("dragover", preventFileNavigation);
      document.addEventListener("drop", preventFileNavigation);
      cleanups.push(() => {
        zone.removeEventListener("dragenter", onDragEnter);
        zone.removeEventListener("dragover", onDragOver);
        zone.removeEventListener("dragleave", onDragLeave);
        zone.removeEventListener("drop", onDrop);
        input.removeEventListener("change", onChange);
        form.removeEventListener("file-upload-cancelled", onCancelled);
        form.removeEventListener("file-upload-failed", onFailed);
        document.removeEventListener("dragover", preventFileNavigation);
        document.removeEventListener("drop", preventFileNavigation);
      });
    });
  }

  function initFileVisibilityToggle(root = document, cleanups = []) {
    root.querySelectorAll("[data-file-hidden-toggle]").forEach(toggle => {
      const form = toggle.closest("form");
      if (!form) return;
      const onChange = () => {
        if (form.getAttribute("aria-busy") === "true") return;
        form.dataset.focusAfterNavigation = "[data-file-hidden-toggle]";
        form.requestSubmit(form.querySelector("[data-search-submit]"));
      };
      toggle.addEventListener("change", onChange);
      cleanups.push(() => toggle.removeEventListener("change", onChange));
    });
  }

  function initFileQuickAccess(root = document, cleanups = [], initiallyOpen = false) {
    const disclosure = root.querySelector("[data-file-quick-access]");
    if (!disclosure) return;
    disclosure.open = initiallyOpen;
    const list = disclosure.querySelector("[data-file-quick-list]");
    const empty = disclosure.querySelector("[data-file-quick-empty]");
    const count = disclosure.querySelector("[data-file-quick-count]");
    const countLabel = disclosure.querySelector("[data-file-quick-count-label]");
    const oneLabel = disclosure.querySelector("[data-file-quick-one-label]");
    const manyLabel = disclosure.querySelector("[data-file-quick-many-label]");
	const status = disclosure.querySelector("[data-file-quick-status]");
    if (!list || !empty || !count || !countLabel || !oneLabel || !manyLabel || !status) return;
	const validationController = new AbortController();
	cleanups.push(() => validationController.abort());

    const storageKey = "scriptboard.files.pinnedDirectories.v2";
    const isValidPin = pin => {
      if (!pin || typeof pin.path !== "string" || pin.path.length === 0 ||
        typeof pin.label !== "string" || pin.label.length === 0 ||
        typeof pin.href !== "string" || !pin.href.startsWith("/")) return false;
      try {
        const target = new URL(pin.href, location.origin);
        return target.origin === location.origin && target.pathname === "/resources/files" && target.searchParams.get("path") === pin.path;
      } catch {
        return false;
      }
    };
	const normalizePins = value => {
	  if (!Array.isArray(value)) return [];
	  const paths = new Set();
	  return value.filter(isValidPin).filter(pin => {
		if (paths.has(pin.path)) return false;
		paths.add(pin.path);
		return true;
	  }).slice(0, 30);
	};
	let legacyPins = [];
	try { legacyPins = normalizePins(JSON.parse(localStorage.getItem(storageKey) || "[]")); } catch { /* ignore invalid legacy storage */ }
	let pins = [];
	const showSaveError = () => {
	  status.textContent = disclosure.dataset.saveFailed;
	  status.hidden = false;
	};
	const clearSaveError = () => {
	  status.textContent = "";
	  status.hidden = true;
	};
	const savePin = async (pin, pinned) => {
	  const response = await fetch(disclosure.dataset.pinsUrl, {
		method: "POST",
		credentials: "same-origin",
		headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
		body: new URLSearchParams({ csrf_token: disclosure.dataset.csrfToken, path: pin.path, pinned: String(pinned) }),
		signal: validationController.signal,
	  });
	  if (!response.ok) throw new Error(`Unable to save Quick access (${response.status})`);
	  const payload = await response.json();
	  pins = normalizePins(payload?.pins);
	  clearSaveError();
    };
    const pinControls = Array.from(root.querySelectorAll("[data-file-pin]"));
    const renderControl = control => {
      const pinned = pins.some(pin => pin.path === control.dataset.filePinPath);
      const label = pinned ? control.dataset.unpinLabel : control.dataset.pinLabel;
      control.hidden = false;
      control.setAttribute("aria-pressed", String(pinned));
      control.setAttribute("aria-label", label);
      control.dataset.tooltip = label;
      setControlIcon(control.querySelector("span"), pinned ? "pin-off" : "pin");
    };
    const render = () => {
      count.textContent = String(pins.length);
      countLabel.textContent = pins.length === 1 ? oneLabel.textContent : manyLabel.textContent;
      empty.hidden = pins.length > 0;
      list.hidden = pins.length === 0;
      list.replaceChildren();
      pins.forEach(pin => {
        const item = document.createElement("li");
        item.className = "file-quick-row";

        const link = document.createElement("a");
		link.setAttribute("aria-disabled", "true");
        const icon = document.createElement("span");
        icon.className = "file-quick-row__icon";
        icon.append(makeIcon("folder"));
        const copy = document.createElement("span");
        const label = document.createElement("strong");
        label.textContent = pin.label;
        const path = document.createElement("small");
        path.textContent = pin.path;
        copy.append(label, path);
        link.append(icon, copy);
		link.addEventListener("click", () => {
		  if (link.hasAttribute("href")) disclosure.open = false;
		});
		const validationURL = new URL(disclosure.dataset.validationUrl || "/resources/files/validate", location.origin);
		validationURL.searchParams.set("path", pin.path);
		fetch(validationURL, { headers: { Accept: "application/json" }, signal: validationController.signal })
		  .then(response => response.ok ? response.json() : { accessible: false })
		  .then(result => {
			if (!result?.accessible || !pins.some(candidate => candidate.path === pin.path)) return;
			link.setAttribute("href", pin.href);
			link.removeAttribute("aria-disabled");
		  })
		  .catch(error => { if (error?.name !== "AbortError") link.dataset.unavailable = "true"; });

        const remove = document.createElement("button");
        remove.className = "icon-button";
        remove.type = "button";
        remove.setAttribute("aria-label", `${disclosure.dataset.removeLabel}: ${pin.label}`);
        remove.dataset.tooltip = disclosure.dataset.removeLabel;
        remove.append(makeIcon("pin-off"));
		remove.addEventListener("click", async () => {
		  remove.disabled = true;
		  try {
			await savePin(pin, false);
			render();
		  } catch (error) {
			if (error?.name !== "AbortError") showSaveError();
		  } finally {
			remove.disabled = false;
		  }
        });
        item.append(link, remove);
        list.append(item);
      });
      pinControls.forEach(renderControl);
    };

    pinControls.forEach(control => {
	  const onToggle = async () => {
        const path = control.dataset.filePinPath;
		const pin = { path, label: control.dataset.filePinLabel, href: control.dataset.filePinHref };
		if (!isValidPin(pin)) return;
		control.disabled = true;
		try {
		  await savePin(pin, !pins.some(candidate => candidate.path === path));
		  render();
		} catch (error) {
		  if (error?.name !== "AbortError") showSaveError();
		} finally {
		  control.disabled = false;
		}
      };
      control.addEventListener("click", onToggle);
      cleanups.push(() => control.removeEventListener("click", onToggle));
    });

	const loadPins = async () => {
	  try {
		const response = await fetch(disclosure.dataset.pinsUrl, {
		  credentials: "same-origin", headers: { Accept: "application/json" }, signal: validationController.signal,
		});
		if (!response.ok) throw new Error(`Unable to read Quick access (${response.status})`);
		pins = normalizePins((await response.json())?.pins);
		let migrated = true;
		for (const pin of legacyPins) {
		  if (pins.some(candidate => candidate.path === pin.path)) continue;
		  try { await savePin(pin, true); } catch (error) {
			if (error?.name === "AbortError") return;
			migrated = false;
		  }
		}
		if (migrated) {
		  try { localStorage.removeItem(storageKey); } catch { /* migration is already durable on the server */ }
		} else {
		  showSaveError();
		}
	  } catch (error) {
		if (error?.name === "AbortError") return;
		pins = legacyPins;
		showSaveError();
	  }
	  disclosure.hidden = false;
	  render();
	};
	loadPins();
  }

  function initFileOperation(cleanups) {
    const root = document.querySelector("[data-file-operation]");
    if (!root || !root.dataset.eventsUrl) return;
    const phase = root.querySelector("[data-file-operation-phase]");
    const state = root.querySelector("[data-file-operation-state]");
    const error = root.querySelector("[data-file-operation-error]");
    const progress = root.querySelector("[data-file-operation-progress]");
    const bytes = root.querySelector("[data-file-operation-bytes]");
    const formatBytes = value => {
      let amount = Number(value) || 0;
      const units = ["B", "KiB", "MiB", "GiB", "TiB"];
      let unit = 0;
      while (amount >= 1024 && unit < units.length - 1) { amount /= 1024; unit += 1; }
      return `${unit === 0 ? Math.round(amount) : amount.toFixed(1)} ${units[unit]}`;
    };
    const terminal = new Set(["completed", "rolled_back", "cancelled", "failed"]);
    const stream = new EventSource(root.dataset.eventsUrl);
    stream.addEventListener("progress", event => {
      let operation;
      try { operation = JSON.parse(event.data); } catch { return; }
      if (phase) phase.textContent = operation.phase || "";
      if (state) state.textContent = operation.phase || "";
      if (error) error.textContent = operation.error || "";
      if (progress) {
        progress.max = Math.max(1, Number(operation.bytesTotal) || 0);
        progress.value = Math.max(0, Number(operation.bytesCompleted) || 0);
      }
      if (bytes) bytes.textContent = `${formatBytes(operation.bytesCompleted)} / ${formatBytes(operation.bytesTotal)}`;
      if (terminal.has(operation.phase)) stream.close();
    });
    cleanups.push(() => stream.close());
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
      if(urlHint)urlHint.textContent=isWebSocket?(form.dataset.websocketAddressHint||'Use ws:// or wss://'):(form.dataset.httpAddressHint||'Use http:// or https://');
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
        const icon=document.createElement('span');
        icon.dataset.lucide='check';
        icon.setAttribute('aria-hidden','true');
        const copy=document.createElement('div');
        const title=document.createElement('h2');
        title.textContent=root.dataset.successTitle||'Websites added';
        const description=document.createElement('p');
        description.textContent=`${imported} ${root.dataset.successDescription||''}`;
        copy.append(title,description);
        const ledger=document.createElement('a');
        ledger.className='button button--primary';
        ledger.href=safeInternalURL(payload?.redirectURL,'/monitor/websites');
        ledger.textContent=root.dataset.viewLedger||'View website ledger';
        success.append(icon,copy,ledger);
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

  function initWebsiteConfigTransfer(root){
    const form=root.querySelector('[data-website-selection-form]');
    if(!form)return ()=>{};
    const selectAll=form.querySelector('[data-website-select-all]');
    const candidates=[...form.querySelectorAll('[data-website-selection]:not(:disabled)')];
    const count=form.querySelector('[data-website-selected-count]');
    const button=form.querySelector('[data-website-selection-submit]');
    const buttonCount=form.querySelector('[data-website-submit-count]');
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
      if(event.target.matches('[data-website-selection]'))sync();
    };
    selectAll?.addEventListener('change',onSelectAll);
    form.addEventListener('change',onChange);
    sync();
    return ()=>{
      selectAll?.removeEventListener('change',onSelectAll);
      form.removeEventListener('change',onChange);
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
    let refreshStatus=root.querySelector('[data-website-refresh-status]');
    const refreshControl=root.querySelector('[data-website-refresh]');
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
    const backgroundSurfaceBlocked=()=>!surfaceTaskURL&&Boolean(taskPanelState);
    const reloadCurrentSurface=async(preferredFocusKey='')=>{
      if(stopped||!root.isConnected||backgroundSurfaceBlocked())return;
      if(surfaceTaskURL){
        if(taskPanelState?.taskURL===surfaceTaskURL&&taskPanelState.host.contains(root)){
          const nextRoot=await refreshTaskPanelMain(surfaceTaskURL,'website-detail',preferredFocusKey);
          const nextStatus=nextRoot?.querySelector('[data-website-refresh-status]');
          if(nextStatus)nextStatus.textContent=nextRoot.dataset.refreshedLabel||'';
        }
        return;
      }
      const result=await fetchDocument(location.href,{cache:'no-store'});
      const nextRoot=result.document?.querySelector('[data-website-monitoring]');
      const nextData=nextRoot?.querySelector('[data-website-data]');
      const currentData=root.querySelector('[data-website-data]');
      if(!result.response.ok||!nextRoot||!nextData||!currentData)throw new Error(root.dataset.refreshFailed||'');
      currentData.replaceWith(document.importNode(nextData,true));
      root.dataset.countsToken=nextRoot.dataset.countsToken||'';
      refreshStatus=root.querySelector('[data-website-refresh-status]');
      renderIcons(root);
      localizeTimes(root);
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
        await reloadCurrentSurface('check');
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
    const refresh=async(manual=false)=>{
      if(stopped||busy||document.hidden||backgroundSurfaceBlocked())return;
      busy=true;
      if(manual){
        refreshControl?.setAttribute('aria-busy','true');
        refreshControl?.setAttribute('aria-disabled','true');
      }
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
      if(manual){
        refreshControl?.removeAttribute('aria-busy');
        refreshControl?.removeAttribute('aria-disabled');
      }
    };
    const onManualRefresh=event=>{
      event.preventDefault();
      refresh(true);
    };
    refreshControl?.addEventListener('click',onManualRefresh);
    const timer=setInterval(refresh,10000);
    const visibility=()=>{if(!document.hidden)refresh()};
    document.addEventListener('visibilitychange',visibility);
    return ()=>{
      stopped=true;
      checkController?.abort();
      clearInterval(timer);
      document.removeEventListener('visibilitychange',visibility);
      refreshControl?.removeEventListener('click',onManualRefresh);
      root.removeEventListener('click',onLedgerClick);
      root.removeEventListener('keydown',onLedgerKey);
      checkForm?.removeEventListener('submit',onCheck);
    };
  }

  function initDisplaySettings(cleanups) {
    const root = document.querySelector("[data-display-settings]");
    if (!root) return;
    const options = Array.from(root.querySelectorAll('input[name="website_fault_color"]'));
    const selected = readWebsiteFaultColor();
    options.forEach(option => {
      option.checked = option.value === selected;
      const onChange = () => {
        if (!option.checked) return;
        applyWebsiteFaultColor(option.value);
        try {
          localStorage.setItem(websiteFaultColorKey, option.value);
        } catch {
          // The visual preference remains active for this page when storage is unavailable.
        }
      };
      option.addEventListener("change", onChange);
      cleanups.push(() => option.removeEventListener("change", onChange));
    });
  }

  function initLiveLog(cleanups) {
    const root = document.querySelector("[data-live-log-viewer]");
    if (!root) return;
    const output = root.querySelector("[data-log-output]");
    const historySentinel = root.querySelector("[data-log-history-sentinel]");
    const historyLabel = root.querySelector("[data-log-history-label]");
    const state = root.querySelector("[data-log-state]");
    const stateLabel = root.querySelector("[data-log-state-label]");
    const pause = root.querySelector("[data-log-pause]");
    const pauseLabel = root.querySelector("[data-log-pause-label]");
    const autoFollow = root.querySelector("[data-log-autofollow]");
    const copyButton = root.querySelector("[data-log-copy]");
    const copyLabel = root.querySelector("[data-log-copy-label]");
    const clearButton = root.querySelector("[data-log-clear]");
    const newLinesButton = root.querySelector("[data-log-new-lines]");
    const newLinesLabel = root.querySelector("[data-log-new-lines-label]");
    if (!output || !historySentinel) return;

    const MAX_LOG_ENTRIES = 20000;
    const MAX_LOG_BYTES = 16 * 1024 * 1024;
    const textEncoder = new TextEncoder();
    let before = "";
    let hasMore = true;
    let historyLoading = false;
    let initialLoaded = false;
    let eventSource = null;
    let lastCursor = "";
    let loadedBytes = 0;
    let paused = false;
    let complete = false;
    let shouldFollow = true;
    let unseenLines = 0;
    let disposed = false;

    const setState = (name, label = "") => {
      if (state) state.dataset.state = name;
      if (stateLabel) {
        const fallback = root.dataset[`logLabel${name.charAt(0).toUpperCase()}${name.slice(1)}`];
        stateLabel.textContent = label || fallback || name;
      }
    };
    const localizedState = name => {
      if (!name) return "";
      return root.dataset[`logLabel${name.charAt(0).toUpperCase()}${name.slice(1)}`] || "";
    };

    const scrollToLatest = behavior => {
      output.scrollTo({ top: output.scrollHeight, behavior });
    };

    const updateNewLines = () => {
      if (!newLinesButton || !newLinesLabel) return;
      newLinesButton.hidden = unseenLines === 0 || shouldFollow;
      newLinesLabel.textContent = unseenLines
        ? `${unseenLines} ${root.dataset.logLabelNewLines || "new lines"}`
        : "";
    };

    const setAutoFollow = enabled => {
      shouldFollow = enabled;
      autoFollow?.setAttribute("aria-pressed", String(enabled));
      root.classList.toggle("is-following", enabled);
      if (enabled) {
        unseenLines = 0;
        updateNewLines();
        scrollToLatest("smooth");
      }
    };

    const entryBytes = entry => textEncoder.encode(entry.text || "").byteLength;

    const createEntryRow = entry => {
      const row = document.createElement("div");
      row.className = "live-log-entry";
      row.dataset.severity = entry.severity || "normal";
      row.dataset.source = entry.source || "combined";
      row.dataset.cursor = entry.cursor || "";
      row.dataset.logText = entry.text || "";
      row.dataset.logBytes = String(entryBytes(entry));
      if (entry.continuation) row.dataset.continuation = "true";
      if (entry.encodingError) row.dataset.encodingError = "true";

      const time = document.createElement("time");
      time.className = "live-log-entry__time";
      if (entry.time) {
        const timestamp = new Date(entry.time);
        if (!Number.isNaN(timestamp.valueOf())) {
          time.dateTime = timestamp.toISOString();
          time.textContent = new Intl.DateTimeFormat(locale(), {
            hour: "2-digit", minute: "2-digit", second: "2-digit",
            fractionalSecondDigits: 3, hour12: false
          }).format(timestamp);
        }
      }
      if (!time.textContent) time.textContent = "—";

      const level = document.createElement("strong");
      level.className = "live-log-entry__level";
      level.textContent = row.dataset.severity === "error"
        ? "ERROR"
        : row.dataset.severity === "warning" ? "WARN" : "INFO";

      const source = document.createElement("span");
      source.className = "live-log-entry__source";
      source.textContent = String(entry.source || "combined").toUpperCase();

      const message = document.createElement("span");
      message.className = "live-log-entry__message";
      message.textContent = entry.text || "";
      row.append(time, level, source, message);
      return row;
    };

    const trimEntries = () => {
      let rows = output.querySelectorAll(".live-log-entry");
      let trimmed = false;
      while (rows.length > MAX_LOG_ENTRIES || loadedBytes > MAX_LOG_BYTES) {
        const row = rows[0];
        if (!row) break;
        loadedBytes -= Number(row.dataset.logBytes) || 0;
        row.remove();
        trimmed = true;
        rows = output.querySelectorAll(".live-log-entry");
      }
      if (trimmed) {
        const oldestRetained = rows[0];
        if (oldestRetained?.dataset.cursor) before = oldestRetained.dataset.cursor;
        hasMore = true;
      }
    };

    const appendEntries = (entries, older = false) => {
      if (!Array.isArray(entries) || entries.length === 0) return;
      const fragment = document.createDocumentFragment();
      entries.forEach(entry => {
        const row = createEntryRow(entry);
        loadedBytes += Number(row.dataset.logBytes) || 0;
        fragment.append(row);
        if (!older && entry.cursor) lastCursor = entry.cursor;
      });
      if (older) {
        const previousHeight = output.scrollHeight;
        output.insertBefore(fragment, historySentinel.nextSibling);
        output.scrollTop += output.scrollHeight - previousHeight;
      } else {
        output.append(fragment);
        if (shouldFollow) {
          unseenLines = 0;
          scrollToLatest(initialLoaded ? "smooth" : "auto");
        } else {
          unseenLines += entries.length;
        }
        updateNewLines();
      }
      trimEntries();
    };

    const addNotice = (tone, message) => {
      if (!message) return;
      const notice = document.createElement("div");
      notice.className = "live-log-notice";
      notice.dataset.tone = tone;
      notice.setAttribute("role", tone === "error" ? "alert" : "status");
      notice.textContent = message;
      output.append(notice);
      if (shouldFollow) scrollToLatest("smooth");
    };

    const loadHistory = async (initial = false) => {
      if (historyLoading || (!initial && !hasMore)) return;
      historyLoading = true;
      historySentinel.dataset.state = "loading";
      try {
        const url = new URL(root.dataset.logHistoryUrl, location.href);
        if (!initial && before) url.searchParams.set("before", before);
        const response = await fetch(url, {
          credentials: "same-origin", cache: "no-store",
          headers: { Accept: "application/json" }
        });
        if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
        const page = await response.json();
        if (page.sourceVersion && root.dataset.logSourceVersion &&
            page.sourceVersion !== root.dataset.logSourceVersion) {
          addNotice("gap", localizedState("gap") || "Log continuity could not be verified.");
        }
        before = page.before || "";
        hasMore = Boolean(page.hasMore);
        appendEntries(page.entries || [], !initial);
        if (initial && page.entries?.length) {
          lastCursor = page.entries[page.entries.length - 1].cursor || "";
        }
        historySentinel.dataset.state = hasMore ? "ready" : "complete";
        if (historyLabel) {
          historyLabel.textContent = hasMore
            ? root.dataset.logLabelLoadingHistory || "Load earlier output"
            : root.dataset.logLabelHistoryEnd || "Beginning of available output";
        }
      } catch (error) {
        historySentinel.dataset.state = "error";
        if (historyLabel) historyLabel.textContent = error.message;
        setState("error", error.message);
      } finally {
        historyLoading = false;
      }
    };

    const parseEvent = event => {
      try {
        return JSON.parse(event.data);
      } catch {
        return null;
      }
    };

    const closeEvents = () => {
      eventSource?.close();
      eventSource = null;
    };

    const connect = () => {
      if (disposed || paused || complete || !window.EventSource) return;
      closeEvents();
      const url = new URL(root.dataset.logEventsUrl, location.href);
      if (lastCursor) url.searchParams.set("after", lastCursor);
      setState("connecting");
      eventSource = new EventSource(url.href);
      eventSource.addEventListener("entry", event => {
        const payload = parseEvent(event);
        if (!payload?.entry) return;
        appendEntries([payload.entry]);
      });
      eventSource.addEventListener("state", event => {
        const payload = parseEvent(event);
        if (!payload) return;
        if (payload.state === "live") {
          setState("live");
          return;
        }
        const connectionState = payload.state === "error" ? "error" : "reconnecting";
        const message = payload.state === "error"
          ? payload.message || localizedState("error")
          : localizedState(payload.state) || payload.state;
        setState(connectionState, message);
        addNotice(payload.state === "error" ? "error" : "state", message);
      });
      eventSource.addEventListener("gap", () => {
        const message = localizedState("gap") || "Log continuity could not be verified.";
        setState("reconnecting", message);
        addNotice("gap", message);
      });
      eventSource.addEventListener("complete", () => {
        complete = true;
        closeEvents();
        const message = localizedState("complete") || "Complete";
        setState("complete", message);
        addNotice("state", message);
      });
      eventSource.addEventListener("error", () => {
        if (!paused && !complete) setState("reconnecting");
      });
    };

    const onScroll = () => {
      const distanceFromBottom = output.scrollHeight - output.scrollTop - output.clientHeight;
      if (distanceFromBottom > 64 && shouldFollow) setAutoFollow(false);
      if (output.scrollTop < 48 && hasMore) loadHistory(false);
    };
    const onPause = () => {
      paused = !paused;
      if (paused) {
        closeEvents();
        setState("paused");
      } else {
        complete = false;
        connect();
      }
      if (pauseLabel) pauseLabel.textContent = paused
        ? root.dataset.logLabelResume || "Resume"
        : root.dataset.logLabelPause || "Pause";
      pause?.setAttribute("aria-pressed", String(paused));
    };
    const onAutoFollow = () => setAutoFollow(!shouldFollow);
    const onNewLines = () => setAutoFollow(true);
    const onClear = () => {
      output.querySelectorAll(".live-log-entry,.live-log-notice").forEach(row => row.remove());
      loadedBytes = 0;
      unseenLines = 0;
      updateNewLines();
    };
    const onCopy = async () => {
      const text = Array.from(output.querySelectorAll(".live-log-entry"))
        .map(row => row.dataset.logText || "")
        .join("\n");
      try {
        await copyTextToClipboard(text);
        if (copyLabel) copyLabel.textContent = root.dataset.logLabelCopied || "Copied";
        window.setTimeout(() => {
          if (copyLabel) copyLabel.textContent = root.dataset.logLabelCopy || "Copy";
        }, 1500);
      } catch {
        copyButton?.focus();
      }
    };

    output.addEventListener("scroll", onScroll, { passive: true });
    pause?.addEventListener("click", onPause);
    autoFollow?.addEventListener("click", onAutoFollow);
    newLinesButton?.addEventListener("click", onNewLines);
    clearButton?.addEventListener("click", onClear);
    copyButton?.addEventListener("click", onCopy);
    setAutoFollow(true);

    loadHistory(true).finally(() => {
      if (disposed) return;
      initialLoaded = true;
      scrollToLatest("auto");
      connect();
    });

    cleanups.push(() => {
      disposed = true;
      closeEvents();
      output.removeEventListener("scroll", onScroll);
      pause?.removeEventListener("click", onPause);
      autoFollow?.removeEventListener("click", onAutoFollow);
      newLinesButton?.removeEventListener("click", onNewLines);
      clearButton?.removeEventListener("click", onClear);
      copyButton?.removeEventListener("click", onCopy);
    });
  }

  function initAssistantWorkspace(cleanups) {
    const root = document.querySelector("[data-assistant-workspace]");
    if (!root) return;

    const csrfToken = root.dataset.csrfToken || "";
    const input = root.querySelector("[data-assistant-input]");
    const composer = root.querySelector("[data-assistant-composer]");
	const sendButton = composer?.querySelector(".assistant-send");
	const abortForm = root.querySelector("[data-assistant-abort]");
	const conversationStatus = root.querySelector("[data-assistant-conversation-status]");
	const turnLockedControls = root.querySelectorAll(".assistant-agent-controls select, .assistant-agent-controls button, .assistant-compact-button");
	const messageList = root.querySelector("[data-assistant-message-list]");
	const transcript = root.querySelector(".assistant-transcript");
	const approvalPanel = root.querySelector("[data-assistant-approval-panel]");
	const approvalForm = root.querySelector("[data-assistant-approval-form]");
	const helper = root.querySelector("[data-assistant-error]");
	const helperDefault = helper?.textContent || "";
	const eventsURL = root.dataset.eventsUrl || "";
	let assistantEvents = null;
	const pendingToolCalls = new Map();
	let followTranscript = true;
	let transcriptScrollFrame = 0;
    const modelControl = root.querySelector("[data-model-picker]");
    const modelToggle = modelControl?.querySelector("[data-model-picker-toggle]");
    const modelPicker = modelControl?.querySelector(".assistant-model-picker");
    const modelInput = root.querySelector("[data-assistant-model-input]");
    const modelLabel = root.querySelector("[data-model-picker-label]");
    const modelChoices = modelPicker ? [...modelPicker.querySelectorAll("[data-model-choice]")] : [];
    const approvalToggle = root.querySelector("[data-auto-approval-toggle]");
    const approvalLabel = approvalToggle?.querySelector("[data-assistant-approval-label]");
	const approvalInput = root.querySelector("[data-assistant-approval-input]");
	const profileInput = root.querySelector("[data-assistant-profile-input]");
	const telemetryContext = root.querySelector("[data-assistant-telemetry-context]");
	const telemetryPercent = root.querySelector("[data-assistant-telemetry-percent]");
	const telemetryProgress = root.querySelector("[data-assistant-telemetry-progress]");
	const telemetryInput = root.querySelector("[data-assistant-telemetry-input]");
	const telemetryOutput = root.querySelector("[data-assistant-telemetry-output]");
	const operationList = root.querySelector("[data-assistant-operation-list]");
	const operationCount = root.querySelector("[data-assistant-operation-count]");
    const inspectorToggle = root.querySelector("[data-assistant-inspector-toggle]");
    const inspectorClose = root.querySelector("[data-assistant-inspector-close]");
    const assistantBody = root.querySelector(".assistant-body");
    const resourcePicker = root.querySelector("[data-resource-picker]");
    const resourceToggle = root.querySelector("[data-resource-picker-toggle]");
    const resourceSearch = root.querySelector("[data-resource-search]");
    const resourceList = resourcePicker?.querySelector(".assistant-resource-list");
    let resourceOptions = resourcePicker ? [...resourcePicker.querySelectorAll(".assistant-resource-option")] : [];
    let resourceSearchTimer = 0;
    let resourceSearchAbort = null;
    const contextHost = root.querySelector("[data-assistant-context-chips]");
    const contextCount = root.querySelector("[data-assistant-context-count]");
    let modelIndex = Math.max(0, modelChoices.findIndex(choice => choice.getAttribute("aria-selected") === "true"));
    let resourceIndex = 0;
    let resourceFilter = "all";
    const selectedResources = new Map();
	contextHost?.querySelectorAll("[data-context-key]").forEach(chip => {
		const key = chip.dataset.contextKey || "";
		if (!key) return;
	selectedResources.set(key, {
			key,
			kind: chip.dataset.contextKind || "",
			id: chip.dataset.contextId || "",
			label: chip.dataset.contextLabel || "",
			image: resourceOptions.some(option => `${option.dataset.resourceKind}:${option.dataset.resourceId}` === key && option.dataset.resourceImage === "true"),
		});
	});
	if (assistantBody && window.matchMedia("(max-width: 1180px)").matches) {
		assistantBody.dataset.inspectorOpen = "false";
		inspectorToggle?.setAttribute("aria-expanded", "false");
	}

	const messageValue = (message, camel, exported) => message?.[camel] ?? message?.[exported] ?? "";
	const tokenFormatter = new Intl.NumberFormat(locale());
	const renderTelemetry = telemetry => {
		if (!telemetry) return;
		const contextTokens = Number(messageValue(telemetry, "contextTokens", "ContextTokens") || 0);
		const contextWindow = Number(messageValue(telemetry, "contextWindow", "ContextWindow") || 0);
		const contextPercent = Math.max(0, Math.min(100, Number(messageValue(telemetry, "contextPercent", "ContextPercent") || 0)));
		const inputTokens = Number(messageValue(telemetry, "inputTokens", "InputTokens") || 0);
		const outputTokens = Number(messageValue(telemetry, "outputTokens", "OutputTokens") || 0);
		if (telemetryContext) telemetryContext.textContent = contextWindow > 0 ? `${tokenFormatter.format(contextTokens)} / ${tokenFormatter.format(contextWindow)} tokens` : "—";
		if (telemetryPercent) telemetryPercent.textContent = contextWindow > 0 ? `${Math.round(contextPercent)}%` : "—";
		if (telemetryProgress) telemetryProgress.value = contextWindow > 0 ? contextPercent : 0;
		if (telemetryInput) telemetryInput.textContent = tokenFormatter.format(inputTokens);
		if (telemetryOutput) telemetryOutput.textContent = tokenFormatter.format(outputTokens);
	};
	const transcriptNearBottom = () => !transcript || transcript.scrollHeight - transcript.scrollTop - transcript.clientHeight <= 72;
	const scrollTranscript = (force = false) => {
		if (!transcript) return;
		if (force) followTranscript = true;
		if (!followTranscript) return;
		if (transcriptScrollFrame) cancelAnimationFrame(transcriptScrollFrame);
		transcriptScrollFrame = requestAnimationFrame(() => {
			transcriptScrollFrame = 0;
			if (followTranscript) transcript.scrollTop = transcript.scrollHeight;
		});
	};
	const onTranscriptScroll = () => {
		followTranscript = transcriptNearBottom();
	};
	const renderAssistantMarkdown = async (segment, source) => {
		if (!segment) return;
		segment.dataset.markdownSource = source;
		segment.classList.remove("markdown-preview");
		segment.textContent = source;
		if (!source.trim()) return;
		try {
			await loadMarkdownLibraries();
			if (!segment.isConnected || segment.dataset.markdownSource !== source) return;
			const renderer = window.markdownit({ html: false, linkify: true, breaks: true });
			const fragment = window.DOMPurify.sanitize(renderer.render(source), {
				USE_PROFILES: { html: true }, SANITIZE_NAMED_PROPS: true, ALLOW_DATA_ATTR: false,
				FORBID_TAGS: ["style", "form", "input", "button", "textarea", "select", "option"],
				FORBID_ATTR: ["style"], RETURN_DOM_FRAGMENT: true,
			});
			rewriteMarkdownResources(fragment, "");
			await highlightMarkdownCode(fragment);
			if (!segment.isConnected || segment.dataset.markdownSource !== source) return;
			segment.replaceChildren(fragment);
			segment.classList.add("markdown-preview");
			scrollTranscript();
		} catch {
			segment.classList.remove("markdown-preview");
			segment.textContent = source;
		}
	};
	const messageSegments = article => [...article.querySelectorAll(":scope > [data-message-segment]")];
	const renderMessageMarkdown = article => {
		if (!article?.classList.contains("assistant-message--assistant") || article.dataset.messageStatus === "streaming") return;
		messageSegments(article).forEach(segment => void renderAssistantMarkdown(segment, segment.dataset.segmentSource || segment.textContent || ""));
	};
	const reflowAssistantMessage = article => {
		if (!article) return;
		const source = article.dataset.messageSource || "";
		const characters = Array.from(source);
		const clusters = [...article.querySelectorAll(":scope > [data-assistant-tool-cluster]")]
			.sort((left, right) => Number(left.dataset.toolOffset || 0) - Number(right.dataset.toolOffset || 0));
		messageSegments(article).forEach(segment => segment.remove());
		let cursor = 0;
		const appendSegment = value => {
			const segment = document.createElement("div");
			segment.dataset.messageBody = "";
			segment.dataset.messageSegment = "";
			segment.dataset.segmentSource = value;
			segment.textContent = value;
			article.append(segment);
		};
		clusters.forEach(cluster => {
			const offset = Math.max(cursor, Math.min(characters.length, Number(cluster.dataset.toolOffset || 0)));
			if (offset > cursor) appendSegment(characters.slice(cursor, offset).join(""));
			article.append(cluster);
			cursor = offset;
		});
		appendSegment(characters.slice(cursor).join(""));
		renderMessageMarkdown(article);
	};
	const renderAssistantMessage = message => {
		if (!messageList || !message) return null;
		const id = messageValue(message, "id", "ID");
		if (!id) return null;
		let article = messageList.querySelector(`[data-message-id="${CSS.escape(id)}"]`);
		const role = messageValue(message, "role", "Role") || "assistant";
		const status = messageValue(message, "status", "Status") || "complete";
		if (!article) {
			article = document.createElement("article");
			article.className = `assistant-message assistant-message--${role === "user" ? "user" : "assistant"}`;
			article.dataset.messageId = id;
			const header = document.createElement("header");
			const author = document.createElement("strong");
			author.textContent = role === "user" ? "YOU" : "PI";
			const timestamp = document.createElement("time");
			const createdAt = messageValue(message, "createdAt", "CreatedAt");
			if (createdAt) {
				timestamp.dateTime = createdAt;
				const date = new Date(createdAt);
				timestamp.textContent = Number.isNaN(date.getTime()) ? "" : new Intl.DateTimeFormat(locale() === "zh-CN" ? "zh-CN" : "en", { hour: "2-digit", minute: "2-digit" }).format(date);
			}
			header.append(author, timestamp);
			article.append(header);
			messageList.append(article);
		}
		article.dataset.messageStatus = status;
		article.dataset.messageSource = messageValue(message, "body", "Body");
		reflowAssistantMessage(article);
		const waiting = pendingToolCalls.get(id);
		if (waiting) {
			pendingToolCalls.delete(id);
			waiting.forEach(renderToolCall);
		}
		scrollTranscript();
		return article;
	};
	const toolStatusLabel = status => {
		const labels = locale() === "zh-CN"
			? { running: "执行中", waiting_approval: "等待审批", complete: "已完成", error: "失败", rejected: "已拒绝", cancelled: "已取消", interrupted: "已中断" }
			: { running: "Running", waiting_approval: "Waiting for approval", complete: "Complete", error: "Failed", rejected: "Rejected", cancelled: "Cancelled", interrupted: "Interrupted" };
		return labels[status] || status;
	};
	const toolStatusIcon = status => status === "complete" ? "check" : status === "waiting_approval" ? "shield-alert" : status === "running" ? "loader-circle" : "triangle-alert";
	const statefulToolNames = new Set(["start_quick_run", "run_schedule_now", "stop_run", "check_website_now", "perform_ui_action"]);
	const unexecutedToolErrors = new Set(["approval_invalid", "approval_expired", "approval_rejected", "approval_cancelled", "tool_target_changed", "tool_forbidden"]);
	const operationLabel = name => ({
		start_quick_run: root.dataset.operationStartQuickRun,
		run_schedule_now: root.dataset.operationRunScheduleNow,
		stop_run: root.dataset.operationStopRun,
		check_website_now: root.dataset.operationCheckWebsiteNow,
		perform_ui_action: root.dataset.operationPerformUiAction,
	}[name] || name);
	const executedStatefulCall = call => {
		const name = messageValue(call, "name", "Name");
		const status = messageValue(call, "status", "Status");
		const errorCode = messageValue(call, "errorCode", "ErrorCode");
		if (!statefulToolNames.has(name) || status === "waiting_approval" || status === "rejected") return false;
		if (status === "cancelled" && errorCode === "approval_cancelled") return false;
		if (status === "error" && unexecutedToolErrors.has(errorCode)) return false;
		return ["running", "complete", "error", "cancelled", "interrupted"].includes(status);
	};
	const renderOperation = call => {
		if (!operationList || !call) return;
		const id = messageValue(call, "id", "ID");
		if (!id) return;
		let row = operationList.querySelector(`[data-operation-id="${CSS.escape(id)}"]`);
		if (!executedStatefulCall(call)) {
			row?.remove();
			if (operationCount) operationCount.textContent = String(operationList.childElementCount);
			return;
		}
		if (!row) {
			row = document.createElement("div");
			row.className = "assistant-operation-row";
			row.dataset.operationId = id;
			const time = document.createElement("time");
			const iconHost = document.createElement("span");
			iconHost.className = "assistant-operation-row__icon";
			iconHost.append(document.createElement("span"));
			row.append(time, iconHost, document.createElement("span"), document.createElement("small"));
			operationList.prepend(row);
		}
		const name = messageValue(call, "name", "Name");
		const status = messageValue(call, "status", "Status") || "running";
		const target = messageValue(call, "targetSummary", "TargetSummary") || messageValue(call, "parameterSummary", "ParameterSummary");
		const startedAt = messageValue(call, "startedAt", "StartedAt");
		const started = startedAt ? new Date(startedAt) : new Date();
		row.dataset.operationName = name;
		row.dataset.operationState = status === "complete" ? "success" : status === "running" ? "running" : "failed";
		row.querySelector("time").dateTime = Number.isNaN(started.valueOf()) ? "" : started.toISOString();
		row.querySelector("time").textContent = Number.isNaN(started.valueOf()) ? "—" : started.toLocaleTimeString(locale(), { hour: "2-digit", minute: "2-digit", hour12: false });
		row.children[2].textContent = target ? `${operationLabel(name)} · ${target}` : operationLabel(name);
		row.querySelector("small").textContent = toolStatusLabel(status);
		replaceIconHost(row.querySelector(".assistant-operation-row__icon > span"), status === "complete" ? "check" : status === "running" ? "loader-circle" : "triangle-alert");
		if (operationCount) operationCount.textContent = String(operationList.childElementCount);
	};
	const formatToolCountSummary = (template, values) => {
		let index = 0;
		return template.replace(/%d/g, () => String(values[index++] ?? 0));
	};
	const toolGroupTitle = count => count === 1
		? (root.dataset.toolsCalledOne || (locale() === "zh-CN" ? "调用了 1 个工具" : "Called 1 tool"))
		: formatToolCountSummary(root.dataset.toolsCalledMany || (locale() === "zh-CN" ? "调用了 %d 个工具" : "Called %d tools"), [count]);
	const toolGroupPresentation = statuses => {
		const succeeded = statuses.filter(status => status === "complete").length;
		const active = statuses.filter(status => status === "running" || status === "waiting_approval").length;
		const failed = statuses.length - succeeded - active;
		const aggregateStatus = statuses.includes("running") ? "running"
			: statuses.includes("waiting_approval") ? "waiting_approval"
				: statuses.find(status => status !== "complete") || "complete";
		const template = active > 0
			? (root.dataset.toolsSummaryActive || (locale() === "zh-CN" ? "成功 %d · 失败 %d · 进行中 %d" : "%d succeeded · %d failed · %d in progress"))
			: (root.dataset.toolsSummary || (locale() === "zh-CN" ? "成功 %d · 失败 %d" : "%d succeeded · %d failed"));
		return { aggregateStatus, summary: formatToolCountSummary(template, active > 0 ? [succeeded, failed, active] : [succeeded, failed]) };
	};
	function ensureToolCluster(messageID, bodyOffset) {
		if (!messageList || !messageID) return null;
		const article = messageList.querySelector(`[data-message-id="${CSS.escape(messageID)}"]`);
		if (!article) return null;
		const offset = Math.max(0, Number(bodyOffset) || 0);
		let cluster = [...article.querySelectorAll(":scope > [data-assistant-tool-cluster]")]
			.find(candidate => Number(candidate.dataset.toolOffset || 0) === offset);
		if (cluster) return cluster;
		cluster = document.createElement("details");
		cluster.className = "assistant-tool-cluster";
		cluster.dataset.assistantToolCluster = "";
		cluster.dataset.toolMessageId = messageID;
		cluster.dataset.toolOffset = String(offset);
		const summary = document.createElement("summary");
		const iconHost = document.createElement("span");
		iconHost.className = "assistant-tool-row__icon";
		const icon = document.createElement("span");
		icon.dataset.toolStatusIcon = "";
		iconHost.append(icon);
		const identity = document.createElement("span");
		const name = document.createElement("strong");
		name.dataset.toolClusterName = "";
		const target = document.createElement("small");
		target.dataset.toolClusterTarget = "";
		identity.append(name, target);
		const status = document.createElement("span");
		status.className = "assistant-tool-status";
		status.dataset.toolClusterStatus = "";
		const chevron = document.createElement("span");
		chevron.dataset.lucide = "chevron-down";
		summary.append(iconHost, identity, status, chevron);
		const list = document.createElement("div");
		list.className = "assistant-tool-list";
		list.dataset.assistantToolList = "";
		cluster.append(summary, list);
		article.append(cluster);
		reflowAssistantMessage(article);
		return cluster;
	}
	function renderToolCall(call) {
		if (!call) return null;
		const id = messageValue(call, "id", "ID");
		if (!id) return null;
		renderOperation(call);
		const messageID = messageValue(call, "messageId", "MessageID");
		const bodyOffset = messageValue(call, "bodyOffset", "BodyOffset");
		const cluster = ensureToolCluster(messageID, bodyOffset);
		if (!cluster) {
			if (messageID) {
				const waiting = pendingToolCalls.get(messageID) || [];
				const previous = waiting.findIndex(item => messageValue(item, "id", "ID") === id);
				if (previous >= 0) waiting[previous] = call;
				else waiting.push(call);
				pendingToolCalls.set(messageID, waiting);
			}
			return null;
		}
		const toolList = cluster.querySelector("[data-assistant-tool-list]");
		let row = toolList.querySelector(`[data-tool-call-id="${CSS.escape(id)}"]`);
		if (!row) {
			row = document.createElement("details");
			row.className = "assistant-tool-row";
			row.dataset.toolCallId = id;
			const summary = document.createElement("summary");
			const iconHost = document.createElement("span");
			iconHost.className = "assistant-tool-row__icon";
			const icon = document.createElement("span");
			icon.dataset.toolStatusIcon = "";
			iconHost.append(icon);
			const identity = document.createElement("span");
			identity.append(document.createElement("strong"), document.createElement("small"));
			const status = document.createElement("span");
			status.className = "assistant-tool-status";
			status.dataset.toolStatusLabel = "";
			const chevron = document.createElement("span");
			chevron.dataset.lucide = "chevron-down";
			summary.append(iconHost, identity, status, chevron);
			const details = document.createElement("div");
			details.className = "assistant-tool-row__details";
			const jsonGrid = document.createElement("div");
			jsonGrid.className = "assistant-tool-json-grid";
			const jsonBlock = (label, dataAttribute) => {
				const section = document.createElement("section");
				const heading = document.createElement("h4");
				heading.textContent = label;
				const pre = document.createElement("pre");
				const code = document.createElement("code");
				code.dataset[dataAttribute] = "";
				pre.append(code);
				section.append(heading, pre);
				return section;
			};
			jsonGrid.append(
				jsonBlock(root.dataset.toolCallJsonLabel || (locale() === "zh-CN" ? "调用 JSON" : "Call JSON"), "toolRequestJson"),
				jsonBlock(root.dataset.toolResponseJsonLabel || (locale() === "zh-CN" ? "返回 JSON" : "Response JSON"), "toolResponseJson"),
			);
			details.append(jsonGrid);
			row.append(summary, details);
			toolList.append(row);
		}
		const status = messageValue(call, "status", "Status") || "running";
		row.dataset.toolStatus = status;
		const name = messageValue(call, "name", "Name") || "tool";
		const target = messageValue(call, "targetSummary", "TargetSummary");
		row.querySelector("summary strong").textContent = name;
		row.querySelector("summary small").textContent = target;
		row.querySelector("[data-tool-status-label]").textContent = toolStatusLabel(status);
		replaceIconHost(row.querySelector("[data-tool-status-icon]"), toolStatusIcon(status));
		row.querySelector("[data-tool-request-json]").textContent = messageValue(call, "requestJSON", "RequestJSON") || "{}";
		row.querySelector("[data-tool-response-json]").textContent = messageValue(call, "responseJSON", "ResponseJSON") || "null";
		const rows = [...toolList.querySelectorAll(":scope > [data-tool-call-id]")];
		const statuses = rows.map(item => item.dataset.toolStatus || "running");
		const { aggregateStatus, summary } = toolGroupPresentation(statuses);
		cluster.querySelector("[data-tool-cluster-name]").textContent = toolGroupTitle(rows.length);
		cluster.querySelector("[data-tool-cluster-target]").textContent = summary;
		cluster.querySelector("[data-tool-cluster-status]").textContent = toolStatusLabel(aggregateStatus);
		replaceIconHost(cluster.querySelector("[data-tool-status-icon]"), toolStatusIcon(aggregateStatus));
		const article = cluster.closest("[data-message-id]");
		reflowAssistantMessage(article);
		renderIcons(row);
		renderIcons(cluster);
		scrollTranscript();
		return row;
	}
	const renderApproval = (approval, call) => {
		if (!approvalPanel || !approvalForm) return;
		const id = messageValue(approval, "id", "ID");
		const status = messageValue(approval, "status", "Status");
		if (!id || status !== "pending") {
			approvalPanel.hidden = true;
			approvalPanel.removeAttribute("data-approval-id");
			return;
		}
		const previousID = approvalPanel.dataset.approvalId || "";
		approvalPanel.hidden = false;
		approvalPanel.dataset.approvalId = id;
		const expires = messageValue(approval, "expiresAt", "ExpiresAt");
		if (expires) approvalPanel.dataset.approvalExpires = expires;
		if (previousID !== id) {
			approvalForm.querySelectorAll("button").forEach(button => { button.disabled = false; });
		}
		const name = messageValue(call, "name", "Name") || (locale() === "zh-CN" ? "状态修改" : "State change");
		const target = messageValue(call, "targetSummary", "TargetSummary");
		const parameters = messageValue(call, "parameterSummary", "ParameterSummary");
		const title = approvalPanel.querySelector("[data-approval-tool]");
		const description = approvalPanel.querySelector("[data-approval-target]");
		if (title) title.textContent = name;
		if (description) description.textContent = [target, parameters].filter(Boolean).join(" · ");
		const conversationID = root.dataset.conversationId || "";
		approvalForm.action = `/ai/conversations/${encodeURIComponent(conversationID)}/approvals/${encodeURIComponent(id)}`;
		renderIcons(approvalPanel);
		scrollTranscript();
	};
	const updateApprovalExpiry = () => {
		if (!approvalPanel || approvalPanel.hidden) return;
		const expiry = new Date(approvalPanel.dataset.approvalExpires || "").getTime();
		if (!Number.isFinite(expiry)) return;
		const seconds = Math.max(0, Math.ceil((expiry - Date.now()) / 1000));
		const label = approvalPanel.querySelector("[data-approval-expiry]");
		if (label) label.textContent = seconds > 0
			? (locale() === "zh-CN" ? `${seconds} 秒后过期` : `Expires in ${seconds}s`)
			: (locale() === "zh-CN" ? "审批已过期" : "Approval expired");
		if (seconds === 0) approvalForm?.querySelectorAll("button").forEach(button => { button.disabled = true; });
	};
	const approvalClock = window.setInterval(updateApprovalExpiry, 1000);
	updateApprovalExpiry();
	const setConversationStatus = status => {
		if (!conversationStatus || !status) return;
		const normalized = status === "complete" ? "idle" : status === "error" ? "failed" : status;
		const label = conversationStatus.getAttribute(`data-label-${normalized}`);
		if (label) conversationStatus.textContent = label;
	};
	const setTurnRunning = (running, status = running ? "running" : "idle") => {
		if (sendButton) sendButton.disabled = running || root.dataset.runtimeAvailable !== "true";
		if (abortForm) abortForm.hidden = !running;
		turnLockedControls.forEach(control => { control.disabled = running; });
		setConversationStatus(status);
		if (helper) helper.textContent = running ? (locale() === "zh-CN" ? "Pi 正在生成…" : "Pi is responding…") : helperDefault;
	};
	const handleAssistantEvent = event => {
		let payload;
		try { payload = JSON.parse(event.data); } catch { return; }
		if (event.type === "snapshot") {
			if (messageList) messageList.replaceChildren();
			if (operationList) operationList.replaceChildren();
			if (operationCount) operationCount.textContent = "0";
			pendingToolCalls.clear();
			(payload.messages || []).forEach(renderAssistantMessage);
			(payload.toolCalls || []).forEach(renderToolCall);
			renderApproval(payload.approval, (payload.toolCalls || []).find(call => messageValue(call, "id", "ID") === messageValue(payload.approval, "toolCallId", "ToolCallID")));
			const messages = payload.messages || [];
			const running = messages.some(message => messageValue(message, "status", "Status") === "streaming");
			const latestAssistant = [...messages].reverse().find(message => messageValue(message, "role", "Role") === "assistant");
			const settledStatus = latestAssistant ? messageValue(latestAssistant, "status", "Status") : "idle";
			setTurnRunning(running, payload.approval ? "waiting_approval" : running ? "running" : settledStatus);
			return;
		}
		if (event.type === "message") {
			renderAssistantMessage(payload.message || payload.Message);
			if (messageValue(payload.message || payload.Message, "status", "Status") === "streaming") setTurnRunning(true);
			return;
		}
		if (["tool_started", "tool_updated", "tool_finished", "approval_requested", "approval_resolved"].includes(event.type)) {
			const call = payload.toolCall || payload.ToolCall;
			renderToolCall(call);
			if (event.type === "approval_requested") {
				renderApproval(payload.approval || payload.Approval, call);
				setTurnRunning(true, "waiting_approval");
			}
			if (event.type === "approval_resolved") {
				renderApproval(null, call);
				setTurnRunning(true, "running");
			}
			return;
		}
		if (event.type === "retrying" || event.type === "compacting") {
			if (helper) {
				const running = (payload.status || payload.Status) === "running";
				if (running && event.type === "retrying") {
					const attempt = payload.attempt || payload.Attempt || 0;
					helper.textContent = locale() === "zh-CN" ? `Provider 正在重试${attempt ? `（第 ${attempt} 次）` : ""}…` : `Provider retrying${attempt ? ` (attempt ${attempt})` : ""}…`;
				} else if (running) helper.textContent = locale() === "zh-CN" ? "Pi 正在压缩对话上下文…" : "Pi is compacting conversation context…";
				else helper.textContent = helperDefault;
			}
			return;
		}
		const messageId = payload.messageId || payload.MessageID || "";
		const article = messageId && messageList?.querySelector(`[data-message-id="${CSS.escape(messageId)}"]`);
		if (event.type === "delta") {
			if (article) {
				const cumulative = payload.body ?? payload.Body;
				article.dataset.messageSource = typeof cumulative === "string"
					? cumulative
					: (article.dataset.messageSource || "") + (payload.delta || payload.Delta || "");
				article.dataset.messageStatus = "streaming";
				reflowAssistantMessage(article);
			}
			setTurnRunning(true);
			scrollTranscript();
			return;
		}
		if (event.type === "settled") {
			if (article) article.dataset.messageStatus = payload.status || payload.Status || "complete";
			renderTelemetry(payload.telemetry || payload.Telemetry);
			renderMessageMarkdown(article);
			setTurnRunning(false, payload.status || payload.Status || "complete");
			scrollTranscript();
		}
	};
	if (eventsURL && window.EventSource) {
		assistantEvents = new EventSource(eventsURL);
		["snapshot", "message", "delta", "settled", "tool_started", "tool_updated", "tool_finished", "approval_requested", "approval_resolved", "retrying", "compacting"].forEach(type => assistantEvents.addEventListener(type, handleAssistantEvent));
		assistantEvents.addEventListener("error", () => {
			if (helper && assistantEvents?.readyState === EventSource.CLOSED) helper.textContent = locale() === "zh-CN" ? "实时连接已断开，刷新页面可恢复。" : "Live connection closed. Refresh to reconnect.";
		});
	}

    const setModelPicker = open => {
      if (!modelPicker || !modelToggle) return;
      modelPicker.dataset.open = String(open);
      modelPicker.setAttribute("aria-hidden", String(!open));
      modelToggle.setAttribute("aria-expanded", String(open));
      if (open) {
        modelChoices.forEach((choice, index) => { choice.dataset.active = String(index === modelIndex); });
        modelChoices[modelIndex]?.focus();
      }
    };
    const markSelectedModel = (id, name) => {
      if (modelInput) modelInput.value = id;
      if (modelLabel) modelLabel.textContent = name;
      modelControl.dataset.invalid = String(!id);
      modelChoices.forEach(choice => {
        const selected = choice.dataset.modelChoice === id;
        choice.setAttribute("aria-selected", String(selected));
      });
    };
    const selectModel = async choice => {
      const id = choice?.dataset.modelChoice || "";
      const name = choice?.dataset.modelName || "";
      if (!id) return;
      const endpoint = modelControl.dataset.modelEndpoint;
      if (endpoint) {
        modelControl.setAttribute("aria-busy", "true");
        try {
          const response = await fetch(endpoint, {
            method: "POST", credentials: "same-origin",
            headers: { "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8", "Accept": "application/json" },
            body: new URLSearchParams({ csrf_token: csrfToken, model_id: id }),
          });
          if (!response.ok) throw new Error(`HTTP ${response.status}`);
        } catch {
          modelControl.dataset.invalid = "true";
          modelToggle?.focus();
          return;
        } finally {
          modelControl.removeAttribute("aria-busy");
        }
      }
      markSelectedModel(id, name);
      setModelPicker(false);
      modelToggle?.focus();
    };

    const setApproval = enabled => {
      if (!approvalToggle) return;
      approvalToggle.setAttribute("aria-pressed", String(enabled));
      if (approvalInput) approvalInput.value = String(enabled);
      if (approvalLabel) approvalLabel.textContent = enabled ? approvalLabel.dataset.autoLabel : approvalLabel.dataset.manualLabel;
      replaceIconHost(approvalToggle.querySelector("[data-approval-icon]"), enabled ? "zap" : "shield-check");
    };
    const toggleApproval = async () => {
      if (!approvalToggle || approvalToggle.disabled) return;
      const next = approvalToggle.getAttribute("aria-pressed") !== "true";
      const endpoint = approvalToggle.dataset.endpoint;
      if (!endpoint) {
        setApproval(next);
        return;
      }
      approvalToggle.disabled = true;
      try {
        const response = await fetch(endpoint, {
          method: "POST", credentials: "same-origin",
          headers: { "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8", "Accept": "application/json" },
          body: new URLSearchParams({ csrf_token: csrfToken, auto_approval: String(next) }),
        });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        setApproval(next);
      } finally {
        approvalToggle.disabled = false;
      }
    };

    const visibleResourceOptions = () => resourceOptions.filter(option => !option.hidden);
	const renderDynamicResource = (resource, query) => {
		if (!resourceList || !resource?.id || !resource?.kind) return;
		const key = `${resource.kind}:${resource.id}`;
		if (resourceOptions.some(option => `${option.dataset.resourceKind}:${option.dataset.resourceId}` === key)) return;
		const option = document.createElement("button");
		option.type = "button";
		option.setAttribute("role", "option");
		option.setAttribute("aria-selected", String(selectedResources.has(key)));
		option.className = "assistant-resource-option";
		option.dataset.resourceDynamic = "true";
		option.dataset.resourceId = resource.id;
		option.dataset.resourceKind = resource.kind;
		option.dataset.resourceCategory = resource.category || "files";
		option.dataset.resourceLabel = resource.label || resource.id;
		option.dataset.resourceImage = String(Boolean(resource.imageHint));
		option.dataset.resourceSearchText = `${resource.label || ""} ${resource.detail || ""} ${query}`;
		const icon = document.createElement("span");
		icon.className = "assistant-resource-option__icon";
		icon.dataset.lucide = resource.icon || (resource.kind === "directory" ? "folder" : "file-text");
		icon.setAttribute("aria-hidden", "true");
		const copy = document.createElement("span");
		const title = document.createElement("strong");
		title.textContent = resource.label || resource.id;
		const detail = document.createElement("small");
		detail.textContent = `${resource.kind} · ${resource.detail || ""}`;
		copy.append(title, detail);
		const indicator = document.createElement("span");
		indicator.className = "assistant-resource-option__indicator";
		indicator.dataset.resourceOptionIndicator = "";
		indicator.dataset.lucide = selectedResources.has(key) ? "check" : "plus";
		indicator.setAttribute("aria-hidden", "true");
		option.append(icon, copy, indicator);
		resourceList.append(option);
		resourceOptions.push(option);
		renderIcons(option);
	};
	const searchHostPathResources = () => {
		const query = (resourceSearch?.value || "").trim();
		const absolute = query.startsWith("/") || query.startsWith("\\\\") || /^[a-z]:[\\/]/i.test(query);
		if (!absolute || !resourcePicker?.dataset.resourceEndpoint) return;
		window.clearTimeout(resourceSearchTimer);
		resourceSearchTimer = window.setTimeout(async () => {
			resourceSearchAbort?.abort();
			resourceSearchAbort = new AbortController();
			try {
				const endpoint = new URL(resourcePicker.dataset.resourceEndpoint, window.location.origin);
				endpoint.searchParams.set("query", query);
				const response = await fetch(endpoint, { credentials: "same-origin", headers: { Accept: "application/json" }, signal: resourceSearchAbort.signal });
				if (!response.ok) return;
				resourceOptions.filter(option => option.dataset.resourceDynamic === "true").forEach(option => option.remove());
				resourceOptions = resourceOptions.filter(option => option.dataset.resourceDynamic !== "true");
				const payload = await response.json();
				(payload.resources || []).forEach(resource => renderDynamicResource(resource, query));
				filterResources();
			} catch (error) {
				if (error?.name !== "AbortError") resourceSearchAbort = null;
			}
		}, 180);
	};
    const filterResources = () => {
      const query = (resourceSearch?.value || "").trim().toLocaleLowerCase();
      resourceOptions.forEach(option => {
        const categoryMatches = resourceFilter === "all" || option.dataset.resourceCategory === resourceFilter;
        const queryMatches = !query || (option.dataset.resourceSearchText || "").toLocaleLowerCase().includes(query);
        option.hidden = !categoryMatches || !queryMatches;
      });
      resourceIndex = 0;
      visibleResourceOptions().forEach((option, index) => { option.dataset.active = String(index === resourceIndex); });
    };
    const setResourcePicker = (open, focus = false) => {
      if (!resourcePicker || !resourceToggle) return;
      resourcePicker.dataset.open = String(open);
      resourcePicker.setAttribute("aria-hidden", String(!open));
      resourceToggle.setAttribute("aria-expanded", String(open));
      if (open) {
        filterResources();
        if (focus) requestAnimationFrame(() => resourceSearch?.focus());
      }
    };
    const renderContextResources = () => {
      if (!contextHost || !contextCount) return;
      contextHost.replaceChildren();
      selectedResources.forEach(resource => {
        const chip = document.createElement("span");
        chip.className = "assistant-context-chip";
		chip.dataset.contextKey = resource.key;
		chip.dataset.contextKind = resource.kind;
		chip.dataset.contextId = resource.id;
		chip.dataset.contextLabel = resource.label;
        const label = document.createElement("span");
        label.textContent = resource.label;
		const imageIcon = document.createElement("span");
		imageIcon.dataset.lucide = resource.image ? "image" : "paperclip";
		imageIcon.setAttribute("aria-hidden", "true");
		const kindInput = document.createElement("input");
		kindInput.type = "hidden";
		kindInput.name = "context_kind";
		kindInput.value = resource.kind;
		const idInput = document.createElement("input");
		idInput.type = "hidden";
		idInput.name = "context_id";
		idInput.value = resource.id;
        const remove = document.createElement("button");
        remove.type = "button";
		remove.setAttribute("aria-label", `${root.dataset.removeReferenceLabel || "Remove reference"} ${resource.label}`);
        remove.dataset.removeResource = resource.key;
        const icon = document.createElement("span");
        icon.dataset.lucide = "x";
        remove.append(icon);
		chip.append(imageIcon, label, kindInput, idInput, remove);
        contextHost.append(chip);
      });
      contextCount.textContent = String(selectedResources.size);
      contextCount.hidden = selectedResources.size === 0;
      contextHost.hidden = selectedResources.size === 0;
		const imageCount = [...selectedResources.values()].filter(resource => resource.image).length;
		if (helper) helper.textContent = imageCount > 0 ? `${imageCount} image${imageCount === 1 ? "" : "s"} will be safety-processed before sending.` : helperDefault;
      renderIcons(contextHost);
    };
    const chooseResource = option => {
      if (!option) return;
      const key = `${option.dataset.resourceKind}:${option.dataset.resourceId}`;
      if (selectedResources.has(key)) selectedResources.delete(key);
	  else selectedResources.set(key, {
		key,
		kind: option.dataset.resourceKind || "",
		id: option.dataset.resourceId || "",
		label: option.dataset.resourceLabel || option.dataset.resourceId,
		image: option.dataset.resourceImage === "true",
	  });
      option.setAttribute("aria-selected", String(selectedResources.has(key)));
      replaceIconHost(option.querySelector("[data-resource-option-indicator]"), selectedResources.has(key) ? "check" : "plus");
      renderContextResources();
    };

    const onClick = event => {
      if (event.target.closest("[data-model-picker-toggle]")) {
        event.preventDefault();
        setModelPicker(modelPicker?.dataset.open !== "true");
        return;
      }
      const modelChoice = event.target.closest("[data-model-choice]");
      if (modelChoice && root.contains(modelChoice)) {
        event.preventDefault();
        void selectModel(modelChoice);
        return;
      }
      if (event.target.closest("[data-auto-approval-toggle]")) {
        event.preventDefault();
        void toggleApproval();
        return;
      }
      if (event.target.closest("[data-assistant-inspector-toggle]")) {
        const open = assistantBody?.dataset.inspectorOpen !== "true";
        if (assistantBody) assistantBody.dataset.inspectorOpen = String(open);
        inspectorToggle?.setAttribute("aria-expanded", String(open));
        return;
      }
      if (event.target.closest("[data-assistant-inspector-close]")) {
        if (assistantBody) assistantBody.dataset.inspectorOpen = "false";
        inspectorToggle?.setAttribute("aria-expanded", "false");
        inspectorToggle?.focus();
        return;
      }
      if (event.target.closest("[data-assistant-rail-open]")) {
        document.body.classList.add("assistant-rail-open");
        return;
      }
      if (event.target.closest("[data-resource-picker-toggle]")) {
        event.preventDefault();
        setResourcePicker(resourcePicker?.dataset.open !== "true", true);
        return;
      }
      const filter = event.target.closest("[data-resource-filter]");
      if (filter && root.contains(filter)) {
        resourceFilter = filter.dataset.resourceFilter || "all";
        root.querySelectorAll("[data-resource-filter]").forEach(button => button.setAttribute("aria-pressed", String(button === filter)));
        filterResources();
        return;
      }
      const option = event.target.closest(".assistant-resource-option");
      if (option && root.contains(option)) {
        chooseResource(option);
        return;
      }
      const remove = event.target.closest("[data-remove-resource]");
      if (remove && root.contains(remove)) {
        selectedResources.delete(remove.dataset.removeResource);
        resourceOptions.forEach(item => {
          const key = `${item.dataset.resourceKind}:${item.dataset.resourceId}`;
          if (key === remove.dataset.removeResource) {
            item.setAttribute("aria-selected", "false");
            replaceIconHost(item.querySelector("[data-resource-option-indicator]"), "plus");
          }
        });
        renderContextResources();
        return;
      }
      const prompt = event.target.closest("[data-assistant-prompt]");
      if (prompt && input) {
        input.value = prompt.dataset.assistantPrompt || "";
		if (profileInput && prompt.dataset.assistantProfile) profileInput.value = prompt.dataset.assistantProfile;
        input.dispatchEvent(new Event("input", { bubbles: true }));
        input.focus();
      }
    };
    const onDocumentClick = event => {
      if (modelPicker?.dataset.open === "true" && !event.target.closest("[data-model-picker]")) setModelPicker(false);
      if (resourcePicker?.dataset.open === "true" && !event.target.closest("[data-resource-picker], [data-resource-picker-toggle]")) setResourcePicker(false);
    };
    const onInput = () => {
      if (!input) return;
      input.style.height = "auto";
      input.style.height = `${Math.min(input.scrollHeight, 180)}px`;
      if (input.value.endsWith("@") && resourcePicker?.dataset.open !== "true") setResourcePicker(true, true);
    };
    const onKeydown = event => {
      if (event.key === "Escape") {
        if (modelPicker?.dataset.open === "true") { event.preventDefault(); setModelPicker(false); modelToggle?.focus(); return; }
        if (resourcePicker?.dataset.open === "true") { event.preventDefault(); setResourcePicker(false); input?.focus(); return; }
      }
      if (modelPicker?.contains(event.target) && ["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
        event.preventDefault();
        if (event.key === "Home") modelIndex = 0;
        else if (event.key === "End") modelIndex = modelChoices.length - 1;
        else modelIndex = (modelIndex + (event.key === "ArrowDown" ? 1 : -1) + modelChoices.length) % modelChoices.length;
        modelChoices.forEach((choice, index) => { choice.dataset.active = String(index === modelIndex); });
        modelChoices[modelIndex]?.focus();
        return;
      }
      if (event.target === resourceSearch && ["ArrowDown", "ArrowUp", "Enter"].includes(event.key)) {
        const visible = visibleResourceOptions();
        if (!visible.length) return;
        event.preventDefault();
        if (event.key === "Enter") chooseResource(visible[resourceIndex]);
        else resourceIndex = (resourceIndex + (event.key === "ArrowDown" ? 1 : -1) + visible.length) % visible.length;
        visible.forEach((option, index) => { option.dataset.active = String(index === resourceIndex); });
        visible[resourceIndex]?.scrollIntoView({ block: "nearest" });
        return;
      }
      if (event.target === input && event.key === "Enter" && !event.shiftKey && !event.isComposing) {
        if (composer?.querySelector("button[type='submit']")?.disabled) return;
        event.preventDefault();
        composer?.requestSubmit();
      }
    };
    const onSubmit = event => {
      if (event.target !== composer) return;
      if (!modelInput?.value) {
        event.preventDefault();
        modelControl.dataset.invalid = "true";
        setModelPicker(true);
        return;
      }
      if (!root.dataset.conversationId) {
        const title = composer.elements.title;
        if (title && input?.value.trim()) title.value = input.value.trim().slice(0, 80);
		return;
      }
	  event.preventDefault();
	  if (!input?.value.trim() || sendButton?.disabled) return;
	  scrollTranscript(true);
	  if (helper) helper.textContent = locale() === "zh-CN" ? "正在提交…" : "Submitting…";
	  if (sendButton) sendButton.disabled = true;
	  void fetch(composer.action, {
		method: "POST", credentials: "same-origin", headers: { "Accept": "application/json" }, body: new FormData(composer),
	  }).then(async response => {
		if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
		input.value = "";
		onInput();
		setTurnRunning(true);
	  }).catch(error => {
		if (helper) helper.textContent = error.message;
		if (sendButton) sendButton.disabled = root.dataset.runtimeAvailable !== "true";
	  });
    };
	const onApprovalSubmit = event => {
		if (event.target !== approvalForm) return;
		event.preventDefault();
		const decision = event.submitter?.value || "";
		if (!approvalForm.action || !["approve", "reject"].includes(decision)) return;
		const buttons = [...approvalForm.querySelectorAll("button")];
		buttons.forEach(button => { button.disabled = true; });
		void fetch(approvalForm.action, {
			method: "POST", credentials: "same-origin",
			headers: { "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8", "Accept": "application/json" },
			body: new URLSearchParams({ csrf_token: csrfToken, decision }),
		}).then(async response => {
			if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
			renderApproval(null, null);
			if (helper) helper.textContent = decision === "approve"
				? (locale() === "zh-CN" ? "已批准，执行前正在重新授权。" : "Approved; reauthorizing before execution.")
				: (locale() === "zh-CN" ? "已拒绝此次操作。" : "Action rejected.");
		}).catch(error => {
			if (helper) helper.textContent = error.message;
			buttons.forEach(button => { button.disabled = false; });
		});
	};

    root.addEventListener("click", onClick);
    document.addEventListener("click", onDocumentClick);
    root.addEventListener("keydown", onKeydown);
    input?.addEventListener("input", onInput);
    resourceSearch?.addEventListener("input", filterResources);
    resourceSearch?.addEventListener("input", searchHostPathResources);
	composer?.addEventListener("submit", onSubmit);
	approvalForm?.addEventListener("submit", onApprovalSubmit);
	transcript?.addEventListener("scroll", onTranscriptScroll, { passive: true });
	scrollTranscript(true);
    cleanups.push(() => {
      root.removeEventListener("click", onClick);
      document.removeEventListener("click", onDocumentClick);
      root.removeEventListener("keydown", onKeydown);
      input?.removeEventListener("input", onInput);
      resourceSearch?.removeEventListener("input", filterResources);
      resourceSearch?.removeEventListener("input", searchHostPathResources);
	  window.clearTimeout(resourceSearchTimer);
	  resourceSearchAbort?.abort();
      composer?.removeEventListener("submit", onSubmit);
	  approvalForm?.removeEventListener("submit", onApprovalSubmit);
	  transcript?.removeEventListener("scroll", onTranscriptScroll);
	  if (transcriptScrollFrame) cancelAnimationFrame(transcriptScrollFrame);
	  window.clearInterval(approvalClock);
	  assistantEvents?.close();
      document.body.classList.remove("assistant-rail-open");
    });
  }

  function initAssistantSettings(cleanups) {
    const root = document.querySelector("[data-assistant-settings]");
    if (!root) return;
    const layer = root.querySelector("[data-llm-drawer]");
    const drawer = layer?.querySelector(".assistant-llm-drawer");
    const form = layer?.querySelector("[data-llm-form]");
    const title = layer?.querySelector("[data-llm-drawer-title]");
    const credentialHelp = layer?.querySelector("[data-credential-help]");
    const reasoningCapability = form?.elements.supports_reasoning;
    const thinkingDetail = layer?.querySelector("[data-thinking-detail]");
    const thinkingLevel = form?.elements.default_thinking_level;
    const guardrailLayer = root.querySelector("[data-guardrail-drawer]");
    const guardrailDrawer = guardrailLayer?.querySelector(".assistant-guardrail-drawer");
    const guardrailForm = guardrailLayer?.querySelector("form");
    let returnFocus = null;
    let guardrailReturnFocus = null;

    const syncThinkingCapability = () => {
      const enabled = Boolean(reasoningCapability?.checked);
      thinkingDetail?.classList.toggle("is-visible", enabled);
      thinkingDetail?.setAttribute("aria-hidden", String(!enabled));
      if (thinkingLevel) thinkingLevel.disabled = !enabled;
    };

    const close = () => {
      if (!layer) return;
      layer.dataset.open = "false";
      layer.setAttribute("aria-hidden", "true");
      document.body.style.overflow = "";
      returnFocus?.focus?.();
      returnFocus = null;
    };
    const open = row => {
      if (!layer || !form) return;
      returnFocus = document.activeElement;
      form.reset();
      form.elements.id.value = row?.dataset.llmId || "";
      form.elements.name.value = row?.dataset.name || "";
      form.elements.provider.value = row?.dataset.provider || "openai";
      form.elements.model.value = row?.dataset.model || "";
      form.elements.endpoint.value = row?.dataset.endpoint || "https://api.openai.com/v1";
      form.elements.api_key.value = "";
	  form.elements.api_key.required = !row;
      form.elements.make_default.checked = row?.dataset.default === "true";
		form.elements.supports_images.checked = row?.dataset.supportsImages === "true";
		form.elements.supports_reasoning.checked = row?.dataset.supportsReasoning === "true";
		form.elements.default_thinking_level.value = row?.dataset.defaultThinkingLevel || "medium";
		form.elements.shared.checked = row?.dataset.shared === "true";
      syncThinkingCapability();
      if (title) title.textContent = row ? layer.dataset.editTitle : layer.dataset.addTitle;
      if (credentialHelp) credentialHelp.dataset.editing = String(Boolean(row));
      layer.dataset.open = "true";
      layer.setAttribute("aria-hidden", "false");
      document.body.style.overflow = "hidden";
      requestAnimationFrame(() => form.elements.name.focus());
    };
    const closeGuardrails = () => {
      if (!guardrailLayer) return;
      guardrailLayer.dataset.open = "false";
      guardrailLayer.setAttribute("aria-hidden", "true");
      if (layer?.dataset.open !== "true") document.body.style.overflow = "";
      guardrailReturnFocus?.focus?.();
      guardrailReturnFocus = null;
    };
    const openGuardrails = trigger => {
      if (!guardrailLayer) return;
      guardrailReturnFocus = trigger || document.activeElement;
      guardrailLayer.dataset.open = "true";
      guardrailLayer.setAttribute("aria-hidden", "false");
      document.body.style.overflow = "hidden";
      requestAnimationFrame(() => guardrailForm?.querySelector("input:not([type='hidden']),select,button")?.focus());
    };
    const onClick = event => {
      if (event.target.closest("[data-add-llm]")) { open(null); return; }
      const edit = event.target.closest("[data-edit-llm]");
      if (edit) { open(edit.closest("[data-llm-id]")); return; }
      if (event.target.closest("[data-close-llm]")) { event.preventDefault(); close(); return; }
      const guardrailTrigger = event.target.closest("[data-open-guardrails]");
      if (guardrailTrigger) { openGuardrails(guardrailTrigger); return; }
      if (event.target.closest("[data-close-guardrails]")) { event.preventDefault(); closeGuardrails(); }
    };
    const onChange = event => {
      if (event.target === reasoningCapability) syncThinkingCapability();
    };
    const trapFocus = (event, activeDrawer) => {
      if (event.key !== "Tab" || !activeDrawer) return;
      const focusable = [...activeDrawer.querySelectorAll("button:not([disabled]),input:not([disabled]):not([type='hidden']),select:not([disabled]),textarea:not([disabled]),a[href]")]
        .filter(element => element.getClientRects().length > 0);
      if (!focusable.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && event.target === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && event.target === last) { event.preventDefault(); first.focus(); }
    };
    const onKeydown = event => {
      if (guardrailLayer?.dataset.open === "true") {
        if (event.key === "Escape") { event.preventDefault(); closeGuardrails(); return; }
        trapFocus(event, guardrailDrawer);
        return;
      }
      if (layer?.dataset.open === "true") {
        if (event.key === "Escape") { event.preventDefault(); close(); return; }
        trapFocus(event, drawer);
      }
    };
    root.addEventListener("click", onClick);
    form?.addEventListener("change", onChange);
    document.addEventListener("keydown", onKeydown);
    cleanups.push(() => {
      root.removeEventListener("click", onClick);
      form?.removeEventListener("change", onChange);
      document.removeEventListener("keydown", onKeydown);
      document.body.style.overflow = "";
    });
  }

  function initSecurityDialogs(cleanups) {
    document.querySelectorAll("dialog.security-dialog[open]").forEach(dialog => {
      dialog.removeAttribute("open");
      if (typeof dialog.showModal === "function") dialog.showModal();
      else dialog.setAttribute("open", "");
      const onCancel = event => {
        event.preventDefault();
        const close = dialog.querySelector('a[href^="/monitor/security"]');
        if (close) close.click();
      };
      dialog.addEventListener("cancel", onCancel);
      cleanups.push(() => {
        dialog.removeEventListener("cancel", onCancel);
        if (dialog.open) dialog.close();
      });
    });
  }

  function showConnectionTestError(form, message) {
    const dialog = document.createElement("dialog");
    dialog.className = "connection-test-dialog";
    dialog.innerHTML = '<header><div><p></p><h2></h2></div><button class="icon-button icon-button--quiet" type="button" data-dialog-close><span data-lucide="x" aria-hidden="true"></span></button></header><p class="connection-test-dialog__message"></p><footer><button class="button" type="button" data-dialog-close></button></footer>';
    const title = form.dataset.connectionTitle || "Connection test";
    dialog.querySelector("header p").textContent = form.dataset.connectionFailure || "Connection failed";
    dialog.querySelector("h2").textContent = title;
    dialog.querySelector(".connection-test-dialog__message").textContent = message;
    dialog.querySelector("footer button").textContent = form.dataset.closeLabel || "Close";
    dialog.querySelector("header button").setAttribute("aria-label", form.dataset.closeLabel || "Close");
    const close = () => dialog.close();
    dialog.querySelectorAll("[data-dialog-close]").forEach(button => button.addEventListener("click", close));
    dialog.addEventListener("close", () => dialog.remove(), { once: true });
    document.body.append(dialog);
    renderIcons(dialog);
    dialog.showModal();
    dialog.querySelector("footer button")?.focus();
  }

  async function submitConnectionTest(form, submitter) {
    const result = form.querySelector("[data-connection-test-result]");
    if (result) {
      result.classList.add("sr-only");
      result.removeAttribute("data-state");
      result.textContent = "";
    }
    const data = new FormData(form);
    if (submitter?.name) data.set(submitter.name, submitter.value);
    try {
      const response = await fetch(form.action, {
        method: "POST",
        body: data,
        credentials: "same-origin",
        headers: { "Accept": "application/json" },
      });
      const contentType = response.headers.get("content-type") || "";
      const payload = contentType.includes("application/json") ? await response.json() : { message: (await response.text()).trim() };
      const ok = response.ok && (payload.ok === true || payload.OK === true);
      const detail = payload.message || payload.Error || payload.error || "";
      if (!ok) throw new Error(detail || `HTTP ${response.status}`);
      const version = payload.Version ? ` · ${payload.Version}` : "";
      if (result) {
        result.classList.remove("sr-only");
        result.dataset.state = "success";
        result.textContent = `${form.dataset.connectionSuccess || detail || "Connected"}${version}`;
      }
    } catch (error) {
      const message = error?.message || form.dataset.connectionFailure || words().submitFailed;
      showConnectionTestError(form, message);
    } finally {
      resetSubmit(form);
    }
  }

  function initMySQLDrawers(cleanups) {
    const root = document.querySelector("[data-mysql-workspace]");
    if (!root) return;
    root.querySelectorAll('form[method="post"]:not([data-connection-test])').forEach(form => form.dataset.async = "");
    const drawers = [...root.querySelectorAll("details.mysql-drawer")];
    const dropDrawer = root.querySelector("[data-mysql-drop-drawer]");
    const backupRestoreDrawer = root.querySelector("[data-mysql-backup-restore-drawer]");
    const backupDeleteDrawer = root.querySelector("[data-mysql-backup-delete-drawer]");
    let active = null;
    const returnFocus = new WeakMap();

    const close = (drawer, restoreFocus = true) => {
      if (!drawer?.open) return;
      drawer.open = false;
      drawer.querySelector(":scope > summary")?.setAttribute("aria-expanded", "false");
      if (active === drawer) active = null;
      if (guardrailLayer?.dataset.open !== "true") document.body.style.overflow = "";
      const trigger = returnFocus.get(drawer);
      if (restoreFocus && trigger) trigger.focus();
      else if (restoreFocus) drawer.querySelector(":scope > summary")?.focus();
      returnFocus.delete(drawer);
    };
    const onToggle = event => {
      const drawer = event.currentTarget;
      const summary = drawer.querySelector(":scope > summary");
      summary?.setAttribute("aria-expanded", String(drawer.open));
      if (!drawer.open) {
        if (active === drawer) {
          active = null;
          document.body.style.overflow = "";
        }
        return;
      }
      drawers.forEach(candidate => { if (candidate !== drawer && candidate.open) close(candidate, false); });
      active = drawer;
      document.body.style.overflow = "hidden";
      window.setTimeout(() => drawer.querySelector(".mysql-drawer-sheet")?.focus(), 180);
    };
    const onClick = event => {
      const restoreTrigger = event.target.closest("[data-mysql-backup-restore-trigger]");
      if (restoreTrigger && backupRestoreDrawer) {
        event.preventDefault();
        restoreTrigger.closest("details.action-menu")?.removeAttribute("open");
        returnFocus.set(backupRestoreDrawer, restoreTrigger);
        const backupID = restoreTrigger.dataset.backupId || "";
        const database = restoreTrigger.dataset.database || "";
        const form = backupRestoreDrawer.querySelector("[data-mysql-backup-restore-form]");
        const backupName = backupRestoreDrawer.querySelector("[data-mysql-backup-restore-id]");
        const databaseInput = backupRestoreDrawer.querySelector("[data-mysql-backup-restore-database]");
        const confirmation = backupRestoreDrawer.querySelector("[data-mysql-backup-restore-confirmation]");
        if (form) form.action = `/resources/databases/backups/${encodeURIComponent(backupID)}/restore`;
        if (backupName) backupName.textContent = backupID;
        if (databaseInput) databaseInput.value = database;
        if (confirmation) confirmation.value = "";
        backupRestoreDrawer.open = true;
        window.setTimeout(() => databaseInput?.focus(), 190);
        return;
      }
      const deleteTrigger = event.target.closest("[data-mysql-backup-delete-trigger]");
      if (deleteTrigger && backupDeleteDrawer) {
        event.preventDefault();
        deleteTrigger.closest("details.action-menu")?.removeAttribute("open");
        returnFocus.set(backupDeleteDrawer, deleteTrigger);
        const backupID = deleteTrigger.dataset.backupId || "";
        const form = backupDeleteDrawer.querySelector("[data-mysql-backup-delete-form]");
        const backupName = backupDeleteDrawer.querySelector("[data-mysql-backup-delete-id]");
        const confirmation = backupDeleteDrawer.querySelector("[data-mysql-backup-delete-confirmation]");
        if (form) form.action = `/resources/databases/backups/${encodeURIComponent(backupID)}/delete`;
        if (backupName) backupName.textContent = backupID;
        if (confirmation) confirmation.value = "";
        backupDeleteDrawer.open = true;
        window.setTimeout(() => confirmation?.focus(), 190);
        return;
      }
      const dropTrigger = event.target.closest("[data-mysql-drop-trigger]");
      if (dropTrigger && dropDrawer) {
        event.preventDefault();
        dropTrigger.closest("details.action-menu")?.removeAttribute("open");
        returnFocus.set(dropDrawer, dropTrigger);
        const database = dropTrigger.dataset.database || "";
        const databaseInput = dropDrawer.querySelector("[data-mysql-drop-database]");
        const databaseName = dropDrawer.querySelector("[data-mysql-drop-name]");
        const confirmation = dropDrawer.querySelector("[data-mysql-drop-confirmation]");
        if (databaseInput) databaseInput.value = database;
        if (databaseName) databaseName.textContent = database;
        if (confirmation) confirmation.value = "";
        dropDrawer.open = true;
        window.setTimeout(() => confirmation?.focus(), 190);
        return;
      }
      const control = event.target.closest("[data-mysql-drawer-close]");
      if (!control) return;
      const drawer = control.closest("details.mysql-drawer");
      if (!drawer) return;
      event.preventDefault();
      close(drawer);
    };
    const onKeydown = event => {
      if (!active?.open) return;
      if (event.key === "Escape") {
        event.preventDefault();
        close(active);
        return;
      }
      if (event.key !== "Tab") return;
      const sheet = active.querySelector(".mysql-drawer-sheet");
      const focusable = [...sheet.querySelectorAll("a[href],button:not([disabled]),input:not([disabled]):not([type='hidden']),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex='-1'])")]
        .filter(element => element.getClientRects().length > 0);
      if (!focusable.length) { event.preventDefault(); sheet.focus(); return; }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && (event.target === first || !sheet.contains(event.target))) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && event.target === last) { event.preventDefault(); first.focus(); }
    };

    drawers.forEach(drawer => {
      drawer.querySelector(":scope > summary")?.setAttribute("aria-expanded", String(drawer.open));
      drawer.addEventListener("toggle", onToggle);
    });
    root.addEventListener("click", onClick);
    document.addEventListener("keydown", onKeydown);
    cleanups.push(() => {
      drawers.forEach(drawer => drawer.removeEventListener("toggle", onToggle));
      root.removeEventListener("click", onClick);
      document.removeEventListener("keydown", onKeydown);
      document.body.style.overflow = "";
    });
  }

  function initExternalKeyManagers(cleanups) {
    const root = document.querySelector("[data-external-interfaces-page]");
    if (!root) return;
    if (!root.querySelector("details[data-external-key-manager]")) return;
    let active = null;

    const close = (manager, restoreFocus = true) => {
      if (!manager?.open) return;
      manager.open = false;
      manager.querySelector(":scope > summary")?.setAttribute("aria-expanded", "false");
      if (active === manager) active = null;
      document.body.style.overflow = "";
      if (restoreFocus) manager.querySelector(":scope > summary")?.focus();
    };
    const onToggle = event => {
      const manager = event.target.closest?.("details[data-external-key-manager]");
      if (!manager || !root.contains(manager)) return;
      manager.querySelector(":scope > summary")?.setAttribute("aria-expanded", String(manager.open));
      if (!manager.open) {
        if (active === manager) {
          active = null;
          document.body.style.overflow = "";
        }
        return;
      }
      root.querySelectorAll("details[data-external-key-manager][open]").forEach(candidate => { if (candidate !== manager) close(candidate, false); });
      active = manager;
      document.body.style.overflow = "hidden";
      window.setTimeout(() => manager.querySelector(".external-key-manager__sheet")?.focus(), 180);
    };
    const onClick = event => {
      const taskLink = event.target.closest("a[data-task-link]");
      if (taskLink) {
        const manager = taskLink.closest("details[data-external-key-manager]");
        if (manager) {
          event.preventDefault();
          event.stopPropagation();
          const returnFocus = manager.querySelector(":scope > summary");
          const destination = taskLink.href;
          close(manager, false);
          openTask(destination, true, returnFocus);
        }
        return;
      }
      const control = event.target.closest("[data-external-key-manager-close]");
      if (!control) return;
      const manager = control.closest("details[data-external-key-manager]");
      if (!manager) return;
      event.preventDefault();
      close(manager);
    };
    const onKeydown = event => {
      if (active && !active.isConnected) active = root.querySelector("details[data-external-key-manager][open]");
      if (!active?.open) return;
      if (event.key === "Escape") {
        event.preventDefault();
        close(active);
        return;
      }
      if (event.key !== "Tab") return;
      const sheet = active.querySelector(".external-key-manager__sheet");
      const focusable = [...sheet.querySelectorAll("a[href],button:not([disabled]),input:not([disabled]):not([type='hidden']),[tabindex]:not([tabindex='-1'])")]
        .filter(element => element.getClientRects().length > 0);
      if (!focusable.length) { event.preventDefault(); sheet.focus(); return; }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && (event.target === first || !sheet.contains(event.target))) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && event.target === last) { event.preventDefault(); first.focus(); }
    };

    root.querySelectorAll("details[data-external-key-manager]").forEach(manager => {
      manager.querySelector(":scope > summary")?.setAttribute("aria-expanded", String(manager.open));
    });
    root.addEventListener("toggle", onToggle, true);
    root.addEventListener("click", onClick);
    document.addEventListener("keydown", onKeydown);
    cleanups.push(() => {
      root.removeEventListener("toggle", onToggle, true);
      root.removeEventListener("click", onClick);
      document.removeEventListener("keydown", onKeydown);
      document.body.style.overflow = "";
    });
  }

  function initStateBackupDrawers(cleanups) {
    const root = document.querySelector("[data-state-backups-page]");
    if (!root) return;
    const drawers = [...root.querySelectorAll("details[data-state-backup-drawer]")];
    if (!drawers.length) return;
    let active = null;

    const close = (drawer, restoreFocus = true) => {
      if (!drawer?.open) return;
      drawer.open = false;
      drawer.querySelector(":scope > summary")?.setAttribute("aria-expanded", "false");
      if (active === drawer) active = null;
      document.body.style.overflow = "";
      if (restoreFocus) drawer.querySelector(":scope > summary")?.focus();
    };
    const onToggle = event => {
      const drawer = event.currentTarget;
      const summary = drawer.querySelector(":scope > summary");
      if (drawer.open && summary?.getAttribute("aria-disabled") === "true") {
        drawer.open = false;
        return;
      }
      summary?.setAttribute("aria-expanded", String(drawer.open));
      if (!drawer.open) {
        if (active === drawer) {
          active = null;
          document.body.style.overflow = "";
        }
        return;
      }
      drawers.forEach(candidate => { if (candidate !== drawer && candidate.open) close(candidate, false); });
      active = drawer;
      document.body.style.overflow = "hidden";
      window.setTimeout(() => drawer.querySelector(".state-backup-drawer__sheet")?.focus(), 180);
    };
    const onClick = event => {
      const control = event.target.closest("[data-state-backup-drawer-close]");
      if (!control) return;
      const drawer = control.closest("details[data-state-backup-drawer]");
      if (!drawer) return;
      event.preventDefault();
      close(drawer);
    };
    const onKeydown = event => {
      if (!active?.open) return;
      if (event.key === "Escape") {
        event.preventDefault();
        close(active);
        return;
      }
      if (event.key !== "Tab") return;
      const sheet = active.querySelector(".state-backup-drawer__sheet");
      const focusable = [...sheet.querySelectorAll("a[href],button:not([disabled]),input:not([disabled]):not([type='hidden']),[tabindex]:not([tabindex='-1'])")]
        .filter(element => element.getClientRects().length > 0);
      if (!focusable.length) { event.preventDefault(); sheet.focus(); return; }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && (event.target === first || !sheet.contains(event.target))) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && event.target === last) { event.preventDefault(); first.focus(); }
    };

    drawers.forEach(drawer => {
      drawer.querySelector(":scope > summary")?.setAttribute("aria-expanded", String(drawer.open));
      drawer.addEventListener("toggle", onToggle);
    });
    root.addEventListener("click", onClick);
    document.addEventListener("keydown", onKeydown);
    cleanups.push(() => {
      drawers.forEach(drawer => drawer.removeEventListener("toggle", onToggle));
      root.removeEventListener("click", onClick);
      document.removeEventListener("keydown", onKeydown);
      document.body.style.overflow = "";
    });
  }

  function initSecurityBanDrawer(cleanups) {
    const root = document.querySelector("[data-security-page]");
    const host = document.querySelector("[data-security-ban-drawer]");
    const drawer = host?.querySelector(".security-ban-drawer");
    const closeLink = drawer?.querySelector("[data-security-ban-drawer-close]");
    const content = drawer?.querySelector("[data-security-ban-drawer-content]");
    const pagination = drawer?.querySelector("[data-security-ban-pagination]");
    const loadingTemplate = drawer?.querySelector("[data-security-ban-loading-template]");
    const errorTemplate = drawer?.querySelector("[data-security-ban-error-template]");
    if (!root || !host || !drawer || !closeLink || !content || !pagination || !loadingTemplate || !errorTemplate) return;
    let closing = false;
    let closeTimer = null;
    let focusTimer = null;
    let opener = null;
    let requestController = null;
    let requestID = 0;
    let lastURL = "";

    const replaceWithTemplate = template => {
      content.replaceChildren(document.importNode(template.content, true));
      renderIcons(content);
    };
    const showLoading = () => {
      drawer.setAttribute("aria-busy", "true");
      pagination.hidden = true;
      replaceWithTemplate(loadingTemplate);
    };
    const showError = error => {
      drawer.removeAttribute("aria-busy");
      pagination.hidden = true;
      replaceWithTemplate(errorTemplate);
      const detail = content.querySelector("[data-security-ban-error-detail]");
      if (detail) detail.textContent = error?.message || "";
    };
    const load = async url => {
      requestController?.abort();
      requestController = new AbortController();
      const controller = requestController;
      const currentRequest = ++requestID;
      lastURL = url;
      showLoading();
      try {
        const result = await fetchDocument(url, { cache: "no-store", signal: controller.signal });
        if (!result.response.ok) throw new Error(`HTTP ${result.response.status}`);
        const sourceDrawer = result.document?.querySelector("[data-security-ban-drawer]");
        const sourceContent = sourceDrawer?.querySelector("[data-security-ban-drawer-content]");
        const sourcePagination = sourceDrawer?.querySelector("[data-security-ban-pagination]");
        if (!sourceContent || !sourcePagination) throw new Error("Ban details were not present in the response.");
        if (currentRequest !== requestID || controller.signal.aborted) return;
        content.replaceChildren(...Array.from(sourceContent.childNodes, node => document.importNode(node, true)));
        pagination.replaceChildren(...Array.from(sourcePagination.childNodes, node => document.importNode(node, true)));
        pagination.className = sourcePagination.className;
        pagination.hidden = sourcePagination.hidden;
        drawer.removeAttribute("aria-busy");
        renderIcons(host);
        localizeTimes(host);
      } catch (error) {
        if (error?.name !== "AbortError" && currentRequest === requestID) showError(error);
      } finally {
        if (requestController === controller) requestController = null;
      }
    };
    const focusClose = () => {
      if (focusTimer) window.clearTimeout(focusTimer);
      focusTimer = window.setTimeout(() => closeLink.focus(), 180);
    };
    const open = trigger => {
      opener = trigger;
      closing = false;
      host.classList.add("is-open");
      host.setAttribute("aria-hidden", "false");
      document.body.style.overflow = "hidden";
      focusClose();
      load(trigger.href);
    };

    const close = event => {
      event?.preventDefault();
      if (closing || !host.classList.contains("is-open")) return;
      closing = true;
      requestController?.abort();
      requestController = null;
      requestID++;
      const destination = new URL(closeLink.href, location.href);
      host.classList.remove("is-open");
      host.setAttribute("aria-hidden", "true");
      document.body.style.overflow = "";
      const delay = matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 180;
      closeTimer = window.setTimeout(() => {
        if (new URL(location.href).searchParams.has("bans")) {
          history.replaceState({ pjax: true }, "", destination.href);
          updateShellLocation(destination.href);
        }
        showLoading();
        opener?.focus();
        opener = null;
        closing = false;
      }, delay);
    };
    const onClick = event => {
      const trigger = event.target.closest("[data-security-ban-drawer-trigger]");
      if (trigger && root.contains(trigger)) {
        event.preventDefault();
        if (host.classList.contains("is-open")) load(trigger.href);
        else open(trigger);
        return;
      }
      if (event.target.closest("[data-security-ban-drawer-close]")) {
        close(event);
        return;
      }
      if (event.target.closest("[data-security-ban-retry]") && lastURL) load(lastURL);
    };
    const onKeydown = event => {
      if (event.key === "Escape") {
        close(event);
        return;
      }
      if (event.key !== "Tab") return;
      const focusable = Array.from(drawer.querySelectorAll("button:not([disabled]),a[href],[tabindex]:not([tabindex='-1'])"))
        .filter(element => !element.hidden && element.getClientRects().length > 0);
      if (!focusable.length) {
        event.preventDefault();
        drawer.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && (event.target === first || !drawer.contains(event.target))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && event.target === last) {
        event.preventDefault();
        first.focus();
      }
    };
    if (host.classList.contains("is-open")) {
      document.body.style.overflow = "hidden";
      focusClose();
    }
    root.addEventListener("click", onClick);
    document.addEventListener("keydown", onKeydown);
    cleanups.push(() => {
      root.removeEventListener("click", onClick);
      document.removeEventListener("keydown", onKeydown);
      requestController?.abort();
      if (closeTimer) window.clearTimeout(closeTimer);
      if (focusTimer) window.clearTimeout(focusTimer);
      document.body.style.overflow = "";
    });
  }

  function initUpdateSourceDrawer(cleanups) {
    const root = document.querySelector("[data-updates-page]");
    const host = root?.querySelector("[data-update-source-drawer]");
    const drawer = host?.querySelector(".update-source-drawer");
    if (!root || !host || !drawer) return;
    let opener = null;
    let focusTimer = 0;
    const focusTarget = () => drawer.querySelector("input[name='source_id']:checked") || drawer;
    const clearFocusTimer = () => {
      if (focusTimer) window.clearTimeout(focusTimer);
      focusTimer = 0;
    };
    const scheduleFocus = (callback, delay) => {
      clearFocusTimer();
      focusTimer = window.setTimeout(() => {
        focusTimer = 0;
        callback();
      }, delay);
    };
    const open = event => {
      event?.preventDefault();
      opener = event?.target?.closest("[data-update-source-open]") || document.activeElement;
      host.classList.add("is-open");
      host.setAttribute("aria-hidden", "false");
      document.body.classList.add("has-update-source-drawer");
      scheduleFocus(() => focusTarget()?.focus(), 180);
    };
    const close = event => {
      event?.preventDefault();
      if (!host.classList.contains("is-open")) return;
      host.classList.remove("is-open");
      host.setAttribute("aria-hidden", "true");
      document.body.classList.remove("has-update-source-drawer");
      if (new URL(location.href).searchParams.get("sources") === "1") {
        history.replaceState(history.state, "", "/settings/updates");
      }
      const restore = opener;
      scheduleFocus(() => restore?.focus(), 200);
    };
    const onClick = event => {
      // A fast submit can happen before the drawer's delayed initial focus.
      // Cancel that stale task so it cannot steal focus back from a later
      // server-error dialog or from the control the user actually selected.
      if (drawer.contains(event.target)) clearFocusTimer();
      if (event.target.closest("[data-update-source-open]")) {
        open(event);
      } else if (event.target.closest("[data-update-source-close]")) {
        close(event);
      }
    };
    const onKeydown = event => {
      if (!host.classList.contains("is-open")) return;
      clearFocusTimer();
      if (event.key === "Escape") {
        close(event);
        return;
      }
      if (event.key !== "Tab") return;
      const focusable = [...drawer.querySelectorAll("a[href],button:not([disabled]),input:not([disabled]),[tabindex]:not([tabindex='-1'])")]
        .filter(element => !element.hidden && element.getClientRects().length > 0);
      if (!focusable.length) {
        event.preventDefault();
        drawer.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && (event.target === first || !drawer.contains(event.target))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && event.target === last) {
        event.preventDefault();
        first.focus();
      }
    };
    if (host.classList.contains("is-open")) {
      document.body.classList.add("has-update-source-drawer");
      scheduleFocus(() => focusTarget()?.focus(), 0);
    }
    root.addEventListener("click", onClick);
    document.addEventListener("keydown", onKeydown);
    cleanups.push(() => {
      root.removeEventListener("click", onClick);
      document.removeEventListener("keydown", onKeydown);
      document.body.classList.remove("has-update-source-drawer");
      clearFocusTimer();
    });
  }

  const containerDetailCopy = {
    "zh-CN": {
      loading: "正在读取容器详情…", loadFailed: "无法读取容器详情", runtime: "运行信息", versions: "版本记录", noVersions: "暂无版本记录",
      network: "网络与端口", storage: "挂载", processes: "关联进程", noPorts: "未发布宿主端口", noMounts: "没有可读取的挂载", noProcesses: "当前没有可读取的进程",
      start: "启动", stop: "停止", restart: "重启", logs: "查看日志", confirm: action => `确认${action}容器？`, samples: "资源趋势",
      internalOnly: "仅容器内部", readOnly: "只读", readWrite: "读写",
      facts: { containerId: "容器 ID", image: "镜像", health: "健康状态", restartPolicy: "重启策略", restartCount: "重启次数", networkMode: "网络模式", memory: "内存", cpu: "CPU", processes: "进程" }
    },
    "en-US": {
      loading: "Loading container details…", loadFailed: "Container details unavailable", runtime: "Runtime", versions: "Versions", noVersions: "No version records",
      network: "Network and ports", storage: "Mounts", processes: "Related processes", noPorts: "No host ports are published", noMounts: "No readable mounts", noProcesses: "No readable processes right now",
      start: "Start", stop: "Stop", restart: "Restart", logs: "View logs", confirm: action => `${action} this container?`, samples: "Resource trend",
      internalOnly: "Container only", readOnly: "Read only", readWrite: "Read/write",
      facts: { containerId: "Container ID", image: "Image", health: "Health", restartPolicy: "Restart policy", restartCount: "Restart count", networkMode: "Network mode", memory: "Memory", cpu: "CPU", processes: "Processes" }
    }
  };

  function containerWords() { return containerDetailCopy[locale()]; }

  function containerBytes(value) {
    let amount = Number(value) || 0;
    if (amount < 1024) return `${amount} B`;
    const units = ["KiB", "MiB", "GiB", "TiB"];
    let index = 0;
    amount /= 1024;
    while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index += 1; }
    return `${amount >= 10 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`;
  }

  function renderContainerDetails(payload, trigger, root) {
    const words = containerWords();
    const application = payload?.application || {};
    const runtime = payload?.runtime || {};
    const docker = runtime?.docker || {};
    const versions = Array.isArray(payload?.versions) ? payload.versions : [];
    const ports = Array.isArray(docker.ports) ? docker.ports : [];
    const mounts = Array.isArray(docker.mounts) ? docker.mounts : [];
    const processes = Array.isArray(docker.relatedProcesses) ? docker.relatedProcesses : [];
    const nameKey = decodeURIComponent(new URL(trigger.dataset.containerDetailUrl, location.href).pathname.split("/").at(-2) || "");
    const state = trigger.dataset.containerState || "stopped";
    const returnTo = root.dataset.containerReturnTo || "/monitor/containers";
    const csrf = root.dataset.csrfToken || "";
    const actionForm = (action, label, danger = false) => `<form method="post" action="/monitor/containers/${encodeURIComponent(nameKey)}/operate" data-container-operation><input type="hidden" name="csrf_token" value="${escapeMarkup(csrf)}"><input type="hidden" name="action" value="${action}"><input type="hidden" name="return_to" value="${escapeMarkup(returnTo)}"><button class="button button--compact${danger ? " button--danger" : ""}" type="submit"><span data-lucide="${action === "start" ? "play" : action === "stop" ? "square" : "rotate-cw"}" aria-hidden="true"></span>${escapeMarkup(label)}</button></form>`;
    const canManage = root.dataset.containerCanManage === "true";
    const actions = canManage
      ? (state === "stopped" || state === "missing" ? actionForm("start", words.start) : actionForm("restart", words.restart) + actionForm("stop", words.stop, true))
      : "";
    const logLink = trigger.dataset.containerApplicationId
      ? `<a class="button button--compact" href="/monitor/applications/${encodeURIComponent(trigger.dataset.containerApplicationId)}/logs"><span data-lucide="scroll-text" aria-hidden="true"></span>${escapeMarkup(words.logs)}</a>`
      : "";
    const facts = [
      [words.facts.containerId, docker.containerId || "—"], [words.facts.image, docker.image || application.technical || trigger.dataset.containerImage || "—"],
      [words.facts.health, docker.health || "—"], [words.facts.restartPolicy, docker.restartPolicy || "—"], [words.facts.restartCount, docker.restartCount ?? "—"],
      [words.facts.networkMode, docker.networkMode || "—"], [words.facts.memory, containerBytes(application.memoryBytes || 0)],
      [words.facts.cpu, application.rateAvailable ? `${Number(application.cpuPercent || 0).toFixed(1)}%` : "—"], [words.facts.processes, application.processCount ?? processes.length]
    ].map(([label, value]) => `<div><dt>${escapeMarkup(label)}</dt><dd>${escapeMarkup(value)}</dd></div>`).join("");
    const versionRows = versions.length ? versions.map(version => `<div><strong>${escapeMarkup(version.image || "—")}</strong><span>${escapeMarkup(new Date(version.observedAt).toLocaleString())} · ${escapeMarkup(version.containerId || "—")}</span></div>`).join("") : `<div class="container-detail-empty">${escapeMarkup(words.noVersions)}</div>`;
    const portRows = ports.length ? ports.map(port => `<div><strong>${escapeMarkup(`${port.containerPort}/${port.protocol}`)}</strong><span>${escapeMarkup(port.hostPort ? `${port.hostAddress || "0.0.0.0"}:${port.hostPort}` : words.internalOnly)}</span></div>`).join("") : `<div class="container-detail-empty">${escapeMarkup(words.noPorts)}</div>`;
    const mountRows = mounts.length ? mounts.map(mount => `<div><strong>${escapeMarkup(mount.destination || "—")}</strong><span>${escapeMarkup(`${mount.source || mount.type || "—"} · ${mount.readOnly ? words.readOnly : words.readWrite}`)}</span></div>`).join("") : `<div class="container-detail-empty">${escapeMarkup(words.noMounts)}</div>`;
    const processRows = processes.length ? processes.map(process => `<div><strong>${escapeMarkup(process.name || process.commandLine || "—")}</strong><span>PID ${escapeMarkup(process.pid ?? "—")} · ${escapeMarkup(process.user || "—")}</span></div>`).join("") : `<div class="container-detail-empty">${escapeMarkup(words.noProcesses)}</div>`;
    return `<div class="container-detail-actions">${actions}${logLink}</div>
      <section class="container-detail-section"><h3>${escapeMarkup(words.runtime)}</h3><dl class="container-detail-facts">${facts}</dl></section>
      <section class="container-detail-section"><h3>${escapeMarkup(words.samples)}</h3>${renderApplicationHistory(payload)}</section>
      <section class="container-detail-section"><h3>${escapeMarkup(words.versions)}</h3><div class="container-detail-list">${versionRows}</div></section>
      <section class="container-detail-section"><h3>${escapeMarkup(words.network)}</h3><div class="container-detail-list">${portRows}</div></section>
      <section class="container-detail-section"><h3>${escapeMarkup(words.storage)}</h3><div class="container-detail-list">${mountRows}</div></section>
      <section class="container-detail-section"><h3>${escapeMarkup(words.processes)}</h3><div class="container-detail-list">${processRows}</div></section>`;
  }

  function initContainers(cleanups) {
    const root = document.querySelector("[data-container-page]");
    if (!root) return;
    const drawer = root.querySelector("[data-container-drawer]");
    const body = drawer?.querySelector("[data-container-drawer-body]");
    const title = drawer?.querySelector("[data-container-drawer-title]");
    const meta = drawer?.querySelector("[data-container-drawer-meta]");
    let snapshotController = null;
    const replaceSnapshot = async destination => {
      snapshotController?.abort(); snapshotController = new AbortController();
      const scrollX = window.scrollX, scrollY = window.scrollY;
      root.setAttribute("aria-busy", "true");
      try {
        const { response, document: page } = await fetchDocument(destination, { cache: "no-store", signal: snapshotController.signal });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const incoming = page?.querySelector("[data-container-page]");
        if (!incoming) throw new Error("Container snapshot was not present in the response.");
        for (const selector of ["[data-container-status-tabs]", ".container-toolbar", ".container-result-summary", "[data-container-table-shell]"]) {
          const current = root.querySelector(selector), next = incoming.querySelector(selector);
          if (current && next) current.replaceWith(next);
        }
        root.dataset.containerReturnTo = incoming.dataset.containerReturnTo || "/monitor/containers";
        history.pushState({ containerMonitor: true }, "", destination);
        renderIcons(root); window.requestAnimationFrame(() => window.scrollTo(scrollX, scrollY));
      } catch (error) { if (error?.name !== "AbortError") console.error(error); }
      finally { root.removeAttribute("aria-busy"); }
    };
    const openDetail = async trigger => {
      if (!drawer || !body) return;
      title.textContent = trigger.dataset.containerName || "Container"; meta.textContent = trigger.dataset.containerImage || "";
      body.innerHTML = `<div class="container-drawer__loading"><span data-lucide="loader-circle" aria-hidden="true"></span>${escapeMarkup(containerWords().loading)}</div>`;
      renderIcons(body); if (!drawer.open) drawer.showModal();
      try {
        const response = await fetch(trigger.dataset.containerDetailUrl, { headers: { Accept: "application/json" }, cache: "no-store" });
        if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
        const payload = await response.json();
        title.textContent = payload?.application?.name || title.textContent;
        meta.textContent = [payload?.application?.technical, trigger.dataset.containerState].filter(Boolean).join(" · ");
        body.innerHTML = renderContainerDetails(payload, trigger, root); renderIcons(body);
      } catch (error) { body.innerHTML = `<div class="container-detail-empty" role="alert">${escapeMarkup(error?.message || containerWords().loadFailed)}</div>`; }
    };
    const onClick = event => {
      const snapshotLink = event.target.closest("[data-container-status-link],.container-sort-link");
      if (snapshotLink && event.button === 0 && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) { event.preventDefault(); replaceSnapshot(snapshotLink.href); return; }
      const detail = event.target.closest("[data-container-detail-url]");
      if (detail) { event.preventDefault(); openDetail(detail); return; }
      if (event.target.closest("[data-container-drawer-close]")) drawer?.close();
    };
    const onSubmit = event => {
      const form = event.target.closest("form[data-container-operation]");
      if (!form || form.elements.confirmed?.value === "yes") return;
      const action = form.elements.action?.value || "", label = containerWords()[action] || action;
      form.dataset.confirm = containerWords().confirm(label);
    };
    const onDrawerClick = event => { if (event.target === drawer) drawer.close(); };
    root.addEventListener("click", onClick); root.addEventListener("submit", onSubmit); drawer?.addEventListener("click", onDrawerClick);
    cleanups.push(() => { snapshotController?.abort(); root.removeEventListener("click", onClick); root.removeEventListener("submit", onSubmit); drawer?.removeEventListener("click", onDrawerClick); if (drawer?.open) drawer.close(); });
  }

  const kubernetesDetailCopy = {
    "zh-CN": {
      loading: "正在读取工作负载…", loadFailed: "无法读取工作负载详情", confirm: "确认执行此操作？",
      runtime: "运行状态", pods: "Pods", events: "事件", versions: "版本记录", logs: "日志",
      redeploy: "滚动重部署", scaleDown: "减少 1 个副本", scaleUp: "增加 1 个副本", runNow: "立即运行",
      confirmRedeploy: "确认触发这个工作负载的滚动重部署？", confirmScaleDown: "确认将副本数减少 1？", confirmScaleUp: "确认将副本数增加 1？", confirmRun: "确认立即从这个 CronJob 创建一次 Job？",
      noPods: "暂无 Pod", noEvents: "暂无事件", noVersions: "暂无版本记录", noLogs: "暂无日志",
      testing: "正在测试连接…", connected: "连接成功", testFailed: "连接测试失败",
      restartCount: value => `${value} 次重启`,
      states: { ready: "就绪", progressing: "变更中", degraded: "需查看", missing: "未发现" },
      facts: { status: "状态", ready: "就绪", restarts: "重启", cpu: "CPU", memory: "内存", node: "节点" }
    },
    "en-US": {
      loading: "Loading workload…", loadFailed: "Workload details unavailable", confirm: "Run this operation?",
      runtime: "Runtime", pods: "Pods", events: "Events", versions: "Versions", logs: "Logs",
      redeploy: "Rolling redeploy", scaleDown: "Scale down by 1", scaleUp: "Scale up by 1", runNow: "Run now",
      confirmRedeploy: "Trigger a rolling redeploy for this workload?", confirmScaleDown: "Scale this workload down by one replica?", confirmScaleUp: "Scale this workload up by one replica?", confirmRun: "Create a Job from this CronJob now?",
      noPods: "No Pods", noEvents: "No events", noVersions: "No version records", noLogs: "No logs",
      testing: "Testing connection…", connected: "Connected", testFailed: "Connection test failed",
      restartCount: value => `${value} restarts`,
      states: { ready: "Ready", progressing: "Progressing", degraded: "Needs attention", missing: "Missing" },
      facts: { status: "Status", ready: "Ready", restarts: "Restarts", cpu: "CPU", memory: "Memory", node: "Node" }
    }
  };

  function kubernetesWords() { return kubernetesDetailCopy[locale()]; }

  function renderKubernetesDetail(payload, trigger, root) {
    const words = kubernetesWords();
    const kubernetesBytes = value => {
      const bytes = Number(value) || 0;
      if (bytes < 1024) return `${bytes} B`;
      const units = ["KiB", "MiB", "GiB", "TiB"];
      let amount = bytes / 1024;
      let index = 0;
      while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index += 1; }
      return `${amount >= 10 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`;
    };
    const kubernetesTime = value => {
      if (!value) return "—";
      const parsed = new Date(value);
      return Number.isNaN(parsed.getTime()) ? String(value) : parsed.toLocaleString();
    };
    const workload = payload?.Workload || {};
    const pods = Array.isArray(payload?.Pods) ? payload.Pods : [];
    const events = Array.isArray(payload?.Events) ? payload.Events : [];
    const versions = Array.isArray(payload?.Versions) ? payload.Versions : [];
    const operationURL = new URL(trigger.dataset.kubernetesDetailUrl, location.href).pathname.replace(/\/details$/, "/operate");
    const returnTo = `${location.pathname}${location.search}`;
    const actionForm = (operation, label, confirmation, replicas) => `<form method="post" action="${escapeMarkup(operationURL)}" data-kubernetes-confirm="${escapeMarkup(confirmation)}"><input type="hidden" name="csrf_token" value="${escapeMarkup(root.dataset.csrfToken || "")}"><input type="hidden" name="operation" value="${escapeMarkup(operation)}">${replicas === undefined ? "" : `<input type="hidden" name="replicas" value="${escapeMarkup(replicas)}">`}<input type="hidden" name="return_to" value="${escapeMarkup(returnTo)}"><button class="button button--compact" type="submit"><span data-lucide="${operation === "redeploy" ? "rotate-cw" : operation === "run_cron" ? "play" : replicas < Number(workload.Desired || 0) ? "minus" : "plus"}" aria-hidden="true"></span>${escapeMarkup(label)}</button></form>`;
    const canOperate = root.dataset.kubernetesCanManage === "true" && root.dataset.kubernetesMode === "limited";
    let actions = "";
    if (canOperate && workload.Kind !== "CronJob" && root.dataset.kubernetesCanRedeploy === "true") actions += actionForm("redeploy", words.redeploy, words.confirmRedeploy);
    if (canOperate && ["Deployment", "StatefulSet"].includes(workload.Kind) && root.dataset.kubernetesCanScale === "true") {
      if (Number(workload.Desired || 0) > 0) actions += actionForm("scale", words.scaleDown, words.confirmScaleDown, Number(workload.Desired) - 1);
      actions += actionForm("scale", words.scaleUp, words.confirmScaleUp, Number(workload.Desired || 0) + 1);
    }
    if (canOperate && workload.Kind === "CronJob" && root.dataset.kubernetesCanRunCron === "true") actions += actionForm("run_cron", words.runNow, words.confirmRun);
    if (root.dataset.kubernetesCanLogs === "true") {
      const logURL = new URL(trigger.dataset.kubernetesDetailUrl, location.href).pathname.replace(/\/details$/, "/logs");
      const logLabel = locale() === "zh-CN" ? "\u67e5\u770b\u65e5\u5fd7" : "View logs";
      actions += `<a class="button button--compact" href="${escapeMarkup(logURL)}"><span data-lucide="scroll-text" aria-hidden="true"></span>${escapeMarkup(logLabel)}</a>`;
    }
    const facts = [
      [words.facts.status, words.states[workload.Status] || workload.StatusLabel || workload.Status || "—"],
      [words.facts.ready, `${workload.Ready ?? 0} / ${workload.Desired ?? 0}`],
      [words.facts.restarts, workload.Restarts ?? 0],
      [words.facts.cpu, `${workload.CPUMillicores ?? 0}m`],
      [words.facts.memory, kubernetesBytes(workload.MemoryBytes || 0)],
      [words.facts.node, workload.Nodes || "—"]
    ].map(([label, value]) => `<div><dt>${escapeMarkup(label)}</dt><dd>${escapeMarkup(value)}</dd></div>`).join("");
    const rows = (items, render, empty) => items.length
      ? items.map(render).join("")
      : `<div class="kubernetes-detail-empty">${escapeMarkup(empty)}</div>`;
    const podRows = rows(pods, pod => `<div><strong>${escapeMarkup(pod.Name)}</strong><span>${escapeMarkup(pod.Phase)} · ${escapeMarkup(pod.Ready)} · ${escapeMarkup(pod.Node || "—")} · ${escapeMarkup(words.restartCount(pod.Restarts || 0))}</span></div>`, words.noPods);
    const eventRows = rows(events.slice(0, 20), event => `<div><strong>${escapeMarkup(event.Reason || event.Type)}</strong><span>${escapeMarkup(event.Message)}</span></div>`, words.noEvents);
    const versionRows = rows(versions.slice(0, 20), version => `<div><strong>${escapeMarkup(version.Image || "—")}</strong><span>${escapeMarkup(kubernetesTime(version.ObservedAt))} · ${escapeMarkup(version.Revision || "—")}</span></div>`, words.noVersions);
    return `${actions ? `<div class="kubernetes-detail-actions">${actions}</div>` : ""}
      <section class="kubernetes-detail-section"><h3>${escapeMarkup(words.runtime)}</h3><dl class="kubernetes-detail-facts">${facts}</dl></section>
      <section class="kubernetes-detail-section"><h3>${escapeMarkup(words.pods)}</h3><div class="kubernetes-detail-list">${podRows}</div></section>
      <section class="kubernetes-detail-section"><h3>${escapeMarkup(words.events)}</h3><div class="kubernetes-detail-list">${eventRows}</div></section>
      <section class="kubernetes-detail-section"><h3>${escapeMarkup(words.versions)}</h3><div class="kubernetes-detail-list">${versionRows}</div></section>`;
  }

  function initKubernetes(cleanups) {
    const root = document.querySelector("[data-kubernetes-page]");
    if (!root) return;
    const clusterSelect = root.querySelector("[data-kubernetes-cluster-select]");
    const drawer = root.querySelector("[data-kubernetes-drawer]");
    const body = drawer?.querySelector("[data-kubernetes-drawer-body]");
    const title = drawer?.querySelector("[data-kubernetes-drawer-title]");
    const meta = drawer?.querySelector("[data-kubernetes-drawer-meta]");
    const local = root.querySelector("[data-kubernetes-local]");
    const importDrawer = local?.querySelector("[data-kubernetes-import-drawer]");
    const importForm = local?.querySelector("[data-kubernetes-import-form]");
    const importFile = local?.querySelector("[data-kubernetes-import-file]");
    const importPreview = local?.querySelector("[data-kubernetes-import-preview]");
    const contextDrawer = local?.querySelector("[data-kubernetes-context-drawer]");
    const contextUpdateForm = local?.querySelector("[data-kubernetes-context-update-form]");
    const contextRenameForm = local?.querySelector("[data-kubernetes-context-rename-form]");
    const contextSearch = local?.querySelector("[data-kubernetes-context-search]");
    let snapshotController = null;
    const openDetail = async button => {
      if (!drawer || !body) return;
      const url = button.dataset.kubernetesDetailUrl;
      if (!url) return;
      title.textContent = button.closest("tr,article")?.querySelector("strong")?.textContent?.trim() || "Workload";
      meta.textContent = "";
      body.innerHTML = `<div class="kubernetes-drawer__loading"><span data-lucide="loader-circle" aria-hidden="true"></span>${escapeMarkup(kubernetesWords().loading)}</div>`;
      renderIcons(body);
      if (!drawer.open) drawer.showModal();
      try {
        const detailResponse = await fetch(url, { headers: { "Accept": "application/json" } });
        if (!detailResponse.ok) throw new Error(await detailResponse.text() || `HTTP ${detailResponse.status}`);
        const payload = await detailResponse.json();
        const workload = payload?.Workload || {};
        title.textContent = workload.Name || title.textContent;
        meta.textContent = [workload.Namespace, workload.Kind, workload.Image].filter(Boolean).join(" · ");
        body.innerHTML = renderKubernetesDetail(payload, button, root);
        renderIcons(body);
      } catch (error) {
        body.innerHTML = `<div class="kubernetes-detail-empty" role="alert">${escapeMarkup(error?.message || kubernetesWords().loadFailed)}</div>`;
      }
    };
    const onClick = event => {
      const snapshotLink = event.target.closest(".kubernetes-status-tabs a,.kubernetes-sort-link");
      if (snapshotLink && event.button === 0 && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) {
        event.preventDefault();
        const scrollX = window.scrollX, scrollY = window.scrollY;
        snapshotController?.abort(); snapshotController = new AbortController();
        root.setAttribute("aria-busy", "true");
        fetchDocument(snapshotLink.href, { cache: "no-store", signal: snapshotController.signal }).then(({response, document: page}) => {
          if (!response.ok) throw new Error(`HTTP ${response.status}`);
          const incoming = page?.querySelector("[data-kubernetes-page]");
          if (!incoming) throw new Error("Kubernetes snapshot was not present in the response.");
          for (const selector of [".kubernetes-status-tabs", ".kubernetes-toolbar", ".kubernetes-table-shell"]) {
            const current = root.querySelector(selector), next = incoming.querySelector(selector);
            if (current && next) current.replaceWith(next);
          }
          history.pushState({ kubernetesMonitor: true }, "", snapshotLink.href);
          renderIcons(root); window.requestAnimationFrame(() => window.scrollTo(scrollX, scrollY));
        }).catch(error => { if (error?.name !== "AbortError") console.error(error); }).finally(() => root.removeAttribute("aria-busy"));
        return;
      }
      const detail = event.target.closest("[data-kubernetes-detail-url]");
      if (detail) {
        event.preventDefault();
        openDetail(detail);
        return;
      }
      if (event.target.closest("[data-kubernetes-import-open]")) {
        if (importDrawer && !importDrawer.open) importDrawer.showModal();
        return;
      }
      if (event.target.closest("[data-kubernetes-import-close]")) {
        importDrawer?.close();
        return;
      }
      const editContext = event.target.closest("[data-kubernetes-context-edit]");
      if (editContext) {
        const row = editContext.closest("[data-kubernetes-context-row]");
        const name = row?.dataset.contextName || "";
        const action = `/monitor/kubernetes/local/contexts/${encodeURIComponent(name)}`;
        if (contextUpdateForm) {
          contextUpdateForm.action = action;
          contextUpdateForm.elements.cluster.value = row?.dataset.contextCluster || "";
          contextUpdateForm.elements.user.value = row?.dataset.contextUser || "";
          contextUpdateForm.elements.namespace.value = row?.dataset.contextNamespace || "";
        }
        if (contextRenameForm) {
          contextRenameForm.action = action;
          contextRenameForm.elements.name.value = name;
        }
        const contextTitle = contextDrawer?.querySelector("[data-kubernetes-context-title]");
        if (contextTitle) contextTitle.textContent = name;
        if (contextDrawer && !contextDrawer.open) contextDrawer.showModal();
        return;
      }
      if (event.target.closest("[data-kubernetes-context-close]")) {
        contextDrawer?.close();
        return;
      }
      if (event.target.closest("[data-kubernetes-drawer-close]")) drawer?.close();
    };
    const onDrawerClick = event => {
      if (event.target === drawer) drawer.close();
    };
    root.addEventListener("click", onClick);
    const onImportDrawerClick = event => { if (event.target === importDrawer) importDrawer.close(); };
    const onContextDrawerClick = event => { if (event.target === contextDrawer) contextDrawer.close(); };
    const onImportFile = async () => {
      const file = importFile?.files?.[0];
      if (!file || !importForm || !importPreview) return;
      const filename = importForm.querySelector("[data-kubernetes-import-filename]");
      const error = importForm.querySelector("[data-kubernetes-import-error]");
      if (filename) filename.textContent = file.name;
      if (error) error.hidden = true;
      importPreview.hidden = true;
      try {
        const response = await fetch(local.dataset.importPreviewUrl, {method: "POST", body: new FormData(importForm), headers: {"Accept": "application/json"}});
        if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
        const payload = await response.json();
        importPreview.querySelector("[data-import-clusters]").textContent = payload.clusters ?? 0;
        importPreview.querySelector("[data-import-users]").textContent = payload.users ?? 0;
        importPreview.querySelector("[data-import-contexts]").textContent = payload.contexts ?? 0;
        const conflicts = Array.isArray(payload.conflicts) ? payload.conflicts : [];
        importPreview.querySelector("[data-import-conflicts]").textContent = conflicts.length ? `${conflicts.length} conflict(s): ${conflicts.join(", ")}` : "No name conflicts.";
        importPreview.hidden = false;
      } catch (failure) {
        if (error) { error.textContent = failure?.message || "Unable to inspect kubeconfig."; error.hidden = false; }
      }
    };
    const onContextSearch = () => {
      const query = (contextSearch?.value || "").trim().toLocaleLowerCase();
      local?.querySelectorAll("[data-kubernetes-context-row]").forEach(row => {
        row.hidden = query !== "" && ![row.dataset.contextName, row.dataset.contextCluster, row.dataset.contextUser, row.dataset.contextNamespace].some(value => (value || "").toLocaleLowerCase().includes(query));
      });
    };
    const onClusterChange = () => clusterSelect?.form?.requestSubmit();
    clusterSelect?.addEventListener("change", onClusterChange);
    drawer?.addEventListener("click", onDrawerClick);
    importDrawer?.addEventListener("click", onImportDrawerClick);
    contextDrawer?.addEventListener("click", onContextDrawerClick);
    importFile?.addEventListener("change", onImportFile);
    contextSearch?.addEventListener("input", onContextSearch);
    cleanups.push(() => {
      snapshotController?.abort();
      root.removeEventListener("click", onClick);
      clusterSelect?.removeEventListener("change", onClusterChange);
      drawer?.removeEventListener("click", onDrawerClick);
      importDrawer?.removeEventListener("click", onImportDrawerClick);
      contextDrawer?.removeEventListener("click", onContextDrawerClick);
      importFile?.removeEventListener("change", onImportFile);
      contextSearch?.removeEventListener("input", onContextSearch);
      if (drawer?.open) drawer.close();
      if (importDrawer?.open) importDrawer.close();
      if (contextDrawer?.open) contextDrawer.close();
    });
  }

  function initKubernetesConnection(cleanups) {
    const root = document.querySelector("[data-kubernetes-connection-page]");
    const form = root?.querySelector("[data-kubernetes-connection-form]");
    const button = root?.querySelector("[data-kubernetes-test]");
    const result = root?.querySelector("[data-kubernetes-test-result]");
    if (!form || !button || !result) return;
    const message = result.querySelector("[data-kubernetes-test-message]");
    const onClick = async () => {
      button.disabled = true;
      button.setAttribute("aria-busy", "true");
      result.hidden = false;
      if (message) message.textContent = kubernetesWords().testing;
      try {
        const response = await fetch("/monitor/kubernetes/connections/test", { method: "POST", body: new FormData(form), headers: { "Accept": "application/json" } });
        const payload = await response.json();
        if (!response.ok) throw new Error(payload.error || `HTTP ${response.status}`);
        const capabilities = payload.capabilities || {};
        result.querySelectorAll("[data-capability]").forEach(item => {
          const supported = capabilities[item.dataset.capability] === true;
          item.dataset.supported = supported ? "true" : "false";
          item.querySelector("svg")?.replaceWith(makeIcon(supported ? "check" : "minus"));
        });
        if (message) message.textContent = kubernetesWords().connected;
      } catch (error) {
        result.querySelectorAll("[data-capability]").forEach(item => item.dataset.supported = "false");
        if (message) message.textContent = error?.message || kubernetesWords().testFailed;
      } finally {
        button.disabled = false;
        button.removeAttribute("aria-busy");
      }
    };
    button.addEventListener("click", onClick);
    cleanups.push(() => button.removeEventListener("click", onClick));
  }

  function initPage(options = {}) {
    const cleanups = [];
    cleanupPage = () => cleanups.splice(0).forEach(cleanup => cleanup());
    renderIcons();
    applySidebarCollapsed(readSidebarCollapsed());
    localizeTimes();
    initMarkdownPreview();
    initScriptPreview();
    initTextPreviewPager(cleanups);
    initPasswordControls(document, cleanups);
    initCopyControls(document, cleanups);
    initFileDropUpload(document, cleanups);
    initFileVisibilityToggle(document, cleanups);
    initFileQuickAccess(document, cleanups, options.openFileQuickAccess === true);
	initFileOperation(cleanups);
    initDirectoryPickers(document, cleanups);
    initQuickCreateDefaults(document, cleanups);
    initOverview(cleanups);
    initApplications(cleanups);
    initContainers(cleanups);
    initKubernetes(cleanups);
    initKubernetesConnection(cleanups);
    initLiveLog(cleanups);
    initRun(cleanups);
    initGroupedRecords(cleanups);
    initScheduleCron(cleanups);
    initExternalEntryForm(document, cleanups);
    initDisplaySettings(cleanups);
	initSecurityDialogs(cleanups);
	initMySQLDrawers(cleanups);
    initExternalKeyManagers(cleanups);
    initStateBackupDrawers(cleanups);
    initSecurityBanDrawer(cleanups);
    initUpdateSourceDrawer(cleanups);
    initAssistantWorkspace(cleanups);
    initAssistantSettings(cleanups);
    const websiteForm = document.querySelector("[data-website-monitor-form]");
    if (websiteForm) cleanups.push(initWebsiteMonitorForm(websiteForm));
    const websiteMonitoring = document.querySelector("[data-website-monitoring],[data-website-detail]");
    if (websiteMonitoring) cleanups.push(initWebsiteMonitoring(websiteMonitoring));
    const websiteNginx = document.querySelector("[data-website-nginx]");
    if (websiteNginx) cleanups.push(initWebsiteNginx(websiteNginx));
    const websiteConfigTransfer = document.querySelector("[data-website-config-transfer]");
    if (websiteConfigTransfer) cleanups.push(initWebsiteConfigTransfer(websiteConfigTransfer));
  }

  document.addEventListener("click", event => {
    const sidebarCollapse = event.target.closest("[data-sidebar-collapse]");
    if (sidebarCollapse) {
      applySidebarCollapsed(!document.body.classList.contains("sidebar-collapsed"), true);
      return;
    }
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
    let link = event.target.closest("a[href]");
    if (!link && !event.target.closest("button,input,select,textarea,form,label,[role='button']")) {
      link = event.target.closest("[data-external-entry-row]")?.querySelector("a[data-task-link]") || null;
    }
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
      const securityTab = link.matches("[data-security-tabs] a");
      if (securityTab) {
        link.closest("[data-security-tabs]")?.querySelectorAll("a[aria-current]").forEach(tab => tab.removeAttribute("aria-current"));
        link.setAttribute("aria-current", "page");
        link.setAttribute("aria-busy", "true");
      }
      navigate(destination.href, true, {
        deferredData: isDeferredDataURL(destination.href),
        immediate: mainNavigation || securityTab,
        title: mainNavigation ? navigationTitle(link) : undefined,
        focusSelector: link.dataset.focusAfterNavigation,
        preserveScroll: link.hasAttribute("data-preserve-scroll"),
        openFileQuickAccess: mainNavigation && destination.pathname === "/resources/files",
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
    const submitter = event.submitter || (form.matches("[data-file-search]") ? form.querySelector("[data-search-submit]") : null);
    if (confirmFormSubmission(event, form, submitter)) return;
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
    } else if (form.hasAttribute("data-connection-test")) {
      event.preventDefault();
      submitConnectionTest(form, submitter);
    } else if (form.hasAttribute("data-file-upload-form")) {
      event.preventDefault();
      submitFileUpload(form);
    } else if (form.hasAttribute("data-update-apply")) {
      event.preventDefault();
      submitUpdateApply(form, submitter);
    } else if (form.hasAttribute("data-service-restart")) {
      event.preventDefault();
      submitServiceRestart(form, submitter);
    } else if (form.hasAttribute("data-async")) {
      event.preventDefault();
      submitAsync(form, submitter);
    } else if (form.method.toLowerCase() === "post" &&
        document.querySelector("[data-app-shell]") &&
        !form.hasAttribute("data-native") &&
        new URL(form.action || location.href, location.href).origin === location.origin &&
        new URL(form.action || location.href, location.href).pathname !== "/logout") {
      event.preventDefault();
      submitAsync(form, submitter, { fullNavigationOnSuccess: true });
    }
  });

  document.addEventListener("keydown", event => {
    const editing = event.target.matches("input,textarea,select,[contenteditable='true']");
    if (event.key === "Escape" && activeFileConflictDialog) return;
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
    if (typeof event.key === "string" && event.key.toLowerCase() === "p" && !editing) {
      const run = document.querySelector("[data-run-events-url]");
      if (run?._toggleLogPause) {
        event.preventDefault();
        run._toggleLogPause();
      }
    }
  });

  window.addEventListener("popstate", event => {
    if (taskPanelHistoryClosePending) {
      taskPanelHistoryClosePending = false;
      if (taskPanelGroupRefreshAfterClose) {
        const refresh = taskPanelGroupRefreshAfterClose;
        taskPanelGroupRefreshAfterClose = null;
        refreshExternalGroup(refresh.groupID, { reopenManager: refresh.reopenManager });
      } else if (taskPanelRefreshAfterCloseURL) {
        const refreshURL = taskPanelRefreshAfterCloseURL;
        taskPanelRefreshAfterCloseURL = "";
        navigate(refreshURL, false);
      }
      return;
    }
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

document.addEventListener("change", function (event) {
  const select = event.target.closest("[data-dashboard-card-type]");
  if (!select) return;
  const form = select.closest("form");
  if (!form) return;
  const type = select.value;
  const website = type === "website";
  const registry = type === "registry";
  form.querySelectorAll("[data-dashboard-http-field]").forEach((field) => { field.hidden = website || registry; });
  form.querySelectorAll("[data-dashboard-refresh-field]").forEach((field) => { field.hidden = website; });
  form.querySelectorAll("[data-dashboard-card-types]").forEach((field) => {
    field.hidden = !field.dataset.dashboardCardTypes.split(",").includes(type);
    field.querySelectorAll("[data-dashboard-expression-required]").forEach((input) => {
      input.required = !field.hidden;
    });
  });
  const websiteField = form.querySelector("[data-dashboard-website-field]");
  if (websiteField) websiteField.hidden = !website;
  const registryField = form.querySelector("[data-dashboard-registry-field]");
  if (registryField) {
    registryField.hidden = !registry;
    registryField.querySelectorAll("input, textarea, select").forEach((input) => {
      input.disabled = !registry;
      if (input.name === "registry_endpoint" || input.name === "registry_images") input.required = registry;
    });
  }
  const valueLabel = form.querySelector("[data-dashboard-value-expression-label]");
  const valueInput = form.querySelector("[data-dashboard-value-expression-input]");
  const valueHint = form.querySelector("[data-dashboard-value-expression-hint]");
  const labels = { number: "取值表达式", percentage: "百分比表达式", quota: "已用额度表达式" };
  const placeholders = { number: "data.value", percentage: "data.used / data.total * 100", quota: "data.used" };
  const hints = { number: "字符串使用 JSON 路径；数字支持路径与 + − × ÷、括号运算", percentage: "支持 JSON 路径与 + − × ÷、括号运算", quota: "支持 JSON 路径与 + − × ÷、括号运算" };
  if (valueLabel) valueLabel.textContent = labels[type] || "取值表达式";
  if (valueInput) valueInput.placeholder = placeholders[type] || "data.value";
  if (valueHint) valueHint.textContent = hints[type] || hints.number;
  form.querySelectorAll("[data-dashboard-card-preview]").forEach((preview) => {
    preview.hidden = preview.dataset.dashboardCardPreview !== type;
  });
  const testButton = form.querySelector("[data-dashboard-test-request]");
  if (testButton) testButton.hidden = website;
  const testWorkbench = form.querySelector("[data-dashboard-test-workbench]");
  if (testWorkbench) testWorkbench.hidden = website;
  const previewLabel = form.querySelector("[data-dashboard-preview-label]");
  if (previewLabel) previewLabel.textContent = ({ number: "数值", percentage: "百分比", quota: "额度", website: "网站状态", registry: "镜像版本" })[type] || "数值";
});

document.addEventListener("change", function (event) {
  const auth = event.target.closest("[data-dashboard-registry-auth]");
  if (!auth) return;
  const credentials = auth.closest("form")?.querySelector("[data-dashboard-registry-credentials]");
  if (!credentials) return;
  credentials.hidden = auth.value !== "basic";
  credentials.querySelectorAll("input").forEach((input) => {
    input.required = auth.value === "basic" && input.name === "registry_username";
  });
});

document.querySelectorAll("[data-dashboard-card-type]:checked").forEach((input) => {
  input.dispatchEvent(new Event("change", { bubbles: true }));
});
document.querySelectorAll("[data-dashboard-registry-auth]").forEach((input) => {
  input.dispatchEvent(new Event("change", { bubbles: true }));
});

document.addEventListener("input", function (event) {
  const input = event.target.closest("[data-dashboard-slug-input]");
  if (!input) return;
  const preview = input.closest("label")?.querySelector("[data-dashboard-slug-preview]");
  if (!preview) return;
  preview.textContent = "公开后生成不可猜链接";
});

function openDashboardDrawer(drawer) {
	if (!drawer) return;
	clearDashboardDrawerCloseWait(drawer);
	drawer._dashboardReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
	drawer.classList.remove("is-closing");
	drawer.classList.add("is-opening");
	drawer.open = true;
	syncDashboardDrawerState();
	// Commit the off-canvas start state, then enter on the next paint.
	drawer.querySelector(".custom-dashboard-drawer")?.getBoundingClientRect();
	window.requestAnimationFrame(() => {
		if (drawer.open && !drawer.classList.contains("is-closing")) drawer.classList.remove("is-opening");
	});
}

function clearDashboardDrawerCloseWait(drawer) {
	if (!drawer) return;
	if (drawer._dashboardCloseTimer) window.clearTimeout(drawer._dashboardCloseTimer);
	drawer._dashboardCloseTimer = null;
	if (drawer._dashboardCloseTransition) {
		const { panel, finish } = drawer._dashboardCloseTransition;
		panel.removeEventListener("transitionend", finish);
		drawer._dashboardCloseTransition = null;
	}
}

function closeDashboardDrawer(drawer, returnFocus = true) {
	if (!drawer?.open || drawer.classList.contains("is-closing")) return;
	clearDashboardDrawerCloseWait(drawer);
	const panel = drawer.querySelector(".custom-dashboard-drawer");
	drawer.classList.remove("is-opening");
	drawer.classList.add("is-closing");
	const complete = () => {
		clearDashboardDrawerCloseWait(drawer);
		drawer.open = false;
		drawer.classList.remove("is-closing");
		syncDashboardDrawerState();
		if (returnFocus) {
			const fallback = drawer.querySelector(":scope > summary");
			const target = drawer._dashboardReturnFocus?.isConnected ? drawer._dashboardReturnFocus : fallback;
			target?.focus();
		}
		drawer._dashboardReturnFocus = null;
	};
	if (!panel || window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
		complete();
		return;
	}
	const finish = (event) => {
		if (event.target !== panel || event.propertyName !== "transform") return;
		complete();
	};
	panel.addEventListener("transitionend", finish);
	drawer._dashboardCloseTransition = { panel, finish };
	// A fallback only covers removed/disabled transitions; normal completion follows CSS.
	drawer._dashboardCloseTimer = window.setTimeout(complete, 600);
}

function syncDashboardDrawerState() {
  document.body.classList.toggle("has-custom-dashboard-drawer", Boolean(document.querySelector("[data-dashboard-drawer][open]")));
}

function openDashboardCardRow(row) {
  const trigger = row?.querySelector("[data-dashboard-card-edit]");
  if (!trigger) return;
  trigger.click();
}

document.addEventListener("click", function (event) {
	const summary = event.target.closest("summary");
	const summaryDrawer = summary?.parentElement;
	if (summaryDrawer?.matches("[data-dashboard-drawer]")) {
		event.preventDefault();
		if (summaryDrawer.classList.contains("is-closing")) openDashboardDrawer(summaryDrawer);
		else if (summaryDrawer.open) closeDashboardDrawer(summaryDrawer);
		else openDashboardDrawer(summaryDrawer);
		return;
	}
	const drawerButton = event.target.closest("[data-dashboard-open-drawer]");
	if (drawerButton) {
		const name = drawerButton.dataset.dashboardOpenDrawer;
		const drawer = document.querySelector(`[data-dashboard-drawer-name="${CSS.escape(name)}"]`);
		openDashboardDrawer(drawer);
		return;
	}
  const deleteButton = event.target.closest("[data-dashboard-delete-open]");
	if (deleteButton) {
		const drawer = document.querySelector("[data-dashboard-delete-drawer]");
		openDashboardDrawer(drawer);
		return;
	}
  const row = event.target.closest("[data-dashboard-card-row]");
  if (!row || event.target.closest("a,button,input,select,textarea,summary,details,form,label")) return;
  openDashboardCardRow(row);
});

document.addEventListener("keydown", function (event) {
  const row = event.target.closest?.("[data-dashboard-card-row]");
  if (!row || event.target !== row || (event.key !== "Enter" && event.key !== " ")) return;
  event.preventDefault();
  openDashboardCardRow(row);
});

document.addEventListener("toggle", function (event) {
  const drawer = event.target.closest?.("[data-dashboard-drawer]");
  if (!drawer) return;
	if (drawer.open && !drawer.classList.contains("is-closing")) {
    document.querySelectorAll("[data-dashboard-drawer][open]").forEach((other) => {
      if (other !== drawer) closeDashboardDrawer(other, false);
    });
    window.requestAnimationFrame(() => {
      const focusTarget = drawer.querySelector(".custom-dashboard-drawer input:not([type=hidden]), .custom-dashboard-drawer button:not([disabled]), .custom-dashboard-drawer a[href]");
      focusTarget?.focus();
    });
  }
  syncDashboardDrawerState();
}, true);

document.addEventListener("click", function (event) {
  const close = event.target.closest("[data-dashboard-drawer-close]");
  if (!close) return;
  const drawer = close.closest("[data-dashboard-drawer]");
  if (!drawer) return;
  event.preventDefault();
  closeDashboardDrawer(drawer);
  syncDashboardDrawerState();
}, true);

document.addEventListener("keydown", function (event) {
  const drawer = document.querySelector("[data-dashboard-drawer][open]");
  if (!drawer) return;
  if (event.key === "Escape") {
    event.preventDefault();
    closeDashboardDrawer(drawer);
    syncDashboardDrawerState();
    return;
  }
  if (event.key !== "Tab") return;
  const focusable = Array.from(drawer.querySelectorAll(".custom-dashboard-drawer a[href], .custom-dashboard-drawer button:not([disabled]), .custom-dashboard-drawer input:not([disabled]), .custom-dashboard-drawer textarea:not([disabled]), .custom-dashboard-drawer select:not([disabled])")).filter((element) => element.offsetParent !== null);
  if (!focusable.length) return;
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
});

document.addEventListener("click", async function (event) {
  const button = event.target.closest("[data-copy-dashboard-url]");
  if (!button) return;
  const label = button.querySelector("[data-copy-dashboard-label]");
  try {
    await window.ScriptBoardCopyText(new URL(button.dataset.copyDashboardUrl, window.location.origin).href);
    if (label) label.textContent = "已复制";
  } catch (_) {
    if (label) label.textContent = "复制失败";
  }
  window.setTimeout(() => { if (label) label.textContent = "复制公开地址"; }, 1800);
});

syncDashboardDrawerState();

function syncDashboardCardSelection(root) {
  if (!root) return;
  const checkboxes = Array.from(root.querySelectorAll('input[name="selection"]'));
  const selected = checkboxes.filter((checkbox) => checkbox.checked).length;
  const count = root.querySelector("[data-dashboard-selection-count]");
  const submit = root.querySelector("[data-dashboard-selection-submit]");
  if (count) count.textContent = `${selected} 个节点已选择`;
  if (submit) submit.disabled = selected === 0;
}

function dashboardTransferTypeLabel(type) {
  return ({ number: "数值", percentage: "百分比", quota: "额度", website: "网站状态", key_value: "数据" })[type] || "未知类型";
}

async function renderDashboardImportSelection(input) {
  const form = input.closest("[data-dashboard-import-form]");
  const filename = form?.querySelector("[data-dashboard-import-filename]");
  const selection = form?.querySelector("[data-dashboard-import-selection]");
  const selectionPresent = form?.querySelector("[data-dashboard-import-selection-present]");
  const list = form?.querySelector("[data-dashboard-import-card-list]");
  const error = form?.querySelector("[data-dashboard-import-file-error]");
  const file = input.files?.[0];
  if (filename) filename.textContent = file?.name || "选择节点配置文件";
  if (selection) selection.hidden = true;
  if (selectionPresent) selectionPresent.disabled = true;
  if (list) list.replaceChildren();
  if (error) error.hidden = true;
  input.setCustomValidity("");
  if (!file || !form || !selection || !selectionPresent || !list) {
    syncDashboardCardSelection(form);
    return;
  }
  try {
    const bundle = JSON.parse(await file.text());
    const nodes = bundle?.nodes;
    if (!Array.isArray(nodes) || nodes.length === 0 || nodes.length > 100) throw new Error("invalid nodes");
    nodes.forEach((node, index) => {
      const item = document.createElement("label");
      const checkbox = document.createElement("input");
      const text = document.createElement("span");
      const name = document.createElement("strong");
      const type = document.createElement("small");
      checkbox.type = "checkbox";
      checkbox.name = "selection";
      checkbox.value = String(index);
      checkbox.checked = true;
      name.textContent = String(node?.name || `节点 ${index + 1}`);
      type.textContent = dashboardTransferTypeLabel(node?.type);
      text.append(name, type);
      item.append(checkbox, text);
      list.append(item);
    });
    selection.hidden = false;
    selectionPresent.disabled = false;
  } catch (_) {
    input.setCustomValidity("无法读取节点清单，请选择由 ScriptBoard 导出的 JSON 文件。");
    if (error) {
      error.hidden = false;
      const message = error.querySelector("span:last-child");
      if (message) message.textContent = input.validationMessage;
    }
  }
  syncDashboardCardSelection(form);
}

document.addEventListener("change", function (event) {
  const importInput = event.target.closest('[data-dashboard-import-file] input[type="file"]');
  if (importInput) {
    renderDashboardImportSelection(importInput);
    return;
  }
  const selection = event.target.closest('[data-dashboard-card-selection] input[name="selection"]');
  if (selection) syncDashboardCardSelection(selection.closest("[data-dashboard-card-selection]"));
});

document.addEventListener("click", function (event) {
  const control = event.target.closest("[data-dashboard-selection-all],[data-dashboard-selection-none]");
  if (!control) return;
  const root = control.closest("[data-dashboard-card-selection]");
  const checked = control.hasAttribute("data-dashboard-selection-all");
  root?.querySelectorAll('input[name="selection"]').forEach((checkbox) => { checkbox.checked = checked; });
  syncDashboardCardSelection(root);
});

document.querySelectorAll("[data-dashboard-card-selection]").forEach(syncDashboardCardSelection);

function dashboardJSONPath(documentValue, path) {
  const tokens = String(path || "").trim().match(/[^.[\]]+|\[(\d+)\]/g) || [];
  let current = documentValue;
  for (const token of tokens) {
    const key = token[0] === "[" ? Number(token.slice(1, -1)) : token;
    if (current == null || !Object.prototype.hasOwnProperty.call(current, key)) throw new Error("missing");
    current = current[key];
  }
  return current;
}

function dashboardLocaleText(zh, en) {
  return document.documentElement.lang.toLowerCase().startsWith("zh") ? zh : en;
}

function dashboardExpressionValue(documentValue, expressionText) {
  const source = String(expressionText || "").trim();
  if (!/[+*/()\-]/.test(source)) return dashboardJSONPath(documentValue, source);
  const tokens = source.match(/\s*(\d+(?:\.\d+)?|[A-Za-z_$][\w$]*(?:\[\d+\]|\.[A-Za-z_$][\w$]*)*|[()+*/-])\s*/g);
  if (!tokens || tokens.join("").replace(/\s/g, "") !== source.replace(/\s/g, "")) throw new Error("invalid");
  let index = 0;
  const factor = () => {
    const token = tokens[index++]?.trim();
    if (token === "(") { const value = expression(); if (tokens[index++]?.trim() !== ")") throw new Error("invalid"); return value; }
    if (token === "+" || token === "-") { const value = factor(); return token === "-" ? -value : value; }
    const number = Number(token);
    if (token !== "" && Number.isFinite(number)) return number;
    const value = dashboardJSONPath(documentValue, token);
    if (typeof value !== "number") throw new Error("type");
    return value;
  };
  const term = () => { let value = factor(); while (["*", "/"].includes(tokens[index]?.trim())) { const op = tokens[index++].trim(); const right = factor(); value = op === "*" ? value * right : value / right; } return value; };
  const expression = () => { let value = term(); while (["+", "-"].includes(tokens[index]?.trim())) { const op = tokens[index++].trim(); const right = term(); value = op === "+" ? value + right : value - right; } return value; };
  const value = expression();
  if (index !== tokens.length || !Number.isFinite(value)) throw new Error("invalid");
  return value;
}

function dashboardValueType(value) {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}

function updateDashboardTestPreview(form) {
  const panel = form.querySelector("[data-dashboard-test-result]");
  if (!panel?._testDocument) return;
  const expression = form.querySelector('[name="value_path"]')?.value || "";
  try {
    const value = dashboardExpressionValue(panel._testDocument, expression);
    panel.querySelector("[data-dashboard-test-value]").textContent = typeof value === "string" ? value : JSON.stringify(value);
    panel.querySelector("[data-dashboard-test-type]").textContent = dashboardValueType(value);
  } catch (_) {
    panel.querySelector("[data-dashboard-test-value]").textContent = dashboardLocaleText("未匹配", "No match");
    panel.querySelector("[data-dashboard-test-type]").textContent = "—";
  }
}

async function copyDashboardFieldPath(path) {
  await window.ScriptBoardCopyText(path);
}

function renderDashboardJSONTree(root, value, path = "", search = "") {
  root.replaceChildren();
  const append = (parent, current, currentPath, label) => {
    const isContainer = current !== null && typeof current === "object";
    const searchable = `${label} ${currentPath}`.toLowerCase();
    if (search && !searchable.includes(search) && !JSON.stringify(current).toLowerCase().includes(search)) return;
    if (isContainer) {
      const details = document.createElement("details");
      details.open = currentPath.split(/[.[]/).length < 3;
      const summary = document.createElement("summary");
      summary.textContent = `${label} ${Array.isArray(current) ? `[${current.length}]` : `{${Object.keys(current).length}}`}`;
      details.append(summary);
      const children = document.createElement("div");
      Object.entries(current).forEach(([key, child]) => append(children, child, currentPath ? (Array.isArray(current) ? `${currentPath}[${key}]` : `${currentPath}.${key}`) : key, key));
      details.append(children);
      parent.append(details);
      return;
    }
    const row = document.createElement("div");
    row.className = "custom-dashboard-json-leaf";
    const key = document.createElement("code"); key.textContent = label;
    const preview = document.createElement("span"); preview.textContent = typeof current === "string" ? current : JSON.stringify(current);
    const type = document.createElement("small"); type.textContent = dashboardValueType(current);
    const copy = document.createElement("button");
    copy.type = "button";
    copy.className = "icon-button icon-button--quiet custom-dashboard-json-copy";
    const copyLabel = dashboardLocaleText(`复制字段路径：${currentPath}`, `Copy field path: ${currentPath}`);
    const copiedLabel = dashboardLocaleText(`已复制：${currentPath}`, `Copied: ${currentPath}`);
    const failedLabel = dashboardLocaleText(`复制失败：${currentPath}`, `Copy failed: ${currentPath}`);
    copy.setAttribute("aria-label", copyLabel);
    const renderCopyIcon = (name) => {
      const icon = document.createElement("span");
      icon.dataset.lucide = name;
      icon.setAttribute("aria-hidden", "true");
      copy.replaceChildren(icon);
      window.ScriptBoardRenderIcons?.(copy);
    };
    renderCopyIcon("copy");
    copy.addEventListener("click", async () => {
      window.clearTimeout(copy._dashboardCopyTimer);
      try {
        await copyDashboardFieldPath(currentPath);
        copy.dataset.state = "success";
        copy.setAttribute("aria-label", copiedLabel);
        renderCopyIcon("check");
      } catch (_) {
        copy.dataset.state = "error";
        copy.setAttribute("aria-label", failedLabel);
        renderCopyIcon("triangle-alert");
      }
      copy._dashboardCopyTimer = window.setTimeout(() => {
        delete copy.dataset.state;
        copy.setAttribute("aria-label", copyLabel);
        renderCopyIcon("copy");
      }, 1800);
    });
    row.append(key, preview, type, copy); parent.append(row);
  };
  append(root, value, path, path || "root");
}

async function testDashboardCardRequest(button) {
  const form = button.form;
  const panel = form?.querySelector("[data-dashboard-test-result]");
  if (!form || !panel || !form.reportValidity()) return;
  button.disabled = true;
  panel.hidden = false;
  panel.querySelector("[data-dashboard-test-summary]").textContent = dashboardLocaleText("正在请求…", "Requesting…");
  try {
    const response = await fetch(button.formAction, { method: "POST", body: new FormData(form), headers: { Accept: "application/json" }, credentials: "same-origin" });
    if (!response.ok) throw new Error(await response.text());
    const result = await response.json();
    const diagnostic = result.diagnostic || {};
    panel.querySelector("[data-dashboard-test-summary]").textContent = result.ok ? dashboardLocaleText("请求成功", "Request succeeded") : (diagnostic.summary || dashboardLocaleText("请求失败", "Request failed"));
    panel.querySelector("[data-dashboard-test-metrics]").textContent = `${diagnostic.httpStatus || "—"} · ${diagnostic.durationMs || 0} ms · ${diagnostic.responseBytes || 0} B · ${diagnostic.contentType || "—"}`;
    panel._testDocument = result.document || null;
    const search = panel.querySelector("[data-dashboard-json-search]");
    const tree = panel.querySelector("[data-dashboard-json-tree]");
    const rerender = () => result.document ? renderDashboardJSONTree(tree, result.document, "", search.value.trim().toLowerCase()) : tree.replaceChildren(document.createTextNode(dashboardLocaleText("当前响应没有可用的 JSON 结构。", "This response has no usable JSON structure.")));
    rerender(); search.oninput = rerender;
    panel.querySelector("[data-dashboard-raw-response]").textContent = result.rawResponse || "";
    panel.querySelector("[data-dashboard-raw-note]").textContent = `${diagnostic.code === "non_json" ? dashboardLocaleText("当前卡片取值要求响应为 JSON。", "This card requires a JSON response.") : ""}${result.rawTruncated ? dashboardLocaleText(" 原始响应已截断至 256 KB。", " The raw response was truncated to 256 KB.") : ""}`.trim();
    const requestInfo = panel.querySelector("[data-dashboard-request-info]"); requestInfo.replaceChildren();
    const entries = [[dashboardLocaleText("地址", "URL"), diagnostic.url || "—"], [dashboardLocaleText("阶段", "Stage"), diagnostic.stage || "—"], ...Object.entries(result.requestHeaders || {}).map(([name, value]) => [`${dashboardLocaleText("请求头", "Header")} · ${name}`, value])];
    entries.forEach(([name, value]) => { const row = document.createElement("div"); const dt = document.createElement("dt"); const dd = document.createElement("dd"); dt.textContent = name; dd.textContent = value; row.append(dt, dd); requestInfo.append(row); });
    updateDashboardTestPreview(form);
  } catch (error) {
    panel.querySelector("[data-dashboard-test-summary]").textContent = String(error.message || error).trim() || dashboardLocaleText("测试请求失败", "Test request failed");
  } finally {
    button.disabled = false;
  }
}

document.addEventListener("click", function (event) {
  const testButton = event.target.closest("[data-dashboard-test-request]");
  if (testButton) { event.preventDefault(); testDashboardCardRequest(testButton); return; }
  const tab = event.target.closest("[data-dashboard-test-tab]");
  if (!tab) return;
  const panel = tab.closest("[data-dashboard-test-result]");
  panel.querySelectorAll("[data-dashboard-test-tab]").forEach((item) => item.setAttribute("aria-pressed", String(item === tab)));
  panel.querySelectorAll("[data-dashboard-test-pane]").forEach((pane) => { pane.hidden = pane.dataset.dashboardTestPane !== tab.dataset.dashboardTestTab; });
});

document.addEventListener("input", function (event) {
  if (event.target.matches('.custom-dashboard-card-form [name="value_path"], .custom-dashboard-form-grid [name="value_path"]')) updateDashboardTestPreview(event.target.form);
});
(() => {
  const decodeBase64URL = (value) => {
    const normalized = String(value).replace(/-/g, "+").replace(/_/g, "/");
    const binary = atob(normalized + "=".repeat((4 - normalized.length % 4) % 4));
    return Uint8Array.from(binary, (character) => character.charCodeAt(0));
  };
  const encodeBase64URL = (value) => {
    if (value == null) return null;
    const bytes = new Uint8Array(value);
    let binary = "";
    bytes.forEach((byte) => { binary += String.fromCharCode(byte); });
    return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  };
  const decodePublicKey = (wrapper) => {
    const options = structuredClone(wrapper.publicKey || wrapper);
    options.challenge = decodeBase64URL(options.challenge);
    if (options.user?.id) options.user.id = decodeBase64URL(options.user.id);
    ["allowCredentials", "excludeCredentials"].forEach((field) => {
      if (Array.isArray(options[field])) options[field].forEach((item) => { item.id = decodeBase64URL(item.id); });
    });
    return options;
  };
  const credentialJSON = (credential) => {
    const response = {
      clientDataJSON: encodeBase64URL(credential.response.clientDataJSON)
    };
    if (credential.response.attestationObject) {
      response.attestationObject = encodeBase64URL(credential.response.attestationObject);
      response.transports = credential.response.getTransports?.() || [];
    } else {
      response.authenticatorData = encodeBase64URL(credential.response.authenticatorData);
      response.signature = encodeBase64URL(credential.response.signature);
      response.userHandle = encodeBase64URL(credential.response.userHandle);
    }
    return {
      id: credential.id,
      rawId: encodeBase64URL(credential.rawId),
      type: credential.type,
      authenticatorAttachment: credential.authenticatorAttachment,
      clientExtensionResults: credential.getClientExtensionResults(),
      response
    };
  };
  const messageFor = (error) => error?.name === "NotAllowedError"
    ? (document.documentElement.lang === "zh-CN" ? "认证器未确认操作或请求已取消。" : "The authenticator did not confirm the operation or the request was cancelled.")
    : (error?.message || (document.documentElement.lang === "zh-CN" ? "通行密钥操作失败。" : "The passkey operation failed."));

  document.addEventListener("click", async (event) => {
    const loginButton = event.target.closest("[data-passkey-login]");
    if (loginButton) {
      const form = loginButton.closest("form");
      if (!window.PublicKeyCredential || !form?.reportValidity()) return;
      loginButton.disabled = true;
      try {
        const body = new URLSearchParams({
          csrf_token: form.elements.csrf_token.value
        });
        const response = await fetch("/auth/passkey/options", { method: "POST", body, credentials: "same-origin" });
        if (!response.ok) throw new Error(await response.text());
        const challenge = await response.json();
        const credential = await navigator.credentials.get({ publicKey: decodePublicKey(challenge.options) });
        form.elements.passkey_ceremony.value = challenge.ceremony_id;
        form.elements.passkey_response.value = JSON.stringify(credentialJSON(credential));
        form.requestSubmit(form.querySelector('button[type="submit"]'));
      } catch (error) {
        const box = document.querySelector("[data-login-error]");
        if (box) {
          box.hidden = false;
          const message = box.querySelector("[data-login-error-message]");
          if (message) message.textContent = messageFor(error);
        }
      } finally {
        loginButton.disabled = false;
      }
      return;
    }

    const stepButton = event.target.closest("[data-passkey-step-up]");
    if (stepButton) {
      const form = stepButton.closest("form");
      if (!window.PublicKeyCredential || !form?.reportValidity()) return;
      stepButton.disabled = true;
      try {
        const response = await fetch("/auth/passkey/step-up/options", {
          method: "POST", credentials: "same-origin", headers: { "X-CSRF-Token": form.elements.csrf_token.value }
        });
        if (!response.ok) throw new Error(await response.text());
        const challenge = await response.json();
        const credential = await navigator.credentials.get({ publicKey: decodePublicKey(challenge.options) });
        form.elements.passkey_ceremony.value = challenge.ceremony_id;
        form.elements.passkey_response.value = JSON.stringify(credentialJSON(credential));
        form.requestSubmit(form.querySelector('button[type="submit"]'));
      } catch (error) {
        const box = form.closest("[data-task-page]")?.querySelector(".page-error") || document.createElement("p");
        box.className = "page-error";
        box.setAttribute("role", "alert");
        box.textContent = messageFor(error);
        if (!box.isConnected) form.before(box);
      } finally {
        stepButton.disabled = false;
      }
      return;
    }

    const registerButton = event.target.closest("[data-passkey-register]");
    if (!registerButton) return;
    const root = registerButton.closest("[data-passkey-registration]");
    const errorBox = root?.querySelector("[data-passkey-error]");
    if (!window.PublicKeyCredential || !root) return;
    registerButton.disabled = true;
    if (errorBox) errorBox.hidden = true;
    try {
      const headers = { "X-CSRF-Token": root.dataset.csrf, "Content-Type": "application/json" };
      const optionResponse = await fetch(root.dataset.optionsUrl, { method: "POST", credentials: "same-origin", headers });
      if (!optionResponse.ok) throw new Error(await optionResponse.text());
      const challenge = await optionResponse.json();
      const credential = await navigator.credentials.create({ publicKey: decodePublicKey(challenge.options) });
      const finishResponse = await fetch(root.dataset.finishUrl, {
        method: "POST", credentials: "same-origin", headers,
        body: JSON.stringify({ ceremony_id: challenge.ceremony_id, name: root.querySelector('[name="passkey_name"]')?.value || "Passkey", credential: credentialJSON(credential) })
      });
      if (!finishResponse.ok) throw new Error(await finishResponse.text());
      location.assign("/login");
    } catch (error) {
      if (errorBox) {
        errorBox.hidden = false;
        errorBox.textContent = messageFor(error);
      }
    } finally {
      registerButton.disabled = false;
    }
  });
})();
