# Kubernetes 顶部刷新工具栏验证

日期：2026-09-09

## 测试条目

- 三个页签上方统一显示一个手动刷新图标按钮和自动刷新开关，默认关闭。
- 三个折叠区域内部不再包含刷新或自动刷新控件。
- 手动刷新更新三个区域，保持折叠状态、URL、页面和集群概况。
- 自动刷新开关、筛选、排序、键盘及全部展开/收起正常工作。
- 桌面和手机布局、管理员登录、静态资源、未知路由。

## 结果

全部通过。Kubernetes 定向 Go 测试通过（5.879 秒）；共享刷新按钮回归测试通过，包含忙碌状态、失败保留原数据及重试。实际部署通过本机 Microsoft Edge 访问验证，无页面异常。补丁格式检查通过。设计检测器仅提示模板已有占位破折号。

## 本地部署

- 地址：http://127.0.0.1:18990/monitor/kubernetes?cluster=k8s_c60c9360d95b940022d7b96d
- 用户：admin；密码保留在 .scratch/k8s-collapse-20260909/admin-password.txt
- 应用 PID：62924；模拟 API PID：22780。
- 既有测试连接、状态和模拟集群数据保留。
- 测试脚本：.scratch/k8s-collapse-20260909/toolbar-check.cjs
- 截图：同目录 toolbar-desktop.png、toolbar-expanded-desktop.png、toolbar-mobile.png。
- 本次测试直接访问重新构建的部署，未使用内部浏览器。
