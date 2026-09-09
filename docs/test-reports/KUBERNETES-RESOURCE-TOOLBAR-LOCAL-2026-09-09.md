# Kubernetes 资源操作行验证

日期：2026-09-09

## 测试条目与结果

- 手动刷新、自动刷新、全部展开、全部收起在同一行：桌面 1440px 与手机 390px 均通过，通过四个控件中心纵坐标相差小于 3px 验证。
- 三个区域默认折叠，内部无刷新控件：通过。
- 手动局部刷新、自动开关、筛选排序、键盘及全部展开/收起：通过。
- 登录、CSS/JS 资源和未知路由 404：通过。
- 手机页面无横向溢出：通过。
- Kubernetes 定向 Go 测试：通过，5.554 秒。
- git diff --check：通过。
- Edge 浏览器未捕获异常：0。
- 设计检测器仅提示原模板已有占位破折号。

## 保留部署

地址：http://127.0.0.1:18990/monitor/kubernetes?cluster=k8s_c60c9360d95b940022d7b96d

用户：admin。密码文件、状态、测试集群均保留在 .scratch/k8s-collapse-20260909/。

应用 PID：67564；模拟 API PID：22780。

实际重新构建部署，通过本机 Microsoft Edge 进行 HTTP 及布局验证。测试脚本 resource-toolbar-check.cjs 和 resource-toolbar-desktop.png、resource-toolbar-expanded-desktop.png、resource-toolbar-mobile.png 均保留在上述目录。
