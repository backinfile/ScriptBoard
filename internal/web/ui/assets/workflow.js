(() => {
  "use strict";
  const clone = (v) => JSON.parse(JSON.stringify(v)),
    esc = (v) =>
      String(v ?? "").replace(
        /[&<>"']/g,
        (c) =>
          ({
            "&": "&amp;",
            "<": "&lt;",
            ">": "&gt;",
            '"': "&quot;",
            "'": "&#39;",
          })[c],
      ),
    uid = () => crypto.randomUUID();
  const field = (name, type = "string", value, required = false) => ({
    name,
    type,
    required,
    source: "local_or_link",
    ...(value === undefined ? {} : { default: value }),
  });
  const out = (name, type = "string") => ({ name, type, required: true });
  const catalog = [
    {
      kind: "wait_for",
      name: "等待节点完成",
      group: "控制",
      inputs: [],
      outputs: [],
      config: { joinMode: "all" },
    },
    {
      kind: "switch",
      name: "按值分支",
      group: "控制",
      inputs: [field("value", "json")],
      outputs: [
        { ...out("value", "json"), required: false },
        out("matched", "json"),
      ],
      config: {
        valueType: "boolean",
        matchMode: "first",
        rules: [
          { id: "is_true", name: "为 true", operator: "eq", value: true },
        ],
      },
    },
    {
      kind: "merge",
      name: "分支汇合",
      group: "控制",
      inputs: [],
      outputs: [],
      config: { joinMode: "any" },
    },
    {
      kind: "wait",
      name: "等待固定时间",
      group: "控制",
      inputs: [field("seconds", "number", 1, true)],
      outputs: [],
    },
    {
      kind: "terminate",
      name: "结束流程",
      group: "控制",
      inputs: [],
      outputs: [],
      config: { status: "failed", message: "" },
    },
    {
      kind: "arithmetic",
      name: "算术",
      group: "数据处理",
      inputs: [field("a", "number", 0, true), field("b", "number", 0)],
      outputs: [out("result", "number")],
      config: { operation: "add" },
    },
    {
      kind: "compare",
      name: "比较",
      group: "数据处理",
      inputs: [field("a", "json", 0, true), field("b", "json", 0, true)],
      outputs: [out("result", "boolean")],
      config: { operation: "eq" },
    },
    {
      kind: "logic",
      name: "布尔逻辑",
      group: "数据处理",
      inputs: [
        field("a", "boolean", false, true),
        field("b", "boolean", false),
      ],
      outputs: [out("result", "boolean")],
      config: { operation: "and" },
    },
    {
      kind: "literal",
      name: "固定值",
      group: "数据",
      inputs: [],
      outputs: [out("value", "json")],
    },
    {
      kind: "lookup",
      name: "取值",
      group: "数据",
      inputs: [],
      outputs: [out("value", "json")],
    },
    {
      kind: "script",
      name: "脚本",
      group: "执行",
      inputs: [
        field("script", "string", undefined, true),
        field("arguments", "json", []),
        field("directory"),
      ],
      outputs: [],
    },
    {
      kind: "condition",
      name: "条件",
      group: "控制",
      inputs: [field("condition", "boolean", undefined, true)],
      outputs: [out("matched", "boolean")],
    },
    {
      kind: "git",
      name: "Git 拉取",
      group: "源码",
      inputs: [
        field("repository", "string", undefined, true),
        field("branch", "string", "dev", true),
        field("directory", "string", undefined, true),
        field("skipTLSVerify", "boolean", false),
      ],
      outputs: [out("sourceDir"), out("sourceCommit")],
    },
    {
      kind: "maven",
      name: "Maven 构建",
      group: "构建",
      inputs: [
        field("directory", "string", undefined, true),
        field("pom", "string", "pom.xml"),
        field("goals", "json", ["clean", "package"]),
        field("profiles"),
        field("skipTests", "boolean", false),
        field("version", "string", ""),
      ],
      outputs: [out("artifactPath"), out("version")],
    },
    {
      kind: "go",
      name: "Go 构建",
      group: "构建",
      inputs: [
        field("directory", "string", undefined, true),
        field("package", "string", "./cmd/server"),
        field("binary", "string", "dist/server"),
        field("goos", "string", "linux"),
        field("goarch", "string", "amd64"),
        field("cgo", "boolean", false),
        field("ldflags", "string", "-s -w"),
        field("version", "string", ""),
      ],
      outputs: [out("binaryPath"), out("version")],
    },
    {
      kind: "image_build",
      name: "制作镜像",
      group: "镜像",
      inputs: [
        field("context", "string", undefined, true),
        field("dockerfile", "string", "Dockerfile"),
        field("image", "string", undefined, true),
        field("platform", "string", "linux/amd64"),
        field("buildArgs", "json", {}),
        field("cache", "boolean", true),
      ],
      outputs: [out("imageRef"), out("imageId")],
    },
    {
      kind: "image_push",
      name: "推送镜像",
      group: "镜像",
      inputs: [
        field("image", "string", undefined, true),
        field("dockerContext"),
        field("dockerConfig"),
      ],
      outputs: [out("imageRef"), out("digest")],
    },
    {
      kind: "image_pull",
      name: "拉取镜像",
      group: "镜像",
      inputs: [field("image", "string", undefined, true)],
      outputs: [out("imageRef"), out("imageId")],
    },
    ...["k3d", "k3s"].map((kind) => ({
      kind,
      name: kind + " 部署",
      group: "部署",
      inputs: [
        ...(kind === "k3d"
          ? [
              field("cluster", "string", undefined, true),
              field("importImage", "boolean", false),
            ]
          : [
              field("kubeconfig", "string", undefined, true),
              field("context"),
              field("server"),
              field("skipTLSVerify", "boolean", false),
            ]),
        field("namespace", "string", "default"),
        field("manifest", "string", undefined, true),
        field("image"),
        field("deployment"),
        field("container", "string", "app"),
        field("wait", "boolean", true),
        field("endpoint", "string", ""),
      ],
      outputs: [out("namespace"), out("endpoint")],
    })),
  ];
  function initialize(root) {
    if (!root || root.dataset.ready) return;
    root.dataset.ready = "true";
    if (!document.querySelector("link[data-workflow-style]")) {
      const style = document.createElement("link");
      style.rel = "stylesheet";
      style.href = "/assets/workflow.css";
      style.dataset.workflowStyle = "true";
      document.head.appendChild(style);
    }
    const q = (s) => root.querySelector(s),
      content = q("[data-wf-content]"),
      drawer = q("[data-wf-drawer]"),
      search = q("[data-wf-search]"),
      manage = root.dataset.manage === "true",
      execute = root.dataset.execute === "true";
    let state = { draft: [], published: [], custom: [], entry: [], runs: [] },
      tab = readTab(),
      draft = null,
      selected = null,
      selectedLink = null,
      inspectorTab = "params",
      undo = [],
      redo = [],
      camera = { x: 35, y: 35, z: 0.8 },
      drag = null,
      wire = null,
      modal = null,
      searchContext = null,
      saveBusy = false,
      dirty = false;
    function readTab() {
      const value = new URL(location.href).searchParams.get("tab");
      return ["entries", "editor", "history", "custom", "guide"].includes(value)
        ? value
        : "entries";
    }
    function setTab(next, replace = false) {
      tab = next;
      const url = new URL(location.href);
      url.searchParams.set("tab", tab);
      if (url.href !== location.href)
        history[replace ? "replaceState" : "pushState"](history.state, "", url);
    }
    // Restore internal navigation in place so browser history preserves the editor draft.
    root.restoreWorkflowTab = () => {
      tab = readTab();
      closeSearch();
      render();
    };
    setTab(tab, true);
    const toast = (text) => {
      const el = q("[data-wf-toast]");
      el.textContent = text;
      el.classList.add("show");
      clearTimeout(el.timer);
      el.timer = setTimeout(() => el.classList.remove("show"), 3500);
    };
    async function api(path, body) {
      const res = await fetch("/workflow/" + path, {
        method: body === undefined ? "GET" : "POST",
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token": root.dataset.csrf,
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      if (!res.ok) {
        let message = await res.text();
        try {
          message = JSON.parse(message).error || message;
        } catch {}
        throw Error(message);
      }
      return res.json();
    }
    async function refresh() {
      state = await api("state");
      render();
    }
    function key(n) {
      return n.nodeType === 1
        ? n.id ||
            n.getAttribute("data-node") ||
            n.getAttribute("data-row") ||
            n.getAttribute("data-field")
        : null;
    }
    // Preserve focused form values while still updating the focused canvas and its wires.
    function patch(parent, fresh) {
      let cursor = parent.firstChild;
      for (const desired of [...fresh.childNodes]) {
        let current = cursor;
        const same = (n) =>
          n &&
          n.nodeType === desired.nodeType &&
          n.nodeName === desired.nodeName &&
          key(n) === key(desired);
        if (!same(current)) {
          current = key(desired) ? [...parent.childNodes].find(same) : null;
          if (!current) current = desired.cloneNode(true);
          parent.insertBefore(current, cursor);
        }
        if (current.nodeType === 1) {
          for (const a of [...current.attributes]) {
            if (a.name === "open" && current.tagName === "DETAILS") continue;
            if (!desired.hasAttribute(a.name)) current.removeAttribute(a.name);
          }
          for (const a of [...desired.attributes])
            if (current.getAttribute(a.name) !== a.value)
              current.setAttribute(a.name, a.value);
          if (
            current !== document.activeElement ||
            !current.matches("input,textarea,select")
          ) {
            patch(current, desired);
            if ("value" in current && current.value !== desired.value)
              current.value = desired.value;
            if ("checked" in current) current.checked = desired.checked;
          }
        } else if (current.nodeValue !== desired.nodeValue)
          current.nodeValue = desired.nodeValue;
        cursor = current.nextSibling;
      }
      while (cursor) {
        const next = cursor.nextSibling;
        parent.removeChild(cursor);
        cursor = next;
      }
    }
    function html(el, text) {
      const t = document.createElement("template");
      t.innerHTML = text;
      patch(el, t.content);
    }
    function types() {
      return [
        ...catalog,
        ...state.custom.map((d) => ({
          kind: "custom",
          customId: d.id,
          customRevision: d.revision,
          name: d.name,
          group: "自定义",
          inputs: d.inputs,
          outputs: d.outputs,
          description: d.description,
        })),
      ];
    }
    function restoreDraft(snapshot) {
      const id = draft.id,
        revision = draft.revision;
      draft = clone(snapshot);
      if (id) {
        draft.id = id;
        draft.revision = revision;
      }
    }
    let layoutBusy = false;
    function nodeSize(n) {
      const el = [...root.querySelectorAll(".wf-node")].find(
        (el) => el.dataset.node === n.id,
      );
      return { width: el?.offsetWidth || 290, height: el?.offsetHeight || 180 };
    }
    function fitGraph() {
      const stage = q(".wf-stage"),
        nodes = draft?.graph.nodes;
      if (!stage || !nodes?.length) return;
      const left = Math.min(...nodes.map((n) => n.x)),
        top = Math.min(...nodes.map((n) => n.y));
      const right = Math.max(...nodes.map((n) => n.x + nodeSize(n).width));
      const bottom = Math.max(...nodes.map((n) => n.y + nodeSize(n).height));
      const z = Math.max(
        0.05,
        Math.min(
          1,
          (stage.clientWidth - 64) / (right - left),
          (stage.clientHeight - 80) / (bottom - top),
        ),
      );
      camera = {
        z,
        x: (stage.clientWidth - (right - left) * z) / 2 - left * z,
        y: (stage.clientHeight - (bottom - top) * z) / 2 - top * z,
      };
      transform();
    }
    async function arrange() {
      if (!manage || !draft?.graph.nodes.length || layoutBusy) return;
      layoutBusy = true;
      const original = draft,
        snapshot = JSON.stringify(draft);
      let worker,
        timer,
        finished = false;
      try {
        const ports = new Map();
        const graph = {
          id: "root",
          layoutOptions: {
            "elk.algorithm": "layered",
            "elk.direction": "RIGHT",
            "elk.spacing.nodeNode": "56",
            "elk.layered.spacing.nodeNodeBetweenLayers": "110",
            "elk.randomSeed": "1",
            "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
            "elk.padding": "[top=32,left=32,bottom=32,right=32]",
          },
          children: draft.graph.nodes.map((n) => ({
            id: n.id,
            ...nodeSize(n),
            ports: [],
            layoutOptions: {
              "elk.portConstraints": "FIXED_POS",
              ...(n.kind === "start"
                ? { "elk.layered.layering.layerConstraint": "FIRST" }
                : n.kind === "end"
                  ? { "elk.layered.layering.layerConstraint": "LAST" }
                  : {}),
            },
          })),
          edges: [],
        };
        for (const link of allLinks()) {
          const ids = [link.a, link.b].map((p) => {
            const key = JSON.stringify([p.node, p.dir, p.mode, p.field]);
            if (!ports.has(key)) {
              const node = graph.children.find((n) => n.id === p.node);
              if (!node) throw new Error("连线引用的节点不存在");
              const source = draft.graph.nodes.find((n) => n.id === p.node),
                point = position(p);
              const id = "port-" + ports.size;
              node.ports.push({
                id,
                width: 0,
                height: 0,
                x: point.x - source.x,
                y: point.y - source.y,
                layoutOptions: {
                  "elk.port.side": p.dir === "out" ? "EAST" : "WEST",
                },
              });
              ports.set(key, id);
            }
            return ports.get(key);
          });
          graph.edges.push({
            id: link.key,
            sources: [ids[0]],
            targets: [ids[1]],
          });
        }
        render();
        const result = await new Promise((resolve, reject) => {
          timer = setTimeout(
            () => reject(new Error("整理超时，请重试")),
            20000,
          );
          const load = window.ELK
            ? Promise.resolve()
            : new Promise((done, fail) => {
                const script = document.createElement("script");
                script.src = "/assets/elk-0.12.0.js";
                script.onload = done;
                script.onerror = () => {
                  script.remove();
                  fail(new Error("布局组件加载失败，请重试"));
                };
                document.head.append(script);
              });
          load
            .then(() => {
              if (finished || !root.isConnected) return;
              worker = new window.ELK({
                workerUrl: "/assets/workflow-layout-worker.js",
                algorithms: ["layered"],
              });
              worker
                .layout(graph)
                .then((result) => resolve(result.children), reject);
            })
            .catch(reject);
        });
        // Ignore stale geometry after editing, switching workflows or leaving the page.
        if (
          !root.isConnected ||
          draft !== original ||
          JSON.stringify(draft) !== snapshot ||
          tab !== "editor"
        )
          return;
        if (
          result.length !== draft.graph.nodes.length ||
          result.some(
            (n) =>
              !Number.isFinite(n.x) ||
              !Number.isFinite(n.y) ||
              !draft.graph.nodes.some((v) => v.id === n.id),
          )
        )
          throw new Error("布局结果无效");
        if (
          result.some((p) => {
            const n = draft.graph.nodes.find((n) => n.id === p.id);
            return n.x !== p.x || n.y !== p.y;
          })
        ) {
          checkpoint();
          for (const p of result)
            Object.assign(
              draft.graph.nodes.find((n) => n.id === p.id),
              { x: p.x, y: p.y },
            );
        }
        render();
        fitGraph();
        toast("已按依赖分层整理，可撤销恢复。");
      } catch (error) {
        toast(error.message);
      } finally {
        finished = true;
        clearTimeout(timer);
        worker?.terminateWorker();
        layoutBusy = false;
        if (root.isConnected) render();
      }
    }
    function checkpoint() {
      undo.push(clone(draft));
      if (undo.length > 40) undo.shift();
      redo = [];
      dirty = true;
    }
    function inputs(n) {
      return n.inputs || [];
    }
    function outputs(n) {
      return n.outputs || [];
    }
    function displayOutputs(n) {
      return ["start", "end", "terminate"].includes(n.kind)
        ? outputs(n)
        : [...outputs(n), { name: "_error", type: "json", required: false }];
    }
    function controlPorts(n) {
      if (["end", "terminate"].includes(n.kind)) return [];
      if (n.kind === "start") return [{ id: "success", name: "成功" }];
      const ending = [
        { id: "failure", name: "失败" },
        { id: "always", name: "完成（成功或失败）" },
      ];
      if (n.kind === "switch")
        return [
          ...(n.config?.rules || []).map((r) => ({ id: r.id, name: r.name })),
          { id: "default", name: "其他情况" },
          ...ending,
        ];
      if (n.kind === "condition")
        return [
          { id: "true", name: "true" },
          { id: "false", name: "false" },
          ...ending,
        ];
      return [{ id: "success", name: "成功" }, ...ending];
    }
    function configSelect(n, key, label, options) {
      return (
        '<label class="wf-field">' +
        label +
        '<select data-control-config="' +
        key +
        '">' +
        options
          .map(
            ([value, name]) =>
              '<option value="' +
              value +
              '" ' +
              (n.config?.[key] === value ? "selected" : "") +
              ">" +
              name +
              "</option>",
          )
          .join("") +
        "</select></label>"
      );
    }
    function controlSettings(n) {
      let html = "";
      if (n.kind === "switch")
        html =
          button("branch-rules", "配置分支规则") +
          "<p>" +
          esc(n.config?.valueType || "boolean") +
          " · " +
          (n.config?.matchMode === "all" ? "触发所有匹配" : "首条匹配") +
          "</p>";
      if (n.kind === "arithmetic")
        html = configSelect(n, "operation", "运算", [
          ["add", "加法 a + b"],
          ["subtract", "减法 a − b"],
          ["multiply", "乘法 a × b"],
          ["divide", "除法 a ÷ b"],
          ["modulo", "取余"],
          ["power", "幂"],
          ["min", "最小值"],
          ["max", "最大值"],
          ["abs", "绝对值 a"],
          ["floor", "向下取整 a"],
          ["ceil", "向上取整 a"],
          ["round", "四舍五入 a"],
        ]);
      if (n.kind === "compare")
        html = configSelect(n, "operation", "比较", [
          ["eq", "等于"],
          ["ne", "不等于"],
          ["gt", "大于"],
          ["gte", "大于等于"],
          ["lt", "小于"],
          ["lte", "小于等于"],
          ["contains", "包含"],
          ["starts_with", "前缀"],
          ["matches", "正则匹配"],
        ]);
      if (n.kind === "logic")
        html = configSelect(n, "operation", "布尔运算", [
          ["and", "与 a AND b"],
          ["or", "或 a OR b"],
          ["not", "非 NOT a"],
          ["xor", "异或 a XOR b"],
        ]);
      if (n.kind === "merge")
        html = configSelect(n, "joinMode", "汇合条件", [
          ["any", "任一入线被触发"],
          ["all", "全部入线被触发"],
        ]);
      if (n.kind === "wait_for")
        html =
          button("wait-targets", "选择等待节点") +
          configSelect(n, "joinMode", "等待条件", [
            ["all", "全部完成"],
            ["any", "任一完成"],
          ]) +
          "<p>等待对象会显示为控制连线。</p>";
      if (n.kind === "terminate")
        html =
          configSelect(n, "status", "流程结束状态", [
            ["failed", "失败"],
            ["succeeded", "成功（明确恢复）"],
          ]) + input("结束说明", "config.message", n.config?.message || "");
      if (!["start", "end", "terminate"].includes(n.kind))
        html +=
          '<details class="wf-retry"><summary>失败重试</summary>' +
          input(
            "总尝试次数（1～10）",
            "config.retryAttempts",
            n.config?.retryAttempts || 1,
            "number",
          ) +
          input(
            "重试间隔（毫秒）",
            "config.retryDelayMs",
            n.config?.retryDelayMs ?? 1000,
            "number",
          ) +
          configSelect(n, "retryBackoff", "间隔方式", [
            ["fixed", "固定"],
            ["exponential", "指数退避（上限 60 秒）"],
          ]) +
          "<small>只对已确认失败重试；有副作用的操作需保证可重复执行。</small></details>";
      return html;
    }
    function button(action, label, id = "", cls = "") {
      return (
        '<button type="button" class="' +
        cls +
        '" data-action="' +
        action +
        '" data-id="' +
        esc(id) +
        '">' +
        label +
        "</button>"
      );
    }
    function input(label, key, value, type = "text") {
      return (
        '<label class="wf-field">' +
        esc(label) +
        '<input data-field="' +
        esc(key) +
        '" type="' +
        type +
        '" value="' +
        esc(value) +
        '"></label>'
      );
    }
    function parameterControl(f, key, value) {
      if (f.enum?.length || f.type === "boolean") {
        const options = f.enum?.length ? f.enum : [true, false];
        return (
          '<label class="wf-field">' +
          esc(f.name) +
          " · " +
          f.type +
          '<select data-field="' +
          esc(key) +
          '"><option value="">使用默认值</option>' +
          options
            .map(
              (v) =>
                '<option value="' +
                esc(v) +
                '" ' +
                (String(value) === String(v) ? "selected" : "") +
                ">" +
                esc(v) +
                "</option>",
            )
            .join("") +
          "</select></label>"
        );
      }
      return input(f.name + " · " + f.type, key, valueText(value ?? ""));
    }
    function valueText(v) {
      return typeof v === "string" ? v : JSON.stringify(v ?? "");
    }
    function parseValue(text, type) {
      if (type === "string") return text;
      if (text === "") return undefined;
      const v = JSON.parse(text);
      if (type === "integer" && !Number.isSafeInteger(v))
        throw Error("需要安全范围内的整数");
      if (type === "number" && (typeof v !== "number" || !Number.isFinite(v)))
        throw Error("需要数字");
      if (type === "boolean" && typeof v !== "boolean")
        throw Error("需要 true 或 false");
      return v;
    }
    function render() {
      root.querySelectorAll("[data-wf-tab]").forEach((b) => {
        b.setAttribute("aria-selected", String(b.dataset.wfTab === tab));
        b.tabIndex = b.dataset.wfTab === tab ? 0 : -1;
      });
      html(
        content,
        tab === "entries"
          ? entriesView()
          : tab === "custom"
            ? customView()
            : tab === "guide"
              ? guideView()
              : tab === "history"
                ? historyView()
                : editorView(),
      );
      if (tab === "editor") transform();
    }
    function exampleBundle() {
      return {
        format: "scriptboard.workflow",
        version: 1,
        workflow: {
          name: "示例：计算并返回结果",
          parameters: [],
          graph: {
            nodes: [
              {
                id: "s",
                kind: "start",
                name: "入口",
                x: 40,
                y: 80,
                inputs: [],
                outputs: [],
              },
              {
                id: "calc",
                kind: "arithmetic",
                name: "计算 7 × 2",
                x: 440,
                y: 80,
                inputs: [
                  field("a", "number", 7, true),
                  field("b", "number", 2, true),
                ],
                outputs: [out("result", "number")],
                config: { operation: "multiply" },
              },
              {
                id: "e",
                kind: "end",
                name: "出口",
                x: 840,
                y: 80,
                inputs: [field("result", "number", undefined, true)],
                outputs: [],
              },
            ],
            links: [
              {
                from: { node: "calc", field: "result" },
                to: { node: "e", field: "result" },
              },
            ],
            dependencies: [{ from: "s", to: "calc" }],
          },
        },
        customNodes: [],
      };
    }
    function downloadText(name, text, type = "text/plain;charset=utf-8") {
      const url = URL.createObjectURL(new Blob([text], { type })),
        link = document.createElement("a");
      link.href = url;
      link.download = name;
      document.body.append(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    }
    function guideView() {
      return (
        '<header class="wf-heading"><div><h2>说明</h2><p>查看工作流配置规范，让 AI 按指南生成配置文件。</p></div></header><div class="wf-guide"><section><h3>让 AI 编写工作流</h3><p>下载指南并交给 AI，描述输入参数、执行步骤和预期结果。指南包含完整字段约定、内置节点模板和可导入示例。</p><div class="wf-row">' +
        button("guide-download", "下载 AI 编写指南", "", "primary") +
        button("example-download", "下载示例 JSON") +
        "</div></section><section><h3>导入与导出</h3><ol><li>在编排页的更多菜单中导出当前配置，或导入 JSON 文件。</li><li>导入前查看文件内容和流程摘要，确认后新建工作流，不覆盖原有配置。</li><li>导入成功后检查主机路径与工具链，再创建 Entry 运行。</li></ol>" +
        "</section><section><h3>文件包含什么</h3><p>流程节点、参数、连线、配置，以及引用版本的自定义脚本。Entry、历史运行和主机文件不随包迁移；配置值与脚本正文会原样导出。</p><p>格式：scriptboard.workflow v1 · UTF-8 JSON · 最大 4 MiB。</p></section></div>"
      );
    }
    function importPreview(text) {
      const bundle = JSON.parse(text.replace(/^\uFEFF/, ""));
      if (
        bundle.format !== "scriptboard.workflow" ||
        bundle.version !== 1 ||
        !Array.isArray(bundle.workflow?.graph?.nodes) ||
        !Array.isArray(bundle.customNodes)
      )
        throw Error("请选择 scriptboard.workflow v1 配置文件");
      return bundle;
    }
    function entryIcon(kind) {
      const paths = {
        workflow:
          '<rect x="3" y="3" width="6" height="6" rx="1"/><rect x="15" y="15" width="6" height="6" rx="1"/><path d="M6 9v9h9M9 6h9v9"/>',
        undo: '<path d="M3 10h11a7 7 0 0 1 0 14M3 10l5-5M3 10l5 5" transform="translate(0 -3)"/>',
        redo: '<path d="M21 7H10a7 7 0 0 0 0 14M21 7l-5-5M21 7l-5 5"/>',
        plus: '<path d="M12 5v14M5 12h14"/>',
        minus: '<path d="M5 12h14"/>',
        fit: '<path d="M8 3H3v5M16 3h5v5M21 16v5h-5M8 21H3v-5"/>',
        edit: '<path d="M12 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="m16 3 5 5-9 9-5 1 1-5Z"/>',
        copy: '<rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>',
        lock: '<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
        unlock:
          '<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 9.9-1"/>',
        trash: '<path d="M3 6h18M19 6l-1 14H6L5 6M9 6V3h6v3M10 10v7M14 10v7"/>',
        play: '<path d="m9 5 12 7-12 7V5Z"/>',
        check: '<path d="m20 6-11 11-5-5"/>',
        x: '<path d="m18 6-12 12M6 6l12 12"/>',
        clock: '<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>',
        more: '<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>',
      };
      return (
        '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
        paths[kind] +
        "</svg>"
      );
    }
    function entriesView() {
      const groups = [
        ...new Set(state.entry.map((e) => e.group || "默认分组")),
      ];
      return (
        '<header class="wf-heading"><div><h2>工作流执行</h2><p>使用 Entry 已保存参数执行工作流。</p></div>' +
        (manage ? button("entry-new", "创建 Entry") : "") +
        "</header>" +
        groups
          .map(
            (group) =>
              '<details open class="wf-group"><summary>' +
              esc(group) +
              '</summary><div class="qr-grid">' +
              state.entry
                .filter((e) => (e.group || "默认分组") === group)
                .sort((a, b) => a.order - b.order)
                .map((e) => {
                  const flow = state.published.find(
                      (f) => f.id === e.workflowId,
                    ),
                    stale = !flow || flow.revision !== e.workflowRevision;
                  const runs = state.runs
                      .filter((r) => r.entryId === e.id)
                      .sort((a, b) => b.createdAt - a.createdAt),
                    last = runs[0];
                  const finished =
                    last &&
                    ["succeeded", "failed", "cancelled", "canceled"].includes(
                      last.status,
                    );
                  const duration = finished
                    ? Math.max(
                        0,
                        (last.updatedAt - last.createdAt) / 1e9,
                      ).toFixed(1) + " 秒"
                    : last
                      ? statusLabel(last.status)
                      : "—";
                  return (
                    '<article class="qr wf-entry" id="entry-' +
                    e.id +
                    '" data-locked="' +
                    e.locked +
                    '"><div class="qr__head"><div class="qr__identity"><h3>' +
                    esc(e.name) +
                    '</h3><code class="qr__path" title="' +
                    esc(flow?.name || "未绑定工作流") +
                    '">' +
                    esc(flow?.name || "未绑定工作流") +
                    "</code></div>" +
                    (manage
                      ? '<details class="action-menu"><summary aria-label="' +
                        esc(e.name) +
                        ' 更多操作">' +
                        entryIcon("more") +
                        "</summary><div>" +
                        button(
                          "entry-workflow",
                          entryIcon("workflow") + "打开工作流",
                          e.id,
                        ) +
                        button("entry-edit", entryIcon("edit") + "编辑", e.id) +
                        button("entry-copy", entryIcon("copy") + "复制", e.id) +
                        button(
                          "entry-lock",
                          entryIcon(e.locked ? "unlock" : "lock") +
                            (e.locked ? "解锁" : "锁定"),
                          e.id,
                        ) +
                        button(
                          "entry-delete",
                          entryIcon("trash") + "删除",
                          e.id,
                          "danger-action",
                        ) +
                        "</div></details>"
                      : "") +
                    '</div><div class="qr__meta"><span class="qr__version">v' +
                    e.workflowRevision +
                    "</span>" +
                    (e.locked
                      ? '<span class="status-chip" data-state="locked">已锁定</span>'
                      : "") +
                    '<div class="quick-run-history__barometer qr__barometer" role="group" aria-label="最近运行">' +
                    (runs.length
                      ? runs
                          .slice(0, 5)
                          .map(
                            (r) =>
                              '<a href="#run-' +
                              r.id +
                              '" data-action="entry-history" data-id="' +
                              r.id +
                              '" data-state="' +
                              r.status +
                              '" title="' +
                              esc(
                                statusLabel(r.status) +
                                  " · " +
                                  new Date(r.createdAt / 1e6).toLocaleString(),
                              ) +
                              '" aria-label="' +
                              esc(
                                "查看运行：" +
                                  statusLabel(r.status) +
                                  " · " +
                                  new Date(r.createdAt / 1e6).toLocaleString(),
                              ) +
                              '">' +
                              entryIcon(
                                r.status === "succeeded"
                                  ? "check"
                                  : r.status === "failed"
                                    ? "x"
                                    : "clock",
                              ) +
                              "</a>",
                          )
                          .join("")
                      : '<span class="quick-run-history__empty">暂无记录</span>') +
                    "</div></div>" +
                    (stale
                      ? '<small class="wf-entry-warning">需要更新工作流绑定</small>'
                      : "") +
                    '<div class="qr__foot"><dl class="quick-run-history__latest qr__last"><div><dt>最近一次</dt><dd>' +
                    (last
                      ? "<time>" +
                        esc(new Date(last.createdAt / 1e6).toLocaleString()) +
                        "</time>"
                      : "—") +
                    '</dd><dd title="从提交到结束，包含排队时间">' +
                    esc(duration) +
                    "</dd></div></dl>" +
                    (execute
                      ? '<button type="button" class="button button--compact qr__run" data-action="run" data-id="' +
                        e.id +
                        '" ' +
                        (stale ? 'disabled title="请先更新工作流绑定"' : "") +
                        ">" +
                        entryIcon("play") +
                        "运行</button>"
                      : "") +
                    "</div></article>"
                  );
                })
                .join("") +
              "</div></details>",
          )
          .join("") +
        (!groups.length
          ? '<div class="wf-empty">先保存工作流，再创建执行入口。</div>'
          : "")
      );
    }
    function customView() {
      return (
        '<header class="wf-heading"><div><h2>自定义节点</h2><p>脚本正文和版本存储在 ScriptBoard。</p></div>' +
        (manage ? button("custom-new", "创建自定义节点") : "") +
        '</header><div class="wf-grid">' +
        state.custom
          .map(
            (d) =>
              '<article class="wf-card" id="custom-' +
              d.id +
              '"><h3>' +
              esc(d.name) +
              "</h3><p>" +
              esc(d.description) +
              '</p><div class="wf-row"><span class="wf-badge">' +
              esc(d.language) +
              '</span><span class="wf-badge">v' +
              d.revision +
              "</span></div><footer><small>" +
              d.inputs.length +
              " 输入 · " +
              d.outputs.length +
              " 输出</small>" +
              (manage
                ? '<button type="button" class="icon-button wf-custom-more" data-action="custom-edit" data-id="' +
                  esc(d.id) +
                  '" aria-label="配置自定义节点 ' +
                  esc(d.name) +
                  '" title="配置节点"><svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/></svg></button>'
                : "") +
              "</footer></article>",
          )
          .join("") +
        "</div>" +
        (!state.custom.length
          ? '<div class="wf-empty">暂无自定义节点。创建节点后可在编排中使用。</div>'
          : "") +
        (!manage ? "<p>当前账号没有管理自定义脚本的权限。</p>" : "")
      );
    }
    const historySelection = new Map();
    function runStatus(status) {
      return (
        '<span class="wf-run-status" data-state="' +
        esc(status) +
        '">' +
        entryIcon(
          status === "succeeded"
            ? "check"
            : ["failed", "cancelled", "canceled", "needs_attention"].includes(
                  status,
                )
              ? "x"
              : "clock",
        ) +
        "<span>" +
        esc(statusLabel(status)) +
        "</span></span>"
      );
    }
    function routeHistory(run, step) {
      const node = run.snapshot.workflow.graph.nodes.find(
        (n) => n.id === step.nodeId,
      );
      const routes = (step.outlets || []).map(
        (id) => controlPorts(node).find((p) => p.id === id)?.name || id,
      );
      return (
        (routes.length
          ? '<p class="wf-route-result">已触发：' +
            esc(routes.join(" · ")) +
            "</p>"
          : "") +
        (step.attempts?.length
          ? '<details class="wf-job-result"><summary>执行尝试 · ' +
            step.attempts.length +
            " 次</summary>" +
            step.attempts
              .map(
                (a) =>
                  '<div class="wf-attempt"><span>第 ' +
                  a.number +
                  " 次 · " +
                  esc(statusLabel(a.status)) +
                  "</span>" +
                  (a.processId ? button("logs", "查看日志", a.processId) : "") +
                  (a.error ? "<p>" + esc(a.error) + "</p>" : "") +
                  "</div>",
              )
              .join("") +
            "</details>"
          : "")
      );
    }
    function historyView() {
      return (
        '<header class="wf-heading"><div><h2>运行历史</h2><p>查看运行状态、节点结果和执行日志。</p></div>' +
        button("refresh", "刷新") +
        "</header>" +
        (!state.runs.length
          ? '<div class="wf-empty">暂无运行记录。</div>'
          : '<div class="wf-run-list">' +
            state.runs
              .map((r) => {
                const nodes = r.snapshot.workflow.graph.nodes;
                const steps = nodes.map((n) => ({
                  ...r.steps.find((s) => s.nodeId === n.id),
                  nodeId: n.id,
                  name: n.name || n.kind,
                  status:
                    r.steps.find((s) => s.nodeId === n.id)?.status || "pending",
                  kind: n.kind,
                }));
                const chosen =
                  steps.find((s) => s.nodeId === historySelection.get(r.id)) ||
                  steps.find((s) =>
                    ["failed", "running", "needs_attention"].includes(s.status),
                  ) ||
                  steps.find((s) => s.processId) ||
                  steps[0];
                const total = [
                  "succeeded",
                  "failed",
                  "cancelled",
                  "canceled",
                ].includes(r.status)
                  ? Math.max(0, (r.updatedAt - r.createdAt) / 1e9).toFixed(1) +
                    " 秒"
                  : statusLabel(r.status);
                return (
                  '<details class="wf-run" id="run-' +
                  r.id +
                  '"><summary>' +
                  runStatus(r.status) +
                  '<span class="wf-run-title"><strong>' +
                  esc(r.snapshot.entry.name) +
                  "</strong><small>" +
                  esc(r.snapshot.workflow.name) +
                  " · v" +
                  r.snapshot.workflow.revision +
                  " · " +
                  esc(r.actor) +
                  "</small></span><time>" +
                  esc(new Date(r.createdAt / 1e6).toLocaleString()) +
                  '</time><small title="包含排队时间">' +
                  esc(total) +
                  '</small></summary><div class="wf-run-overview"><span>运行 ' +
                  esc(r.id.slice(0, 8)) +
                  "</span><span>" +
                  steps.filter((s) => s.status === "succeeded").length +
                  " / " +
                  steps.length +
                  " 个节点成功</span>" +
                  (["queued", "running"].includes(r.status) && execute
                    ? button("cancel", "取消运行", r.id)
                    : "") +
                  (r.status === "needs_attention" && manage
                    ? button("resolve", "核查并释放资源", r.id)
                    : "") +
                  "</div>" +
                  (r.error
                    ? '<p class="wf-form-error">' + esc(r.error) + "</p>"
                    : "") +
                  '<div class="wf-run-detail"><aside class="wf-job-list" aria-label="执行节点"><h3>节点</h3>' +
                  steps
                    .map(
                      (step) =>
                        '<button type="button" data-action="history-step" data-id="' +
                        esc(JSON.stringify([r.id, step.nodeId])) +
                        '" aria-pressed="' +
                        (step === chosen) +
                        '">' +
                        runStatus(step.status) +
                        "<span>" +
                        esc(step.name) +
                        "</span></button>",
                    )
                    .join("") +
                  '</aside><section class="wf-job-panel">' +
                  (chosen
                    ? "<header><div><h3>" +
                      esc(chosen.name) +
                      "</h3><small>" +
                      esc(chosen.kind) +
                      "</small></div>" +
                      runStatus(chosen.status) +
                      "</header>" +
                      (chosen.error
                        ? '<p class="wf-form-error">' +
                          esc(chosen.error) +
                          "</p>"
                        : "") +
                      routeHistory(r, chosen) +
                      '<div class="wf-job-output"><h4>执行日志</h4>' +
                      (chosen.processId
                        ? button("logs", "查看实时日志", chosen.processId)
                        : "<p>" +
                          (["pending", "queued"].includes(chosen.status)
                            ? "节点尚未开始执行。"
                            : "此节点没有进程日志。") +
                          "</p>") +
                      '</div><details class="wf-job-result"><summary>节点输出</summary><pre>' +
                      esc(JSON.stringify(chosen.result || {}, null, 2)) +
                      "</pre></details>"
                    : "<p>暂无节点。</p>") +
                  '<details class="wf-job-result"><summary>工作流输出</summary><pre>' +
                  esc(JSON.stringify(r.result || {}, null, 2)) +
                  "</pre></details></section></div></details>"
                );
              })
              .join("") +
            "</div>")
      );
    }
    function toolButton(action, label, icon, disabled = false) {
      return (
        '<button type="button" class="wf-tool-icon" data-action="' +
        action +
        '" title="' +
        label +
        '" aria-label="' +
        label +
        '" ' +
        (disabled ? "disabled" : "") +
        ">" +
        entryIcon(icon) +
        "</button>"
      );
    }
    function editorView() {
      return (
        '<header class="wf-heading wf-editor-heading"><div class="wf-flow-controls"><select data-flow-select aria-label="选择工作流" ' +
        (saveBusy ? "disabled" : "") +
        '><option value="">选择工作流</option>' +
        state.draft
          .map(
            (f) =>
              '<option value="' +
              f.id +
              '" ' +
              (draft?.id === f.id ? "selected" : "") +
              ">" +
              esc(f.name) +
              "</option>",
          )
          .join("") +
        "</select>" +
        (draft
          ? '<input class="wf-flow-name" data-workflow-name value="' +
            esc(draft.name) +
            '" aria-label="工作流名称">'
          : "") +
        '</div><div class="wf-row">' +
        (manage
          ? (draft
              ? button("flow-fields", "入口参数") +
                button("flow-output-fields", "出口参数") +
                '<button type="button" class="primary" data-action="flow-save" ' +
                (saveBusy ? 'disabled aria-busy="true"' : "") +
                ">" +
                (saveBusy ? "保存中…" : "保存") +
                "</button>"
              : "") +
            '<details class="action-menu wf-flow-menu"><summary aria-label="工作流更多操作">' +
            entryIcon("more") +
            "</summary><div>" +
            button("flow-new", entryIcon("plus") + "新建工作流") +
            button("flow-import", entryIcon("workflow") + "导入工作流") +
            (draft
              ? button("flow-export", entryIcon("copy") + "导出工作流")
              : "") +
            (draft?.id
              ? button(
                  "flow-delete",
                  entryIcon("trash") + "删除工作流",
                  "",
                  "danger-action",
                )
              : "") +
            "</div></details>"
          : "") +
        "</div></header>" +
        (!draft
          ? '<div class="wf-empty">选择或新建工作流，保存后即可配置 Entry。</div>'
          : '<div class="wf-editor"><div class="wf-canvas"><div class="wf-canvas-bar"><div class="wf-tool-group">' +
            (manage
              ? button("node-search", entryIcon("plus") + "添加节点")
              : "") +
            '</div><div class="wf-tool-group">' +
            toolButton("undo", "撤销", "undo", !undo.length) +
            toolButton("redo", "重做", "redo", !redo.length) +
            '</div><div class="wf-tool-group">' +
            (manage
              ? '<button type="button" data-action="arrange" ' +
                (layoutBusy ? 'disabled aria-busy="true"' : "") +
                ">" +
                (layoutBusy ? "整理中…" : "一键整理") +
                "</button>"
              : "") +
            '</div><div class="wf-tool-group wf-zoom-tools">' +
            toolButton("zoom-out", "缩小", "minus") +
            toolButton("fit", "适应画布", "fit") +
            toolButton("zoom-in", "放大", "plus") +
            '</div></div><div class="wf-stage" tabindex="0"><div class="wf-world"><svg class="wf-edges">' +
            linksHTML() +
            "</svg>" +
            draft.graph.nodes.map(nodeHTML).join("") +
            '</div><svg class="wf-wire"></svg><small class="wf-hint">拖拽空白处平移 · 双击添加 · 拖拽端口连接 · Delete 删除选中项</small></div></div><aside class="wf-inspector">' +
            inspectorHTML() +
            "</aside></div>")
      );
    }
    // A parameter owns one row: its port and local value/source are rendered together.
    function nodeParameter(n, f, dir) {
      const entry = n.kind === "start";
      const link =
        dir === "in"
          ? draft.graph.links.find(
              (l) => l.to.node === n.id && l.to.field === f.name,
            )
          : null;
      const editable =
        entry || (dir === "in" && !link && f.source !== "link_only");
      const source = link
        ? draft.graph.nodes.find((node) => node.id === link.from.node)
        : null;
      return (
        '<div class="wf-node-param ' +
        dir +
        (entry ? " entry" : "") +
        '">' +
        portHTML(n.id, f.name, dir) +
        '<label class="wf-param-label"><span>' +
        esc(f.name) +
        (f.required ? " *" : "") +
        "</span><small>" +
        esc(f.type) +
        "</small>" +
        (editable
          ? '<input data-node-value="' +
            esc(f.name) +
            '" data-owner="' +
            n.id +
            '" aria-label="' +
            esc(f.name) +
            '" value="' +
            esc(
              valueText(
                entry
                  ? (f.default ?? "")
                  : (n.values?.[f.name] ?? f.default ?? ""),
              ),
            ) +
            '">'
          : "") +
        "</label>" +
        (link
          ? '<code class="wf-param-source" title="' +
            esc((source?.name || link.from.node) + "." + link.from.field) +
            '">' +
            esc((source?.name || link.from.node) + "." + link.from.field) +
            "</code>"
          : dir === "in" && !editable
            ? '<small class="wf-param-source">等待连接</small>'
            : "") +
        "</div>"
      );
    }
    function nodeHTML(n) {
      const height = Math.max(inputs(n).length, displayOutputs(n).length, 1);
      return (
        '<article class="wf-node ' +
        (selected === n.id ? "selected" : "") +
        '" data-node="' +
        n.id +
        '" tabindex="0"><header><strong>' +
        esc(n.name || n.kind) +
        "</strong><small>" +
        esc(n.kind) +
        (n.customRevision ? " · v" + n.customRevision : "") +
        "</small></header>" +
        Array.from({ length: Math.max(1, controlPorts(n).length) }, (_, i) => {
          const port = controlPorts(n)[i];
          return (
            '<div class="wf-control" data-outlet="' +
            (port?.id || "") +
            '">' +
            (i === 0 && !["start", "literal", "lookup"].includes(n.kind)
              ? portHTML(n.id, "", "in", "control")
              : "") +
            "<span>" +
            esc(port?.name || "执行入口") +
            "</span>" +
            (port ? portHTML(n.id, port.id, "out", "control") : "") +
            "</div>"
          );
        }).join("") +
        Array.from({ length: height }, (_, i) => {
          const a = inputs(n)[i],
            b = n.kind === "start" ? draft.parameters[i] : displayOutputs(n)[i];
          return (
            '<div class="wf-port-row">' +
            (a ? nodeParameter(n, a, "in") : "<span></span>") +
            (b ? nodeParameter(n, b, "out") : "<span></span>") +
            "</div>"
          );
        }).join("") +
        (n.kind === "literal" || n.kind === "lookup"
          ? '<small class="wf-node-value">' +
            esc(valueText(n.config?.value ?? n.config?.path ?? "")) +
            "</small>"
          : "") +
        "</article>"
      );
    }
    function portHTML(node, key, dir, mode = "data") {
      return (
        '<button type="button" class="wf-port ' +
        dir +
        " " +
        mode +
        '" data-port="' +
        esc(JSON.stringify({ node, field: key, dir, mode })) +
        '" aria-label="' +
        dir +
        " " +
        esc(key || "执行顺序") +
        '"></button>'
      );
    }
    function position(p) {
      const n = draft.graph.nodes.find((n) => n.id === p.node);
      if (!n) return { x: 0, y: 0 };
      const list = p.dir === "out" ? displayOutputs(n) : inputs(n),
        i = list.findIndex((f) => f.name === p.field);
      return {
        x: n.x + (p.dir === "out" ? 290 : 0),
        y:
          n.y +
          (p.mode === "control"
            ? 66 +
              (p.dir === "out"
                ? Math.max(
                    0,
                    controlPorts(n).findIndex(
                      (port) => port.id === (p.field || "success"),
                    ),
                  ) * 35
                : 0)
            : 113 +
              (Math.max(1, controlPorts(n).length) - 1) * 35 +
              Math.max(0, i) * 54),
      };
    }
    function curve(a, b) {
      const m = Math.max(70, Math.abs(b.x - a.x) / 2);
      return (
        "M" +
        a.x +
        " " +
        a.y +
        " C" +
        (a.x + m) +
        " " +
        a.y +
        "," +
        (b.x - m) +
        " " +
        b.y +
        "," +
        b.x +
        " " +
        b.y
      );
    }
    function allLinks() {
      return [
        ...draft.graph.links.map((e, i) => ({
          key: "data:" + i,
          a: { ...e.from, dir: "out", mode: "data" },
          b: { ...e.to, dir: "in", mode: "data" },
        })),
        ...draft.graph.dependencies.map((e, i) => ({
          key: "control:" + i,
          a: {
            node: e.from,
            field: e.outlet || "success",
            dir: "out",
            mode: "control",
          },
          b: { node: e.to, dir: "in", mode: "control" },
        })),
      ];
    }
    function linksHTML() {
      return allLinks()
        .map((l) => {
          const a = position(l.a),
            b = position(l.b);
          return (
            '<path class="wf-link-hit" d="' +
            curve(a, b) +
            '" data-link="' +
            l.key +
            '"/><path class="' +
            (l.key.startsWith("control")
              ? "control " + (l.a.field === "failure" ? "failure " : "")
              : "") +
            (selectedLink === l.key ? "selected" : "") +
            '" d="' +
            curve(a, b) +
            '" data-link="' +
            l.key +
            '"/>' +
            (selectedLink === l.key
              ? [
                  ["source", a],
                  ["target", b],
                ]
                  .map(
                    ([side, p]) =>
                      '<circle r="7" cx="' +
                      (p.x + (side === "source" ? 20 : -20)) +
                      '" cy="' +
                      p.y +
                      '" data-handle="' +
                      side +
                      '" data-link="' +
                      l.key +
                      '"/>',
                  )
                  .join("")
              : "")
          );
        })
        .join("");
    }
    function inspectorHTML() {
      const n = draft.graph.nodes.find((n) => n.id === selected);
      if (!n) return "<p>选择节点查看参数或说明。</p>";
      const nodeDocs = {
        wait_for:
          "选择本流程中的其他节点作为等待对象。完成可包含成功或失败，跳过不算完成；成功模式只接受成功结果。所有前驱状态确定后按全部或任一条件继续，互相等待的环路不能保存。当前按依赖顺序执行。",
        switch:
          "按明确类型匹配规则，首条或所有匹配；其他情况处理未命中，规则 ID 保持连线稳定。",
        merge:
          "汇合控制路径。任一模式接受有效分支；全部模式要求每条入线都被触发。",
        wait: "等待 seconds 秒，可取消，最大 86400 秒。",
        terminate:
          "立即结束剩余流程。显式成功可恢复失败分支，原失败节点记录保留。",
        arithmetic:
          "对 a、b 做数值运算；取整和绝对值只使用 a。除零与非有限结果走失败出口。",
        compare: "按原类型比较 a、b，不将字符串隐式转换为数字。",
        logic: "使用布尔 a、b；NOT 只使用 a。",
        git: "拉取指定 Git 分支，输出实际提交和源码目录。保留仓库 URL 的 HTTP/HTTPS/SSH 协议；跳过 TLS 验证会带来中间人攻击风险。",
        maven:
          "在持久工作目录调用 Maven，goals 为 JSON 数组。工作区与依赖缓存保留；输出生成的 JAR 路径。",
        go: "使用本机 Go 工具链构建，配置目标系统、架构、CGO 和 ldflags，输出二进制路径。",
        image_build:
          "使用 Docker CLI 和当前 daemon 制作镜像。Registry 的 HTTP/TLS/证书策略由选定 Docker daemon 管理。",
        image_push:
          "推送镜像并返回实际 digest。dockerContext 选择 daemon；dockerConfig 引用主机上的 Docker 凭据目录。Registry 明文/TLS 和跳过验证由 daemon 配置，跳过验证存在中间人攻击风险。",
        image_pull:
          "使用选定 Docker context/config 拉取镜像，输出实际镜像 ID。保留 daemon 的 Registry 连接策略。",
        k3d: "对指定本机 k3d 集群应用清单，可导入镜像并等待部署。使用独立临时 kubeconfig，不切换主机当前上下文。",
        k3s: "使用指定 kubeconfig/context/namespace 部署。server 可保留 HTTP/HTTPS；显式跳过证书验证存在中间人攻击风险。",
        condition: "按布尔值触发 true 或 false 出口，只跳过未选择的下游。",
        script:
          "执行主机上的普通脚本，arguments 为 JSON 数组。通过输入/输出 JSON 文件传递结构化参数和结果。",
        start:
          "在 Entry 中填写参数。这里的默认值用于初始化 Entry，每个字段有独立输出端口。",
        end: "输入字段组成运行结果对象，每个字段有独立输入端口。",
        literal: "将配置的 JSON 值提供给下游；value 输出类型应与值一致。",
        lookup:
          "读取本次运行快照：entry.name 支持对象路径，variables.NAME 读取明确引用的非密码变量。",
      };
      const definition = types().find(
        (t) =>
          t.kind === n.kind &&
          (n.kind !== "custom" || t.customId === n.customId),
      );
      return (
        '<nav class="wf-inspector-tabs" role="tablist" aria-label="节点信息">' +
        ["params", "docs"]
          .map(
            (t) =>
              '<a href="#wf-node-panel" role="tab" id="wf-inspect-' +
              t +
              '" aria-controls="wf-node-panel" aria-selected="' +
              (inspectorTab === t) +
              '" tabindex="' +
              (inspectorTab === t ? 0 : -1) +
              '" data-action="inspect-' +
              t +
              '">' +
              (t === "params" ? "参数" : "说明") +
              "</a>",
          )
          .join("") +
        '</nav><section id="wf-node-panel" role="tabpanel" aria-labelledby="wf-inspect-' +
        inspectorTab +
        '"><h2>' +
        esc(n.name || n.kind) +
        "</h2>" +
        (inspectorTab === "docs"
          ? "<p>" +
            esc(
              definition?.description ||
                nodeDocs[n.kind] ||
                "通过输入端口接收参数，输出端口为下游提供结构化结果。",
            ) +
            "</p><p>已接线参数使用上游值，不回退本地值；右键端口可断开。执行顺序与数据依赖共同参与环路校验。</p>"
          : input("节点名称", "node.name", n.name || "") +
            controlSettings(n) +
            inputs(n)
              .map((f) => {
                const link = draft.graph.links.find(
                  (l) => l.to.node === n.id && l.to.field === f.name,
                );
                return !link && f.source === "link_only"
                  ? "<p>" + esc(f.name) + " · 仅接受连线</p>"
                  : link
                    ? '<label class="wf-field">' +
                      esc(f.name) +
                      "<code>" +
                      esc(link.from.node + "." + link.from.field) +
                      "</code>" +
                      button("disconnect", "断开", f.name) +
                      "</label>"
                    : input(
                        f.name + " · " + f.type + (f.required ? " · 必填" : ""),
                        "value." + f.name,
                        valueText(n.values?.[f.name] ?? f.default ?? ""),
                      );
              })
              .join("") +
            (["literal", "lookup"].includes(n.kind)
              ? input(
                  n.kind === "literal"
                    ? "固定值 JSON"
                    : "取值路径（entry.version / variables.NAME）",
                  "config." + (n.kind === "literal" ? "value" : "path"),
                  n.kind === "literal"
                    ? JSON.stringify(n.config?.value ?? "")
                    : n.config?.path || "",
                )
              : "") +
            (![
              "start",
              "end",
              "literal",
              "lookup",
              "switch",
              "merge",
              "wait",
              "wait_for",
              "terminate",
              "arithmetic",
              "compare",
              "logic",
              "condition",
            ].includes(n.kind)
              ? input(
                  "工作目录",
                  "config.directory",
                  n.config?.directory || "",
                ) +
                input(
                  "超时（秒）",
                  "config.timeout",
                  n.config?.timeout || 1800,
                  "number",
                ) +
                input(
                  "内存限制（如 512M）",
                  "config.memory",
                  n.config?.memory || "",
                ) +
                input(
                  "环境变量 JSON（敏感配置使用主机文件）",
                  "config.environment",
                  JSON.stringify(n.config?.environment || {}),
                )
              : "") +
            ([
              "start",
              "end",
              "script",
              "literal",
              "lookup",
              "arithmetic",
              "compare",
              "logic",
            ].includes(n.kind)
              ? button(
                  "node-fields",
                  n.kind === "start"
                    ? "入口参数"
                    : n.kind === "end"
                      ? "出口参数"
                      : "编辑输入输出参数",
                )
              : "") +
            "<h3>输出端口</h3>" +
            displayOutputs(n)
              .map((f) => "<p>" + esc(f.name) + " · " + f.type + "</p>")
              .join("") +
            (!["start", "end"].includes(n.kind)
              ? button("node-copy", "复制节点") +
                button("node-delete", "删除节点")
              : "")) +
        "</section>"
      );
    }
    function transform() {
      const world = q(".wf-world"),
        stage = q(".wf-stage");
      if (world) {
        // Apply coordinates through CSSOM so strict CSP also permits restored and arranged positions.
        for (const el of world.querySelectorAll(".wf-node")) {
          const n = draft.graph.nodes.find((n) => n.id === el.dataset.node);
          if (n) {
            el.style.left = n.x + "px";
            el.style.top = n.y + "px";
          }
        }
        world.style.transform =
          "translate(" +
          camera.x +
          "px," +
          camera.y +
          "px) scale(" +
          camera.z +
          ")";
        const maxX = Math.max(1400, ...draft.graph.nodes.map((n) => n.x + 400)),
          maxY = Math.max(
            900,
            ...draft.graph.nodes.map(
              (n) =>
                n.y +
                240 +
                Math.max(1, controlPorts(n).length) * 35 +
                Math.max(inputs(n).length, displayOutputs(n).length) * 70,
            ),
          );
        world.style.width = maxX + "px";
        world.style.height = maxY + "px";
        stage.style.backgroundPosition = camera.x + "px " + camera.y + "px";
      }
    }
    function showError(error) {
      if (modal && drawer.open) {
        modal.error = error.message;
        renderDrawer();
      } else toast(error.message);
    }
    function statusLabel(status) {
      return (
        {
          queued: "排队中",
          running: "运行中",
          dispatching: "准备执行",
          retrying: "等待重试",
          cancelling: "取消中",
          succeeded: "成功",
          failed: "失败",
          cancelled: "已取消",
          canceled: "已取消",
          skipped: "已跳过",
          pending: "待执行",
          needs_attention: "待核查",
        }[status] || status
      );
    }
    function schemaRows(list, side) {
      if (!list.length)
        return '<p class="wf-schema-empty">暂无参数，点击下方按钮添加。</p>';
      return list
        .map((f, i) => {
          const attr = (key) =>
            'data-schema="' + side + ":" + i + ":" + key + '"';
          const entry =
            modal?.kind === "fields" &&
            draft?.graph.nodes.find((n) => n.id === selected)?.kind === "start";
          const local = side === "inputs" || entry;
          return (
            '<fieldset class="wf-schema-row" data-row="' +
            side +
            i +
            '"><legend>参数 ' +
            (i + 1) +
            '</legend><div class="wf-schema-grid">' +
            "<label>参数名称<input " +
            attr("name") +
            ' value="' +
            esc(f.name) +
            '" placeholder="例如 version"></label>' +
            "<label>参数类型<select " +
            attr("type") +
            ">" +
            ["string", "integer", "number", "boolean", "json"]
              .map(
                (t) =>
                  "<option " +
                  (t === f.type ? "selected" : "") +
                  ">" +
                  t +
                  "</option>",
              )
              .join("") +
            "</select></label>" +
            (local && !entry
              ? "<label>取值方式<select " +
                attr("source") +
                '><option value="local_or_link">本地或连线</option><option value="link_only" ' +
                (f.source === "link_only" ? "selected" : "") +
                ">仅连线</option></select></label>"
              : "") +
            (local
              ? "<label>默认值<input " +
                attr("default") +
                ' value="' +
                esc(modal.defaultText?.get(f) ?? valueText(f.default ?? "")) +
                '" placeholder="可选，按参数类型填写"></label>'
              : "") +
            "<label>枚举选项<input " +
            attr("enum") +
            ' value="' +
            esc((f.enum || []).join(",")) +
            '" placeholder="仅字符串，使用逗号分隔"></label></div><div class="wf-schema-actions"><label class="wf-check"><input type="checkbox" ' +
            attr("required") +
            " " +
            (f.required ? "checked" : "") +
            ">必填</label>" +
            button("field-remove", "删除参数", side + ":" + i, "danger") +
            "</div></fieldset>"
          );
        })
        .join("");
    }
    function logLines(events) {
      return (
        (events || [])
          .map(
            (e) =>
              '<div class="wf-log-line"><time>' +
              esc(new Date(e.time).toLocaleTimeString()) +
              "</time><span>" +
              esc(e.source) +
              "</span><pre>" +
              esc(e.text) +
              "</pre></div>",
          )
          .join("") || '<p class="wf-log-empty">暂无日志输出。</p>'
      );
    }
    function showModal(kind, value) {
      modal = {
        kind,
        value: clone(value),
        dirty: false,
        defaultText: new Map(),
      };
      renderDrawer();
      drawer.getAnimations().forEach((a) => a.cancel());
      drawer.showModal();
      if (!matchMedia("(prefers-reduced-motion: reduce)").matches)
        drawer.animate(
          [
            {
              opacity: 0,
              transform:
                "translateX(" +
                getComputedStyle(drawer)
                  .getPropertyValue("--drawer-offset")
                  .trim() +
                ")",
            },
            { opacity: 1, transform: "translateX(0)" },
          ],
          { duration: 130, easing: "ease-out" },
        );
    }
    async function closeDrawer() {
      if (!drawer.open || drawer.dataset.closing) return;
      modal = null;
      drawer.dataset.closing = "true";
      if (!matchMedia("(prefers-reduced-motion: reduce)").matches) {
        const animation = drawer.animate(
          [
            { opacity: 1, transform: "translateX(0)" },
            {
              opacity: 0,
              transform:
                "translateX(" +
                getComputedStyle(drawer)
                  .getPropertyValue("--drawer-offset")
                  .trim() +
                ")",
            },
          ],
          { duration: 100, easing: "ease-in" },
        );
        try {
          await animation.finished;
        } catch {}
      }
      drawer.close();
      delete drawer.dataset.closing;
    }
    function requestDrawerClose() {
      if (modal?.saving) return toast("正在导入，请稍候。");
      if (modal?.dirty && !confirm("放弃未保存的修改？")) return;
      return closeDrawer();
    }
    function branchRulesHTML(v) {
      const operations = [
        ["eq", "等于"],
        ["ne", "不等于"],
        ["gt", "大于"],
        ["gte", "大于等于"],
        ["lt", "小于"],
        ["lte", "小于等于"],
        ["between", "范围（含边界）"],
        ["contains", "包含"],
        ["starts_with", "前缀"],
        ["matches", "正则匹配"],
        ["exists", "存在"],
        ["missing", "不存在"],
        ["is_null", "为空"],
        ["not_null", "非空"],
      ];
      return (
        '<label class="wf-field">判断类型<select data-field="valueType">' +
        ["boolean", "integer", "number", "string", "json"]
          .map(
            (t) =>
              "<option " +
              (v.valueType === t ? "selected" : "") +
              ">" +
              t +
              "</option>",
          )
          .join("") +
        '</select></label><label class="wf-field">匹配方式<select data-field="matchMode"><option value="first">首条匹配</option><option value="all" ' +
        (v.matchMode === "all" ? "selected" : "") +
        ">所有匹配</option></select></label>" +
        input("JSON 字段路径（可选，例如 result.code）", "path", v.path || "") +
        v.rules
          .map(
            (r, i) =>
              '<fieldset class="wf-schema-row wf-branch-rule"><legend>分支 ' +
              (i + 1) +
              '</legend><div class="wf-schema-grid"><label>出口名称<input data-rule="' +
              i +
              ':name" value="' +
              esc(r.name) +
              '"></label><label>判断<select data-rule="' +
              i +
              ':operator">' +
              operations
                .map(
                  ([op, label]) =>
                    '<option value="' +
                    op +
                    '" ' +
                    (op === r.operator ? "selected" : "") +
                    ">" +
                    label +
                    "</option>",
                )
                .join("") +
              '</select></label><label>比较值<input data-rule="' +
              i +
              ':value" value="' +
              esc(r.value ?? "") +
              '" placeholder="存在性判断不需要填写"></label><label>范围上限<input data-rule="' +
              i +
              ':upper" value="' +
              esc(r.upper ?? "") +
              '" placeholder="仅范围判断使用"></label></div><div class="wf-schema-actions">' +
              button("rule-up", "上移", String(i)) +
              button("rule-down", "下移", String(i)) +
              button("rule-delete", "删除", String(i)) +
              "</div></fieldset>",
          )
          .join("") +
        button("rule-add", "新增分支") +
        "<p>规则按顺序判断。“其他情况”处理未命中的值。类型不合法或判断出错走失败出口。</p>"
      );
    }
    function renderDrawer() {
      if (!modal) return;
      const v = modal.value;
      let body = "";
      if (modal.kind === "import") {
        let summary = "选择文件或粘贴 JSON 后点击“检查配置”。";
        if (v.text) {
          try {
            const b = importPreview(v.text);
            summary =
              b.workflow.name +
              " · " +
              b.workflow.graph.nodes.length +
              " 个节点 · " +
              b.customNodes.length +
              " 个自定义版本";
          } catch (err) {
            summary = err.message;
          }
        }
        body =
          '<label class="wf-field">选择 JSON 文件<input type="file" data-workflow-file accept=".json,application/json"></label><label class="wf-field">配置内容<textarea class="wf-code" data-field="text" placeholder="粘贴完整工作流 JSON">' +
          esc(v.text || "") +
          "</textarea></label>" +
          button("import-preview", "检查配置") +
          '<p role="status">' +
          esc(summary) +
          "</p><p>确认后创建新工作流及引用的自定义节点；导入不会执行任务。</p>";
      }
      if (modal.kind === "custom") {
        body =
          input("节点名称", "name", v.name) +
          input("说明", "description", v.description) +
          '<label class="wf-field">语言<select data-field="language">' +
          ["python", "javascript", "shell", "powershell"]
            .map(
              (l) =>
                "<option " +
                (v.language === l ? "selected" : "") +
                ">" +
                l +
                "</option>",
            )
            .join("") +
          "</select></label><h3>输入参数</h3>" +
          schemaRows(v.inputs, "inputs") +
          button("field-add", "新增输入", "inputs") +
          "<h3>输出参数</h3>" +
          schemaRows(v.outputs, "outputs") +
          button("field-add", "新增输出", "outputs") +
          '<label class="wf-field">脚本正文<textarea class="wf-code" data-field="code">' +
          esc(v.code) +
          "</textarea></label><p>Python: main(inputs) 返回字典；JavaScript: export async function main(inputs) 返回对象。Shell/PowerShell 从 SCRIPTBOARD_INPUT_FILE 读取 JSON，写入 SCRIPTBOARD_OUTPUT_FILE。脚本存储于 ScriptBoard。</p>";
      }
      if (modal.kind === "custom" && v.id && manage) {
        body +=
          '<section class="wf-custom-danger" aria-labelledby="wf-custom-delete-title"><div><h3 id="wf-custom-delete-title">删除节点</h3><p>从自定义节点列表移除。已保存的历史版本和运行记录保留。</p></div>' +
          button(
            "custom-delete",
            "删除自定义节点",
            v.id,
            "button button--danger",
          ) +
          "</section>";
      }
      if (modal.kind === "entry") {
        const f = state.published.find((f) => f.id === v.workflowId);
        body =
          input("Entry 名称", "name", v.name) +
          input("分组", "group", v.group) +
          '<label class="wf-field">Workflow<select data-entry-flow>' +
          state.published
            .map(
              (f) =>
                '<option value="' +
                f.id +
                '" ' +
                (v.workflowId === f.id ? "selected" : "") +
                ">" +
                esc(f.name) +
                " · v" +
                f.revision +
                "</option>",
            )
            .join("") +
          "</select></label>" +
          (f?.parameters || [])
            .map((p) =>
              parameterControl(
                p,
                "entry-value." + p.name,
                v.values[p.name] ?? p.default,
              ),
            )
            .join("") +
          input("互斥标识（逗号分隔）", "locks", v.locks.join(", ")) +
          input("排序", "order", v.order || 0, "number") +
          '<label class="wf-entry-confirm"><input type="checkbox" data-field="confirm" ' +
          (v.confirm ? "checked" : "") +
          ">运行前二次确认</label>";
      }
      if (modal.kind === "fields") {
        const kind = draft.graph.nodes.find((n) => n.id === selected).kind;
        body =
          (["start", "literal", "lookup"].includes(kind)
            ? ""
            : "<h3>输入参数</h3>" +
              schemaRows(v.inputs, "inputs") +
              button("field-add", "新增输入", "inputs")) +
          (kind === "end"
            ? ""
            : "<h3>输出参数</h3>" +
              schemaRows(v.outputs, "outputs") +
              (["literal", "lookup"].includes(kind)
                ? "<p>保留唯一输出名称 value，可调整类型。</p>"
                : button("field-add", "新增输出", "outputs")));
      }
      if (modal.kind === "wait-targets")
        body =
          '<label class="wf-field">完成判定<select data-field="outlet"><option value="always" ' +
          (v.outlet === "always" ? "selected" : "") +
          '>执行完成（成功或失败）</option><option value="success" ' +
          (v.outlet === "success" ? "selected" : "") +
          '>执行成功</option></select></label><div class="wf-wait-targets">' +
          draft.graph.nodes
            .filter(
              (n) =>
                n.id !== selected && !["end", "terminate"].includes(n.kind),
            )
            .map(
              (n) =>
                '<label class="wf-entry-confirm"><input type="checkbox" data-wait-target="' +
                esc(n.id) +
                '" ' +
                (v.targets.includes(n.id) ? "checked" : "") +
                "><span>" +
                esc(n.name || n.kind) +
                "<small> · " +
                esc(n.kind) +
                "</small></span></label>",
            )
            .join("") +
          "</div><p>选择至少一个节点。等待关系以控制连线保存，不允许循环依赖。</p>";
      if (modal.kind === "rules") body = branchRulesHTML(v);
      if (modal.kind === "logs")
        body =
          '<div class="wf-log-toolbar"><span>' +
          (v.loading
            ? "正在加载日志…"
            : v.error
              ? esc(v.error)
              : "最近 1000 条 · 每 2.5 秒更新") +
          '</span><a class="button" download href="/history/runs/' +
          encodeURIComponent(v.id) +
          '/download">下载完整日志</a></div><div class="wf-log-lines" tabindex="0" aria-label="日志输出">' +
          logLines(v.events) +
          "</div>";
      const nodeKind =
        modal.kind === "fields"
          ? draft.graph.nodes.find((n) => n.id === selected)?.kind
          : "";
      const title = {
        import: "导入工作流",
        "wait-targets": "等待节点",
        rules: "分支规则",
        custom: "自定义节点",
        entry: "配置 Entry",
        fields:
          nodeKind === "start"
            ? "入口参数"
            : nodeKind === "end"
              ? "出口参数"
              : "参数定义",
        logs: "运行日志",
      }[modal.kind];
      const description = {
        import: "导入配置文件，校验通过后创建新的工作流。",
        "wait-targets": "选择此节点继续执行前需要等待的任务。",
        rules: "根据输入值选择执行出口，调整顺序和名称不会断开连线。",
        custom: "配置节点的输入、输出和托管脚本。",
        entry: "选择已保存工作流，保存执行参数。",
        fields:
          nodeKind === "start"
            ? "定义执行此工作流时需要填写的参数。"
            : nodeKind === "end"
              ? "定义工作流完成时返回的参数。"
              : "定义节点连接时使用的参数名称和类型。",
        logs: v.context || "查看当前步骤的进程输出。",
      }[modal.kind];
      drawer.setAttribute("aria-labelledby", "wf-drawer-title");
      drawer.dataset.kind = modal.kind;
      html(
        drawer,
        '<header><div><p>工作流</p><h2 id="wf-drawer-title">' +
          title +
          "</h2><small>" +
          esc(description) +
          '</small></div><button type="button" class="icon-button wf-close" data-action="drawer-close" aria-label="关闭抽屉"><svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"/></svg></button></header><section>' +
          (modal.error
            ? '<p class="wf-form-error" role="alert">' +
              esc(modal.error) +
              "</p>"
            : "") +
          body +
          "</section><footer>" +
          button("drawer-close", modal.kind === "logs" ? "关闭" : "取消") +
          (modal.kind !== "logs"
            ? button(
                "drawer-save",
                modal.kind === "import" ? "导入" : "保存",
                "",
                "primary",
              )
            : "") +
          "</footer>",
      );
    }
    async function saveModal() {
      const v = modal.value;
      if (modal.kind === "import") {
        if (modal.saving) return;
        const current = modal;
        const bundle = importPreview(v.text || "");
        current.saving = true;
        const submit = drawer.querySelector('[data-action="drawer-save"]');
        submit.disabled = true;
        submit.textContent = "导入中…";
        try {
          const saved = await api("import", bundle);
          await closeDrawer();
          draft = saved;
          dirty = false;
          undo = [];
          redo = [];
          selected = saved.graph.nodes[0]?.id;
          setTab("editor");
          await refresh();
          fitGraph();
          toast("工作流已导入，可配置 Entry 后运行。");
        } finally {
          current.saving = false;
          if (modal === current) renderDrawer();
        }
        return;
      }
      if (["custom", "fields"].includes(modal.kind)) {
        for (const side of ["inputs", "outputs"]) {
          const names = new Set();
          for (const f of v[side] || []) {
            if (!f.name.trim()) throw Error("请填写参数名称");
            if (names.has(f.name))
              throw Error("同一侧的参数名称不能重复：" + f.name);
            names.add(f.name);
            // Parse the displayed value again after type changes; never save an older parsed value.
            const text = modal.defaultText.get(f);
            if (text !== undefined || f.default !== undefined) {
              try {
                f.default = parseValue(text ?? valueText(f.default), f.type);
              } catch {
                throw Error(
                  "参数 " + f.name + " 的默认值需要符合 " + f.type + " 类型",
                );
              }
            }
            if (f.enum?.length && f.type !== "string")
              throw Error("参数 " + f.name + " 的枚举选项仅适用于字符串");
          }
        }
      }
      if (modal.kind === "wait-targets") {
        if (!v.targets.length) throw Error("至少选择一个等待节点");
        const graph = clone(draft.graph);
        graph.dependencies = graph.dependencies
          .filter((e) => e.to !== selected)
          .concat(
            v.targets.map((id) => ({
              from: id,
              to: selected,
              outlet:
                draft.graph.nodes.find((n) => n.id === id).kind === "start"
                  ? "success"
                  : v.outlet,
            })),
          );
        if (hasCycle(graph)) throw Error("等待关系会产生循环，请选择其他节点");
        checkpoint();
        draft.graph = graph;
      } else if (modal.kind === "rules") {
        if (!v.rules.length) throw Error("至少保留一条分支规则");
        const rules = v.rules.map((r) => {
          if (!r.name.trim()) throw Error("请填写出口名称");
          const unary = ["exists", "missing", "is_null", "not_null"].includes(
            r.operator,
          );
          const value = unary ? undefined : parseValue(r.value, v.valueType);
          if (!unary && value === undefined) throw Error("请填写比较值");
          return {
            id: r.id,
            name: r.name,
            operator: r.operator,
            ...(!unary ? { value } : {}),
            ...(r.operator === "between"
              ? { upper: parseValue(r.upper, v.valueType) }
              : {}),
          };
        });
        checkpoint();
        const n = draft.graph.nodes.find((n) => n.id === selected);
        Object.assign(n.config, {
          rules,
          valueType: v.valueType,
          matchMode: v.matchMode,
          path: v.path || "",
        });
        const valid = new Set(controlPorts(n).map((p) => p.id));
        valid.add("success");
        draft.graph.dependencies = draft.graph.dependencies.filter(
          (e) => e.from !== n.id || valid.has(e.outlet || "success"),
        );
      } else if (modal.kind === "custom") {
        await api("custom", v);
      } else if (modal.kind === "entry") {
        const f = state.published.find((f) => f.id === v.workflowId);
        if (!f) throw Error("请选择已保存的工作流");
        for (const el of drawer.querySelectorAll(
          '[data-field^="entry-value."]',
        )) {
          const name = el.dataset.field.slice(12),
            parameter = f.parameters.find((p) => p.name === name);
          try {
            v.values[name] = parseValue(el.value, parameter.type);
          } catch {
            throw Error(
              "参数 " + name + " 需要符合 " + parameter.type + " 类型",
            );
          }
        }
        v.workflowRevision = f.revision;
        await api("entry", v);
      } else if (modal.kind === "fields") {
        checkpoint();
        const n = draft.graph.nodes.find((n) => n.id === selected);
        if (n.kind === "start") {
          draft.parameters = v.outputs.map((f) => ({
            ...f,
            source: "local_or_link",
          }));
          n.outputs = clone(draft.parameters);
        } else {
          n.inputs = v.inputs;
          n.outputs = v.outputs;
        }
        draft.graph.links = draft.graph.links.filter((l) => {
          const a = draft.graph.nodes.find((n) => n.id === l.from.node),
            b = draft.graph.nodes.find((n) => n.id === l.to.node);
          return (
            displayOutputs(a).some((f) => f.name === l.from.field) &&
            inputs(b).some((f) => f.name === l.to.field)
          );
        });
      }
      await closeDrawer();
      await refresh();
    }
    function newDraft() {
      return {
        id: "",
        revision: 0,
        name: "新工作流",

        parameters: [field("version", "string", "dev", true)],
        graph: {
          nodes: [
            {
              id: uid(),
              kind: "start",
              name: "入口参数",
              x: 40,
              y: 80,
              inputs: [],
              outputs: [out("version")],
              values: {},
              config: {},
            },
            {
              id: uid(),
              kind: "end",
              name: "出口参数",
              x: 720,
              y: 100,
              inputs: [],
              outputs: [],
              values: {},
              config: {},
            },
          ],
          links: [],
          dependencies: [],
        },
      };
    }
    function makeNode(type, p) {
      return {
        id: uid(),
        kind: type.kind,
        name: type.name,
        x: p.x,
        y: p.y,
        inputs: clone(type.inputs || []),
        outputs: clone(type.outputs || []),
        values: {},
        config: type.config
          ? clone(type.config)
          : type.kind === "literal"
            ? { value: "" }
            : type.kind === "lookup"
              ? { path: "version" }
              : {},
        ...(type.customId
          ? { customId: type.customId, customRevision: type.customRevision }
          : {}),
      };
    }
    function linkType(p, nodes = draft.graph.nodes) {
      if (p.mode === "control") return "control";
      const n = nodes.find((n) => n.id === p.node);
      return (p.dir === "out" ? displayOutputs(n) : inputs(n)).find(
        (f) => f.name === p.field,
      )?.type;
    }
    function compatible(a, b, nodes = draft.graph.nodes) {
      if (a.node === b.node || a.mode !== b.mode || a.dir === b.dir)
        return false;
      const out = a.dir === "out" ? a : b,
        inside = a.dir === "in" ? a : b;
      return (
        linkType(out, nodes) === linkType(inside, nodes) ||
        (linkType(out, nodes) === "integer" &&
          linkType(inside, nodes) === "number") ||
        linkType(inside, nodes) === "json"
      );
    }
    function removeLink(key, g = draft.graph) {
      const [kind, i] = key.split(":");
      (kind === "data" ? g.links : g.dependencies).splice(Number(i), 1);
    }
    function connect(a, b, replace = null) {
      if (!compatible(a, b)) throw Error("端口方向或类型不兼容");
      const before = clone(draft.graph),
        out = a.dir === "out" ? a : b,
        inside = a.dir === "in" ? a : b;
      if (replace) removeLink(replace);
      if (a.mode === "data") {
        draft.graph.links = draft.graph.links.filter(
          (l) => l.to.node !== inside.node || l.to.field !== inside.field,
        );
        draft.graph.links.push({
          from: { node: out.node, field: out.field },
          to: { node: inside.node, field: inside.field },
        });
      } else if (
        !draft.graph.dependencies.some(
          (l) =>
            l.from === out.node &&
            l.to === inside.node &&
            (l.outlet || "success") === (out.field || "success"),
        )
      )
        draft.graph.dependencies.push({
          from: out.node,
          to: inside.node,
          outlet: out.field || "success",
        });
      if (hasCycle()) {
        draft.graph = before;
        throw Error("连线形成循环依赖");
      }
    }
    function hasCycle(graph = draft.graph) {
      const counts = new Map(graph.nodes.map((n) => [n.id, 0])),
        edges = [
          ...graph.dependencies,
          ...graph.links.map((l) => ({
            from: l.from.node,
            to: l.to.node,
          })),
        ];
      for (const e of edges) counts.set(e.to, (counts.get(e.to) || 0) + 1);
      const ready = [...counts].filter(([, c]) => !c).map(([id]) => id);
      let count = 0;
      while (ready.length) {
        const id = ready.shift();
        count++;
        for (const e of edges.filter((e) => e.from === id)) {
          counts.set(e.to, counts.get(e.to) - 1);
          if (!counts.get(e.to)) ready.push(e.to);
        }
      }
      return count !== counts.size;
    }
    // Dismissing the picker cancels only its pending connection; existing edges stay intact.
    function closeSearch() {
      searchContext = null;
      wire = null;
      const preview = q(".wf-wire");
      if (preview) preview.innerHTML = "";
      if (search.open) search.close();
    }
    function openSearch(x, y, from = null, replace = null) {
      if (!manage || !draft) return;
      const rect = q(".wf-stage").getBoundingClientRect();
      searchContext = {
        point: {
          x: (x - rect.left - camera.x) / camera.z,
          y: (y - rect.top - camera.y) / camera.z,
        },
        from,
        replace,
        choices: [],
      };
      search.innerHTML =
        '<header class="wf-search-header"><strong id="wf-search-title">选择节点类型</strong><button type="button" class="icon-button" data-action="node-search-close" aria-label="关闭节点选择"><svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"/></svg></button></header><input data-node-query aria-label="搜索节点名称或类型" placeholder="搜索节点名称 / 类型"><div data-node-results></div>';
      search.setAttribute("aria-labelledby", "wf-search-title");
      search.style.left =
        Math.max(10, Math.min(x, window.innerWidth - 350)) + "px";
      search.style.top =
        Math.max(10, Math.min(y, window.innerHeight - 410)) + "px";
      search.showModal();
      renderSearch("");
      search.querySelector("input").focus();
    }
    function renderSearch(query) {
      const choices = [];
      for (const type of types().filter((t) =>
        (t.name + " " + t.group + " " + t.kind)
          .toLowerCase()
          .includes(query.toLowerCase()),
      )) {
        if (!searchContext.from) choices.push({ type });
        else {
          const n = makeNode(type, searchContext.point),
            from = searchContext.from,
            dir = from.dir === "out" ? "in" : "out",
            ports =
              from.mode === "control"
                ? ["literal", "lookup"].includes(n.kind)
                  ? []
                  : [
                      {
                        node: n.id,
                        mode: "control",
                        dir,
                        field: dir === "out" ? controlPorts(n)[0]?.id : "",
                      },
                    ]
                : (dir === "in" ? inputs(n) : displayOutputs(n)).map((f) => ({
                    node: n.id,
                    field: f.name,
                    dir,
                    mode: "data",
                  }));
          for (const p of ports)
            if (compatible(from, p, [...draft.graph.nodes, n]))
              choices.push({ type, port: p.field, dir, mode: p.mode });
        }
      }
      const groups = [...new Set(choices.map((c) => c.type.group))];
      searchContext.choices = groups.flatMap((g) =>
        choices.filter((c) => c.type.group === g),
      );
      let i = 0;
      search.querySelector("[data-node-results]").innerHTML =
        groups
          .map(
            (g) =>
              "<h4>" +
              esc(g) +
              "</h4>" +
              choices
                .filter((c) => c.type.group === g)
                .map((c) =>
                  button(
                    "choose-node",
                    esc(c.type.name) + (c.port ? " · " + esc(c.port) : ""),
                    String(i++),
                  ),
                )
                .join(""),
          )
          .join("") || "<p>没有兼容节点</p>";
    }
    function chooseNode(index) {
      const c = searchContext.choices[index];
      if (!c) return;
      checkpoint();
      const n = makeNode(c.type, searchContext.point);
      draft.graph.nodes.push(n);
      try {
        if (searchContext.from)
          connect(
            searchContext.from,
            { node: n.id, field: c.port, dir: c.dir, mode: c.mode },
            searchContext.replace,
          );
      } catch (e) {
        draft = undo.pop();
        throw e;
      }
      selected = n.id;
      selectedLink = null;
      closeSearch();
      render();
    }
    root.addEventListener("click", async (e) => {
      const b = e.target.closest("[data-action],[data-wf-tab]");
      if (!b) return;
      e.preventDefault();
      b.closest(".action-menu")?.removeAttribute("open");
      try {
        if (b.dataset.wfTab) {
          setTab(b.dataset.wfTab);
          render();
          return;
        }
        const a = b.dataset.action,
          id = b.dataset.id;
        if (a === "guide-download") {
          const res = await fetch("/workflow/guide");
          if (!res.ok) throw Error("指南加载失败");
          const templates = catalog.map(
            ({ kind, name, inputs, outputs, config }) => ({
              id: "node_id",
              kind,
              name,
              x: 400,
              y: 100,
              inputs: inputs || [],
              outputs: outputs || [],
              values: {},
              config: config || {},
            }),
          );
          const fence = String.fromCharCode(96).repeat(3);
          const text = await res.text();
          downloadText(
            "scriptboard-workflow-ai-guide.md",
            text +
              "\n\n## 完整可导入示例\n\n" +
              fence +
              "json\n" +
              JSON.stringify(exampleBundle(), null, 2) +
              "\n" +
              fence +
              "\n\n## 内置节点模板（按需选取并补齐必填值）\n\n" +
              fence +
              "json\n" +
              JSON.stringify(templates, null, 2) +
              "\n" +
              fence,
            "text/markdown;charset=utf-8",
          );
          return;
        }
        if (a === "example-download") {
          downloadText(
            "example.workflow.json",
            JSON.stringify(exampleBundle(), null, 2),
            "application/json",
          );
          return;
        }
        if (a === "flow-export") {
          const bundle = await api("export", draft);
          downloadText(
            (draft.name || "workflow").replace(/[\\/:*?"<>|]/g, "_") +
              ".workflow.json",
            JSON.stringify(bundle, null, 2),
            "application/json",
          );
          return;
        }
        if (a === "flow-import") {
          if (dirty && !confirm("导入成功后将切换流程，放弃当前未保存的编辑？"))
            return;
          return showModal("import", { text: "" });
        }
        if (a === "import-preview") {
          importPreview(modal.value.text || "");
          return renderDrawer();
        }
        if (a === "node-search-close") return closeSearch();
        if (a === "refresh") return await refresh();
        if (["custom-delete", "entry-delete", "flow-delete"].includes(a)) {
          const kind =
              a === "custom-delete"
                ? "custom"
                : a === "entry-delete"
                  ? "entry"
                  : "draft",
            v = kind === "draft" ? draft : state[kind].find((v) => v.id === id);
          if (!v.id) return;
          if (!confirm("删除 " + v.name + "？历史版本与运行记录将保留。"))
            return;
          await api("delete", { kind, id: v.id, revision: v.revision });
          if (
            kind === "custom" &&
            modal?.kind === "custom" &&
            modal.value.id === id
          )
            await closeDrawer();
          if (kind === "draft") {
            draft = null;
            dirty = false;
          }
          return await refresh();
        }
        if (a === "custom-new")
          return showModal("custom", {
            id: "",
            revision: 0,
            name: "",
            description: "",
            language: "python",
            code: "def main(inputs):\n    return {}\n",
            inputs: [],
            outputs: [],
          });
        if (a === "custom-edit")
          return showModal(
            "custom",
            state.custom.find((d) => d.id === id),
          );
        if (a === "drawer-close") return await requestDrawerClose();
        if (a === "wait-targets") {
          const edges = draft.graph.dependencies.filter(
            (e) => e.to === selected,
          );
          return showModal("wait-targets", {
            targets: [...new Set(edges.map((e) => e.from))],
            outlet:
              edges.length &&
              edges.every((e) => (e.outlet || "success") === "success")
                ? "success"
                : "always",
          });
        }
        if (a === "branch-rules") {
          const n = draft.graph.nodes.find((n) => n.id === selected);
          return showModal("rules", {
            valueType: n.config.valueType || "boolean",
            matchMode: n.config.matchMode || "first",
            path: n.config.path || "",
            rules: (n.config.rules || []).map((r) => ({
              ...r,
              value: valueText(r.value ?? ""),
              upper: valueText(r.upper ?? ""),
            })),
          });
        }
        if (a === "rule-add") {
          modal.value.rules.push({
            id: "branch_" + uid().replaceAll("-", ""),
            name: "新分支",
            operator: "eq",
            value: "",
            upper: "",
          });
          modal.dirty = true;
          return renderDrawer();
        }
        if (["rule-up", "rule-down", "rule-delete"].includes(a)) {
          const i = Number(id),
            rules = modal.value.rules;
          if (a === "rule-delete") rules.splice(i, 1);
          else {
            const to = i + (a === "rule-up" ? -1 : 1);
            if (to >= 0 && to < rules.length) {
              const [r] = rules.splice(i, 1);
              rules.splice(to, 0, r);
            }
          }
          modal.dirty = true;
          return renderDrawer();
        }
        if (a === "drawer-save") return await saveModal();
        if (a === "field-add") {
          modal.value[id].push(field("", "string", undefined, false));
          modal.dirty = true;
          return renderDrawer();
        }
        if (a === "field-remove") {
          const [side, i] = id.split(":");
          modal.value[side].splice(Number(i), 1);
          modal.dirty = true;
          return renderDrawer();
        }
        if (a === "entry-workflow") {
          const entry = state.entry.find((e) => e.id === id),
            flow = state.draft.find((f) => f.id === entry?.workflowId);
          if (!flow) return toast("对应工作流已不可用");
          if (dirty && draft?.id !== flow.id && !confirm("放弃未保存工作流？"))
            return;
          if (draft?.id !== flow.id) {
            draft = clone(flow);
            dirty = false;
            undo = [];
            redo = [];
            selected = draft.graph.nodes[0]?.id;
          }
          setTab("editor");
          render();
          fitGraph();
          return;
        }
        if (a === "entry-new") {
          const f = state.published[0];
          if (!f) return toast("请先保存工作流");
          return showModal("entry", {
            id: "",
            revision: 0,
            name: f.name,
            group: "默认分组",
            order: 0,
            workflowId: f.id,
            workflowRevision: f.revision,
            values: {},
            locks: [],
            locked: false,
            confirm: true,
          });
        }
        if (a === "entry-edit" || a === "entry-copy") {
          const v = clone(state.entry.find((e) => e.id === id));
          v.confirm ??=
            state.published.find((f) => f.id === v.workflowId)?.confirm ??
            false;
          if (a === "entry-edit" && v.locked) return toast("请先解锁");
          if (a === "entry-copy") {
            v.id = "";
            v.revision = 0;
            v.locked = false;
            v.name += " · 副本";
          }
          return showModal("entry", v);
        }
        if (a === "entry-lock") {
          const v = state.entry.find((e) => e.id === id);
          await api("entry-lock", {
            id,
            revision: v.revision,
            locked: !v.locked,
          });
          return await refresh();
        }
        if (a === "run") {
          const entry = state.entry.find((e) => e.id === id),
            f = state.published.find((f) => f.id === entry.workflowId);
          if (
            (entry.confirm ?? f?.confirm ?? false) &&
            !confirm("执行 " + entry.name + "？")
          )
            return;
          await api("start", { id });
          setTab("history");
          return await refresh();
        }
        if (a === "cancel" || a === "resolve") {
          if (
            a === "resolve" &&
            !confirm("确认已核查实际进程和环境，释放互斥资源？")
          )
            return;
          await api(a, { id });
          return await refresh();
        }
        if (a === "logs") {
          const run = state.runs.find((r) =>
            r.steps.some(
              (s) =>
                s.processId === id ||
                s.attempts?.some((a) => a.processId === id),
            ),
          );
          const step = run?.steps.find(
            (s) =>
              s.processId === id || s.attempts?.some((a) => a.processId === id),
          );
          const node = run?.snapshot.workflow.graph.nodes.find(
            (n) => n.id === step?.nodeId,
          );
          showModal("logs", {
            id,
            events: [],
            loading: true,
            context: [run?.snapshot.entry.name, node?.name || step?.nodeId]
              .filter(Boolean)
              .join(" / "),
          });
          const current = modal;
          current.pending = true;
          try {
            const events = await api("logs/" + encodeURIComponent(id));
            if (modal === current) {
              current.value.events = events || [];
              current.value.loading = false;
              renderDrawer();
              const pane = q(".wf-log-lines");
              if (pane) pane.scrollTop = pane.scrollHeight;
            }
          } catch (error) {
            if (modal === current) {
              current.value.loading = false;
              current.value.error = error.message;
              renderDrawer();
            }
          }
          current.pending = false;
          return;
        }
        if (a === "history-step") {
          const [runID, nodeID] = JSON.parse(id);
          historySelection.set(runID, nodeID);
          render();
          return;
        }
        if (a === "entry-history") {
          setTab("history");
          render();
          const item = document.getElementById("run-" + id);
          if (item) {
            item.open = true;
            item.scrollIntoView({ block: "nearest" });
            item.querySelector("summary").focus();
          }
          return;
        }
        if (a === "flow-new") {
          if (dirty && !confirm("放弃未保存工作流？")) return;
          draft = newDraft();
          selected = draft.graph.nodes[0].id;
          dirty = true;
          undo = [];
          redo = [];
          return render();
        }
        if (a === "flow-save") {
          if (saveBusy) return;
          const editing = draft,
            sent = clone(draft);
          saveBusy = true;
          render();
          try {
            const saved = await api("save", sent);
            if (draft === editing) {
              if (JSON.stringify(draft) === JSON.stringify(sent)) {
                draft = saved;
                dirty = false;
              } else {
                draft.id = saved.id;
                draft.revision = saved.revision;
                dirty = true;
              }
            }
            toast("已保存，可在 Entry 中选择此版本运行。");
            await refresh();
          } finally {
            saveBusy = false;
            render();
          }
          return;
        }
        if (a === "node-search") {
          const r = q(".wf-stage").getBoundingClientRect();
          return openSearch(r.left + 100, r.top + 100);
        }
        if (a === "choose-node") return chooseNode(Number(id));
        if (a === "undo" && undo.length) {
          redo.push(clone(draft));
          restoreDraft(undo.pop());
          dirty = true;
          return render();
        }
        if (a === "redo" && redo.length) {
          undo.push(clone(draft));
          restoreDraft(redo.pop());
          dirty = true;
          return render();
        }
        if (a === "arrange") return await arrange();
        if (a === "fit") return fitGraph();
        if (a === "zoom-in" || a === "zoom-out") {
          camera.z = Math.max(
            0.2,
            Math.min(1.6, camera.z + (a === "zoom-in" ? 0.1 : -0.1)),
          );
          return transform();
        }
        if (a === "inspect-params" || a === "inspect-docs") {
          inspectorTab = a === "inspect-params" ? "params" : "docs";
          return render();
        }
        // Both toolbar shortcuts resolve their endpoint before opening the shared field drawer.
        if (a === "flow-fields" || a === "flow-output-fields") {
          selected = draft.graph.nodes.find(
            (n) => n.kind === (a === "flow-fields" ? "start" : "end"),
          ).id;
        }
        if (["flow-fields", "flow-output-fields", "node-fields"].includes(a)) {
          const n = draft.graph.nodes.find((n) => n.id === selected);
          return showModal("fields", {
            inputs: clone(inputs(n)),
            outputs: clone(n.kind === "start" ? draft.parameters : outputs(n)),
          });
        }
        if (a === "disconnect") {
          checkpoint();
          draft.graph.links = draft.graph.links.filter(
            (l) => l.to.node !== selected || l.to.field !== id,
          );
          return render();
        }
        if (a === "node-copy") {
          checkpoint();
          const n = clone(draft.graph.nodes.find((n) => n.id === selected));
          n.id = uid();
          n.name += " · 副本";
          n.x += 40;
          n.y += 40;
          draft.graph.nodes.push(n);
          selected = n.id;
          return render();
        }
        if (a === "node-delete") return deleteSelection();
      } catch (err) {
        showError(err);
      }
    });
    function deleteSelection() {
      if (!manage || !draft) return;
      if (selectedLink) {
        checkpoint();
        removeLink(selectedLink);
        selectedLink = null;
      } else {
        const n = draft.graph.nodes.find((n) => n.id === selected);
        if (!n || ["start", "end"].includes(n.kind)) return;
        checkpoint();
        draft.graph.nodes = draft.graph.nodes.filter((n) => n.id !== selected);
        draft.graph.links = draft.graph.links.filter(
          (l) => l.from.node !== selected && l.to.node !== selected,
        );
        draft.graph.dependencies = draft.graph.dependencies.filter(
          (l) => l.from !== selected && l.to !== selected,
        );
        selected = null;
      }
      render();
    }
    root.addEventListener("input", (e) => {
      try {
        if (e.target.matches("[data-node-query]"))
          return renderSearch(e.target.value);
        if (!drawer.contains(e.target) || !modal) return;
        modal.dirty = true;
        if (e.target.dataset.waitTarget) {
          const id = e.target.dataset.waitTarget;
          modal.value.targets = modal.value.targets.filter((t) => t !== id);
          if (e.target.checked) modal.value.targets.push(id);
          return;
        }
        if (e.target.dataset.rule) {
          const [i, key] = e.target.dataset.rule.split(":");
          modal.value.rules[Number(i)][key] = e.target.value;
          return;
        }
        if (e.target.dataset.schema) {
          const [side, i, prop] = e.target.dataset.schema.split(":"),
            f = modal.value[side][Number(i)];
          if (prop === "default") {
            // Preserve partially typed values until save validates the complete field.
            modal.defaultText.set(f, e.target.value);
            return;
          }
          f[prop] =
            prop === "enum"
              ? e.target.value
                  .split(",")
                  .map((v) => v.trim())
                  .filter(Boolean)
              : prop === "required"
                ? e.target.checked
                : prop === "default"
                  ? parseValue(e.target.value, f.type)
                  : e.target.value;
          return;
        }
        const key = e.target.dataset.field;
        if (!key) return;
        if (key.startsWith("entry-value.")) {
          const name = key.slice(12),
            f = state.published
              .find((f) => f.id === modal.value.workflowId)
              .parameters.find((p) => p.name === name);
          modal.value.values[name] =
            e.target.tagName === "SELECT" && e.target.value === ""
              ? undefined
              : parseValue(e.target.value, f.type);
        } else
          modal.value[key] =
            key === "locks"
              ? e.target.value
                  .split(",")
                  .map((s) => s.trim())
                  .filter(Boolean)
              : key === "confirm"
                ? e.target.checked
                : key === "order"
                  ? Number(e.target.value)
                  : e.target.value;
      } catch (err) {
        showError(err);
      }
    });
    root.addEventListener("change", (e) => {
      try {
        if (e.target.matches("[data-workflow-file]")) {
          const file = e.target.files[0],
            current = modal;
          if (!file) return;
          if (file.size > 4 * 1024 * 1024) throw Error("文件不能超过 4 MiB");
          file
            .text()
            .then((text) => {
              if (modal !== current) return;
              current.value.text = text;
              current.dirty = true;
              renderDrawer();
            })
            .catch(showError);
          return;
        }
        if (e.target.dataset.controlConfig) {
          checkpoint();
          const n = draft.graph.nodes.find((n) => n.id === selected);
          n.config ||= {};
          n.config[e.target.dataset.controlConfig] = e.target.value;
          return render();
        }
        if (e.target.dataset.nodeValue) {
          const n = draft.graph.nodes.find(
              (n) => n.id === e.target.dataset.owner,
            ),
            key = e.target.dataset.nodeValue,
            f = (n.kind === "start" ? draft.parameters : inputs(n)).find(
              (f) => f.name === key,
            ),
            value = parseValue(e.target.value, f.type);
          checkpoint();
          if (n.kind === "start") {
            f.default = value;
            n.outputs = clone(draft.parameters);
          } else n.values[key] = value;
          render();
          return;
        }
        if (e.target.matches("[data-workflow-name]")) {
          checkpoint();
          draft.name = e.target.value;
          return;
        }
        if (e.target.matches("[data-flow-select]")) {
          if (dirty && !confirm("放弃未保存工作流？")) return render();
          draft = clone(
            state.draft.find((f) => f.id === e.target.value) || null,
          );
          dirty = false;
          selected = draft?.graph.nodes[0]?.id;
          undo = [];
          redo = [];
          return render();
        }
        if (e.target.matches("[data-entry-flow]")) {
          modal.value.workflowId = e.target.value;
          modal.value.values = {};
          return renderDrawer();
        }
        if (!drawer.contains(e.target) && e.target.dataset.field && draft) {
          const n = draft.graph.nodes.find((n) => n.id === selected),
            [kind, ...parts] = e.target.dataset.field.split("."),
            key = parts.join(".");
          checkpoint();
          if (kind === "node") n[key] = e.target.value;
          else if (kind === "value") {
            n.values[key] = parseValue(
              e.target.value,
              inputs(n).find((f) => f.name === key).type,
            );
          } else
            n.config[key] = [
              "timeout",
              "retryAttempts",
              "retryDelayMs",
            ].includes(key)
              ? Number(e.target.value)
              : ["value", "environment"].includes(key)
                ? JSON.parse(e.target.value)
                : e.target.value;
          render();
        }
      } catch (err) {
        showError(err);
      }
    });
    root.addEventListener("pointerdown", (e) => {
      if (tab !== "editor") return;
      const stage = e.target.closest(".wf-stage");
      if (!stage || e.target.closest("input,textarea,select")) return;
      const p = e.target.closest("[data-port]"),
        handle = e.target.closest("[data-handle]");
      if ((p || handle) && manage && e.button === 0) {
        e.preventDefault();
        let port,
          replace = null;
        if (p) {
          port = JSON.parse(p.dataset.port);
          // Dragging an occupied input moves its existing connection without removing it until a valid drop.
          if (port.dir === "in" && port.mode === "data") {
            const existing = allLinks().find(
              (l) =>
                l.b.node === port.node &&
                l.b.field === port.field &&
                l.b.mode === port.mode,
            );
            if (existing) {
              port = existing.a;
              replace = existing.key;
            }
          }
        } else {
          const link = allLinks().find((l) => l.key === handle.dataset.link);
          port = handle.dataset.handle === "source" ? link.b : link.a;
          replace = link.key;
        }
        wire = {
          port,
          replace,
          start: { x: e.clientX, y: e.clientY },
          moved: false,
        };
        return;
      }
      if (e.target.closest("[data-link]")) {
        selectedLink = e.target.closest("[data-link]").dataset.link;
        selected = null;
        render();
        q(".wf-stage").focus({ preventScroll: true });
        return;
      }
      const n = e.target.closest("[data-node]");
      if (n && e.button === 0) {
        selected = n.dataset.node;
        selectedLink = null;
        if (manage)
          drag = {
            node: selected,
            x: e.clientX,
            y: e.clientY,
            before: clone(draft),
            nx: draft.graph.nodes.find((n) => n.id === selected).x,
            ny: draft.graph.nodes.find((n) => n.id === selected).y,
          };
        e.preventDefault();
        render();
        q(".wf-stage").focus({ preventScroll: true });
        return;
      }
      if (e.button === 0 || e.button === 1) {
        e.preventDefault();
        drag = { x: e.clientX, y: e.clientY, cx: camera.x, cy: camera.y };
        stage.setPointerCapture(e.pointerId);
      }
    });
    function move(e) {
      if (!root.isConnected) return;
      if (wire) {
        wire.moved = true;
        const stage = q(".wf-stage").getBoundingClientRect(),
          p = position(wire.port),
          a = { x: p.x * camera.z + camera.x, y: p.y * camera.z + camera.y },
          b = { x: e.clientX - stage.left, y: e.clientY - stage.top };
        q(".wf-wire").innerHTML = '<path d="' + curve(a, b) + '"/>';
        return;
      }
      if (!drag) return;
      if (drag.node) {
        const n = draft.graph.nodes.find((n) => n.id === drag.node);
        n.x = drag.nx + (e.clientX - drag.x) / camera.z;
        n.y = drag.ny + (e.clientY - drag.y) / camera.z;
        const el = q('[data-node="' + n.id + '"]');
        el.style.left = n.x + "px";
        el.style.top = n.y + "px";
        q(".wf-edges").innerHTML = linksHTML();
      } else {
        camera.x = drag.cx + e.clientX - drag.x;
        camera.y = drag.cy + e.clientY - drag.y;
        transform();
      }
    }
    function up(e) {
      if (!root.isConnected) return;
      if (wire) {
        const w = wire;
        wire = null;
        q(".wf-wire").innerHTML = "";
        const hit = document.elementFromPoint(e.clientX, e.clientY),
          port = hit?.closest("[data-port]");
        if (port) {
          const before = clone(draft);
          try {
            connect(w.port, JSON.parse(port.dataset.port), w.replace);
            undo.push(before);
            redo = [];
            dirty = true;
            selectedLink = null;
            render();
          } catch (err) {
            showError(err);
          }
        } else if (
          w.moved &&
          hit?.closest(".wf-stage") &&
          !hit.closest("[data-node]")
        )
          openSearch(e.clientX, e.clientY, w.port, w.replace);
      }
      if (drag?.node) {
        const node = draft.graph.nodes.find((n) => n.id === drag.node);
        // Selection alone does not add an undo step; only a changed position does.
        if (node.x !== drag.nx || node.y !== drag.ny) {
          undo.push(drag.before);
          redo = [];
          dirty = true;
        }
      }
      drag = null;
    }
    document.addEventListener("pointermove", move);
    document.addEventListener("pointerup", up);
    root.addEventListener(
      "wheel",
      (e) => {
        if (!draft || !e.target.closest(".wf-stage")) return;
        e.preventDefault();
        const r = q(".wf-stage").getBoundingClientRect(),
          x = e.clientX - r.left,
          y = e.clientY - r.top,
          z = Math.max(
            0.2,
            Math.min(1.6, camera.z * Math.exp(-e.deltaY * 0.001)),
          );
        camera.x = x - ((x - camera.x) * z) / camera.z;
        camera.y = y - ((y - camera.y) * z) / camera.z;
        camera.z = z;
        transform();
      },
      { passive: false },
    );
    root.addEventListener("dblclick", (e) => {
      if (e.target.closest(".wf-stage") && !e.target.closest("[data-node]"))
        openSearch(e.clientX, e.clientY);
    });
    root.addEventListener("contextmenu", (e) => {
      if (!e.target.closest(".wf-stage")) return;
      e.preventDefault();
      const p = e.target.closest("[data-port]");
      if (p && manage) {
        const port = JSON.parse(p.dataset.port);
        checkpoint();
        if (port.mode === "control") {
          draft.graph.dependencies = draft.graph.dependencies.filter((l) =>
            port.dir === "in"
              ? l.to !== port.node
              : l.from !== port.node ||
                (l.outlet || "success") !== (port.field || "success"),
          );
          render();
          return;
        }
        draft.graph.links = draft.graph.links.filter((l) =>
          port.dir === "in"
            ? l.to.node !== port.node || l.to.field !== port.field
            : l.from.node !== port.node || l.from.field !== port.field,
        );
        render();
      } else if (!e.target.closest("[data-node]"))
        openSearch(e.clientX, e.clientY);
    });
    root.addEventListener("keydown", (e) => {
      if (
        e.target.matches('.wf-inspector-tabs [role="tab"]') &&
        ["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)
      ) {
        e.preventDefault();
        const t =
          e.key === "Home"
            ? "params"
            : e.key === "End"
              ? "docs"
              : inspectorTab === "params"
                ? "docs"
                : "params";
        q('[data-action="inspect-' + t + '"]').click();
        q('[data-action="inspect-' + t + '"]').focus();
        return;
      }
      if (
        e.target.matches("[data-wf-tab]") &&
        ["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)
      ) {
        e.preventDefault();
        const tabs = [...root.querySelectorAll("[data-wf-tab]")],
          i = tabs.indexOf(e.target),
          next =
            e.key === "Home"
              ? 0
              : e.key === "End"
                ? tabs.length - 1
                : (i + (e.key === "ArrowRight" ? 1 : -1) + tabs.length) %
                  tabs.length;
        tabs[next].click();
        tabs[next].focus();
        return;
      }
      if (e.key === "Escape") {
        wire = null;
        const el = q(".wf-wire");
        if (el) el.innerHTML = "";
        if (drag?.node) {
          draft = drag.before;
          render();
        }
        drag = null;
        return;
      }
      if (e.target.matches("[data-node-query]") && e.key === "Enter") {
        e.preventDefault();
        try {
          chooseNode(0);
        } catch (err) {
          showError(err);
        }
        return;
      }
      if (e.target.matches("input,textarea,select") || drawer.open) return;
      if (
        tab === "editor" &&
        (e.ctrlKey || e.metaKey) &&
        ["z", "y"].includes(e.key.toLowerCase())
      ) {
        e.preventDefault();
        q(
          '[data-action="' +
            (e.key.toLowerCase() === "y" || e.shiftKey ? "redo" : "undo") +
            '"]',
        ).click();
        return;
      }
      if (["Delete", "Backspace"].includes(e.key) && tab === "editor") {
        e.preventDefault();
        deleteSelection();
      }
    });
    search.addEventListener("cancel", (e) => {
      e.preventDefault();
      closeSearch();
    });
    search.addEventListener("close", () => {
      if (!search.open) closeSearch();
    });
    search.addEventListener("keydown", (e) => {
      // Picker keyboard actions must not reach canvas delete/undo shortcuts.
      e.stopPropagation();
      if (e.key === "Escape") {
        e.preventDefault();
        closeSearch();
      } else if (e.key === "Enter" && e.target.matches("[data-node-query]")) {
        e.preventDefault();
        try {
          chooseNode(0);
        } catch (err) {
          showError(err);
        }
      }
    });
    let searchBackdropDown = false;
    const outsideSearch = (e) => {
      const r = search.getBoundingClientRect();
      return (
        e.clientX < r.left ||
        e.clientX > r.right ||
        e.clientY < r.top ||
        e.clientY > r.bottom
      );
    };
    search.addEventListener("pointerdown", (e) => {
      searchBackdropDown = e.target === search && outsideSearch(e);
    });
    search.addEventListener("click", (e) => {
      if (searchBackdropDown && e.target === search && outsideSearch(e))
        closeSearch();
      searchBackdropDown = false;
    });
    drawer.addEventListener("cancel", (e) => {
      e.preventDefault();
      requestDrawerClose();
    });
    let backdropDown = false;
    const outsideDrawer = (e) => {
      const r = drawer.getBoundingClientRect();
      return (
        e.clientX < r.left ||
        e.clientX > r.right ||
        e.clientY < r.top ||
        e.clientY > r.bottom
      );
    };
    drawer.addEventListener("pointerdown", (e) => {
      backdropDown = e.target === drawer && outsideDrawer(e);
    });
    drawer.addEventListener("click", (e) => {
      if (backdropDown && e.target === drawer && outsideDrawer(e))
        requestDrawerClose();
      backdropDown = false;
    });
    const timer = setInterval(() => {
      if (!root.isConnected) {
        clearInterval(timer);
        document.removeEventListener("pointermove", move);
        document.removeEventListener("pointerup", up);
        return;
      }
      if (modal?.kind === "logs" && !modal.pending) {
        const current = modal;
        current.pending = true;
        api("logs/" + encodeURIComponent(current.value.id))
          .then((events) => {
            // A response belongs to the exact opened log session, even when another log has since opened.
            if (modal !== current || !root.isConnected) return;
            const pane = q(".wf-log-lines"),
              follow =
                pane &&
                pane.scrollHeight - pane.scrollTop - pane.clientHeight < 32,
              top = pane?.scrollTop || 0;
            current.value.events = events || [];
            current.value.error = "";
            current.value.loading = false;
            renderDrawer();
            const next = q(".wf-log-lines");
            if (next) next.scrollTop = follow ? next.scrollHeight : top;
          })
          .catch((error) => {
            if (modal === current) {
              current.value.error = "更新失败：" + error.message;
              renderDrawer();
            }
          })
          .finally(() => {
            current.pending = false;
          });
      }
      if (
        (tab === "history" || tab === "entries") &&
        !drawer.open &&
        !root.querySelector(".action-menu[open]")
      )
        refresh().catch((err) => toast(err.message));
    }, 2500);
    refresh().catch((err) => toast(err.message));
  }
  window.ScriptBoardWorkflow = initialize;
  initialize(document.querySelector("[data-workflow]"));
})();
