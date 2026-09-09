# Kubernetes 折叠与局部刷新本地测试

日期：2026-09-09

## 测试范围

1. 外部访问、工作负载、节点默认折叠，外层保留基础信息。
2. 三个区域各有一个内部图标刷新按钮，任一按钮更新这三个区域。
3. 刷新保留展开状态、URL、历史记录和集群概况；按钮等待状态防止重复提交。
4. 请求失败、返回结构不完整时保留数据，提示失败并允许重试。
5. 全部展开/收起、键盘、实时刷新、筛选及排序。
6. 管理员登录、创建测试连接、静态资源、未知路由及手机布局。

## 结果

| 检查 | 结果 |
| --- | --- |
| go test ./internal/web -count=1 | 通过，106.992 秒 |
| Kubernetes 定向 Go 测试 | 通过 |
| kubernetes-section-refresh-contract.cjs | 通过：三个刷新入口、状态保留、忙碌状态、503/不完整响应与重试 |
| 现有 Kubernetes local / connection-delete / workload-node 浏览器测试 | 全部通过 |
| node --check internal/web/ui/assets/app.js | 通过 |
| git diff --check | 通过 |
| 实际部署默认折叠、内部按钮及三次刷新 | 通过 |
| 实际部署实时刷新开关、筛选、排序、键盘 | 通过，保留展开状态 |
| 登录、创建测试连接、JS/CSS、404 | 通过 |
| 1440px 桌面、390px 手机收起和展开布局 | 通过 |
| 浏览器未捕获异常 | 0 |
| 应用及模拟集群 stderr | 空 |

浏览器使用本机 Microsoft Edge，通过 HTTP 访问实际部署；无内部浏览器测试。视口切换后等待响应式布局稳定再验证。设计检测器报告的是原模板已有的占位破折号建议，不影响此次交互。

## 保留的部署

- URL：<http://127.0.0.1:18990/monitor/kubernetes?cluster=k8s_c60c9360d95b940022d7b96d>
- 用户：admin
- 密码文件：.scratch/k8s-collapse-20260909/admin-password.txt
- 应用 PID：31252；模拟 Kubernetes API PID：22780
- 模拟 API：http://127.0.0.1:18991
- 连接名称：折叠刷新测试集群；模式：仅观察。
- 文件目录：.scratch/k8s-collapse-20260909/
- 保留可执行文件、State Root、kubeconfig、模拟 API 源码、HTTP/浏览器验证脚本、登录会话和日志。
- 截图：collapsed-desktop.png、expanded-desktop.png、collapsed-mobile.png，均在上述目录中。

仅修改界面与局部更新方式，连接配置和协议策略未改动。
