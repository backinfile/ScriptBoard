# 使用桌面 Chromium 作为浏览器门禁

前端自动化与视觉快照固定在桌面 Chromium，覆盖登录、分组导航、任务面板、运行日志和关键页面。测试依赖只用于开发与验证，不引入生产 Node.js 构建链或客户端框架。

Chrome、Edge、Firefox 和 Safari 继续作为最佳努力兼容目标，但不承诺每次提交都运行其自动化套件。移动端必须能完成核心操作，通过响应式实现、语义化页面和人工验收保证；当前不把移动截图加入自动化门禁。

此决定取代 [ADR-0083](./0083-support-modern-desktop-and-mobile-browsers.md)。
