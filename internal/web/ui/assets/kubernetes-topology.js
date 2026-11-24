(() => {
  "use strict";

  const SVG = "http://www.w3.org/2000/svg";
  const NS_ALL = "all";
  const UNKNOWN_HOST = "unmapped";
  const GRID_WIDTH = 920;
  const MAX_LANES = 24;
  const MAX_COLUMN_ITEMS = 42;
  const MESSAGE_KEYS = ["deployments", "statefulsets", "daemonsets", "cronjobs", "pods", "nodes", "services", "ingresses", "endpointslices", "replicasets", "jobs"];
  const shapes = { ingress: "globe", service: "network", workload: "package", pod: "box", node: "server", group: "box" };
  const stateOrder = { degraded: 0, progressing: 1, ready: 2 };
  const stateIcons = { ready: "circle-check", progressing: "timer", degraded: "triangle-alert" };
  const labels = {
    "zh-CN": {
      workloads: "工作负载", hostStage: "物理机 / Node", unmapped: "物理机未标记", ready: "就绪", progressing: "变更中", degraded: "需查看", notReady: "未就绪",
      empty: "无匹配资源", loading: "加载拓扑", failed: "无法加载拓扑", partial: "部分拓扑数据不可用", limited: "仅显示部分资源，请缩小筛选范围",
      summary: "概况", events: "事件", noEvents: "无事件", logs: "Pod 日志", node: "节点", physical: "物理机", pending: "待调度",
      controller: "控制器", status: "状态", namespace: "命名空间", restarts: "重启", containers: "容器", memory: "内存", role: "角色",
      related: "关联资源", ports: "端口", endpoints: "Pod 端点", outside: "其他端点", rules: "入口规则", endpointsUnavailable: "端点不可用",
    },
    en: {
      workloads: "Workloads", hostStage: "Physical hosts / Nodes", unmapped: "Unmapped physical host", ready: "Ready", progressing: "Progressing", degraded: "Needs attention", notReady: "Not ready",
      empty: "No matching resources", loading: "Loading topology", failed: "Unable to load topology", partial: "Some topology data is unavailable", limited: "Showing a subset of resources. Narrow your filters.",
      summary: "Overview", events: "Events", noEvents: "No events", logs: "Pod logs", node: "Node", physical: "Physical host", pending: "Unscheduled",
      controller: "Controller", status: "Status", namespace: "Namespace", restarts: "Restarts", containers: "Containers", memory: "Memory", role: "Role",
      related: "Related resources", ports: "Ports", endpoints: "Pod endpoints", outside: "Other endpoints", rules: "Ingress rules", endpointsUnavailable: "Endpoints unavailable",
    },
  };

  function element(tag, className, content) {
    const result = document.createElement(tag);
    if (className) result.className = className;
    if (content !== undefined) result.textContent = content;
    return result;
  }

  function icon(name) {
    const result = element("span");
    result.dataset.lucide = name;
    result.setAttribute("aria-hidden", "true");
    return result;
  }

  function resourcesFrom(data) {
    const resources = new Map();
    const add = (type, values, identity, status, attributes) => {
      for (const value of values || []) {
        const id = type + ":" + identity(value);
        resources.set(id, {
          id, type, name: value.Name, namespace: value.Namespace || "", value,
          status: status(value), ...attributes(value),
        });
      }
    };
    const scopedName = value => value.Namespace + "/" + value.Name;
    add("workload", data.Workloads, value => value.Key, value => value.Status, value => ({ kind: value.Kind, pods: [] }));
    add("pod", data.Pods, scopedName, value => value.Status, value => ({
      kind: "Pod", workload: value.WorkloadKey ? "workload:" + value.WorkloadKey : "", node: value.Node ? "node:" + value.Node : "",
      services: [], reason: value.Reason,
    }));
    add("node", data.Nodes, value => value.Name, value => value.Ready ? "ready" : "degraded", value => ({
      kind: "Node", host: value.PhysicalHost ? "host:" + value.PhysicalHost : UNKNOWN_HOST, pods: [],
    }));
    const podsByUID = new Map();
    for (const resource of resources.values()) {
      if (resource.type !== "pod") continue;
      if (resource.value.UID) podsByUID.set(resource.value.UID, resource);
      resources.get(resource.workload)?.pods.push(resource.id);
      resources.get(resource.node)?.pods.push(resource.id);
    }
    add("service", data.Services, scopedName, () => "", value => ({
      kind: "Service", ingresses: [],
      pods: (value.Endpoints || []).map(endpoint => podsByUID.get(endpoint.PodUID)?.id).filter(Boolean),
      endpointReady: new Map((value.Endpoints || []).flatMap(endpoint => {
        const pod = podsByUID.get(endpoint.PodUID);
        return pod ? [[pod.id, endpoint.Ready !== false]] : [];
      })),
    }));
    add("ingress", data.Ingresses, scopedName, () => "", value => ({
      kind: "Ingress", services: [...new Set((value.Rules || []).map(rule => "service:" + value.Namespace + "/" + rule.Service))],
    }));
    for (const resource of resources.values()) {
      if (resource.type === "service") {
        resource.pods.forEach(pod => resources.get(pod)?.services.push(resource.id));
      }
      if (resource.type === "ingress") {
        resource.services.forEach(service => resources.get(service)?.ingresses.push(resource.id));
      }
    }
    return resources;
  }

  function relevantResources(resources, params) {
    const search = (params.get("query") || "").trim().toLowerCase();
    const status = params.get("status") || NS_ALL;
    const kind = params.get("kind") || NS_ALL;
    if (!search && status === NS_ALL && kind === NS_ALL) return [...resources.values()];

    const keep = new Set();
    const include = resource => {
      if (!resource) return;
      keep.add(resource.id);
      if (resource.type !== "pod") return;
      for (const id of [resource.workload, resource.node, ...resource.services]) keep.add(id);
      resource.services.forEach(id => resources.get(id)?.ingresses.forEach(ingress => keep.add(ingress)));
    };
    const statusMatch = item => status === NS_ALL || (status === "attention" ? item.status && item.status !== "ready" : item.status === status);
    const kindMatch = item => {
      if (kind === NS_ALL) return true;
      return item.kind === kind || resources.get(item.workload)?.kind === kind;
    };
    for (const resource of resources.values()) {
      const value = resource.value;
      const texts = [resource.name, resource.namespace, resource.kind, resource.reason, value.Image, value.Node, value.Nodes,
        value.Type, value.PhysicalHost, value.Role, value.ExternalName,
        ...(value.Rules || []).flatMap(rule => [rule.Host, rule.Path, rule.Service])];
      if (!statusMatch(resource) || !kindMatch(resource) || !texts.some(text => String(text || "").toLowerCase().includes(search))) continue;
      include(resource);
      let pods = (resource.pods || []).map(id => resources.get(id));
      if (resource.type === "ingress") {
        pods = resource.services.flatMap(id => {
          const service = resources.get(id);
          if (!service) return [];
          keep.add(service.id);
          return service.pods.map(pod => resources.get(pod));
        });
      }
      const matchingPods = pods.filter(statusMatch);
      (matchingPods.length ? matchingPods : pods).forEach(include);
      if (resource.type === "service") resource.ingresses.forEach(id => keep.add(id));
    }
    return [...resources.values()].filter(resource => keep.has(resource.id));
  }

  function description(resource, words) {
    const value = resource.value;
    switch (resource.type) {
      case "workload": return value.Kind + " · " + value.Ready + "/" + value.Desired;
      case "pod": return value.Reason || (value.Node ? [value.Phase || "Pod", value.Ready].join(" · ") : words.pending);
      case "service": return [value.Type, value.Ports?.map(port => port.Port).slice(0, 2).join(", ")].filter(Boolean).join(" · ");
      case "ingress": return value.Rules?.[0]?.Host || value.Class || "Ingress";
      case "node": return [value.Role, value.Version].filter(Boolean).join(" · ") || "Node";
      default: return resource.meta;
    }
  }

  function graphLayout(items, placement, selected, expansions, byNamespace, words) {
    const byID = new Map(items.map(item => [item.id, item]));
    const columns = placement
      ? [[words.workloads, "package", 34, 196], ["Pod", "box", 328, 226], [words.hostStage, "server", 710, 168]]
      : [["Ingress", "globe", 30, 154], ["Service", "network", 225, 154], [words.workloads, "package", 424, 170], ["Pod", "box", 642, 242]];
    const positions = new Map();
    const edges = new Map();
    const podsShown = new Map();
    const hostGroups = [];
    const lanes = [];
    let truncated = false;
    let y = 73;
    const add = (item, column, top, height = 56) => {
      const [, , x, width] = columns[column];
      const position = { item, x, y: top, width, height };
      positions.set(item.id, position);
      return position;
    };
    const connect = (from, to, kind, lane = null) => {
      if (!positions.has(from) || !positions.has(to)) return;
      const key = [from, to, kind].join("|");
      if (!edges.has(key)) edges.set(key, { from, to, kind, lane });
    };
    const stack = (item, column, top) => {
      let offset = top;
      const [, , x] = columns[column];
      while ([...positions.values()].some(position => position.x === x && Math.abs(position.y - offset) < 66)) offset += 66;
      return add(item, column, offset);
    };
    const rank = (left, right) => left.namespace.localeCompare(right.namespace) ||
      (stateOrder[left.status] ?? 3) - (stateOrder[right.status] ?? 3) || left.name.localeCompare(right.name);
    const prioritize = (left, right) => Number(right.id === selected) - Number(left.id === selected);
    const keep = (list, limit) => {
      if (list.length > limit) truncated = true;
      return list.slice(0, limit);
    };

    const work = keep(items.filter(item => item.type === "workload").sort((a, b) =>
      prioritize(a, b) || Number(b.pods.includes(selected)) - Number(a.pods.includes(selected)) || rank(a, b)), MAX_LANES).sort(rank);
    let namespace = null;
    const nextLane = item => {
      if (byNamespace && namespace !== item.namespace) {
        namespace = item.namespace;
        lanes.push({ name: namespace, y });
        y += 29;
      }
    };
    for (const item of work) {
      nextLane(item);
      const pods = item.pods.map(id => byID.get(id)).filter(Boolean).sort((a, b) => prioritize(a, b) || rank(a, b));
      const inFocus = item.id === selected || pods.some(pod => pod.id === selected);
      const initial = inFocus ? 3 : pods[0]?.status === "degraded" ? 2 : 0;
      const expanded = Math.max(expansions.get(item.id) || 0, initial);
      const visiblePods = pods.length === 1 ? pods : pods.slice(0, expanded);
      const remaining = pods.slice(visiblePods.length);
      const rows = visiblePods.length + Number(Boolean(remaining.length));
      add(item, placement ? 0 : 2, y + (rows > 1 ? 28 : 0));
      for (const [index, pod] of visiblePods.entries()) {
        add(pod, placement ? 1 : 3, y + index * 64);
        podsShown.set(pod.id, pod.id);
      }
      if (remaining.length) {
        const group = {
          type: "group", id: "group:" + item.id, workload: item.id, expanded, kind: "Pod",
          name: remaining.length + " Pod", status: remaining.find(pod => pod.status === "degraded") ? "degraded" : remaining.find(pod => pod.status === "progressing") ? "progressing" : "ready",
          meta: remaining.filter(pod => pod.status === "ready").length + "/" + remaining.length + " " + words.ready,
        };
        add(group, placement ? 1 : 3, y + visiblePods.length * 64);
        remaining.forEach(pod => podsShown.set(pod.id, group.id));
      }
      pods.forEach(pod => connect(item.id, podsShown.get(pod.id), "owner"));
      y += Math.max(112, rows * 64 + 22);
    }
    const orphanPods = keep(items.filter(item => item.type === "pod" && !byID.has(item.workload)).sort(rank), MAX_LANES);
    for (const item of orphanPods) {
      nextLane(item);
      add(item, placement ? 1 : 3, y);
      podsShown.set(item.id, item.id);
      y += 67;
    }
    if (placement) {
      const groups = new Map();
      const nodes = items.filter(item => item.type === "node").sort((a, b) => prioritize(a, b) || Number(b.pods.some(id => podsShown.has(id))) - Number(a.pods.some(id => podsShown.has(id))) ||
        (a.host === UNKNOWN_HOST ? 1 : 0) - (b.host === UNKNOWN_HOST ? 1 : 0) || a.host.localeCompare(b.host) || a.name.localeCompare(b.name));
      for (const item of keep(nodes, MAX_COLUMN_ITEMS)) {
        if (!groups.has(item.host)) groups.set(item.host, []);
        groups.get(item.host).push(item);
      }
      let hostY = 63;
      for (const [host, members] of groups) {
        const height = 38 + members.length * 65;
        hostGroups.push({ host, y: hostY, height, count: members.length });
        for (const [index, node] of members.entries()) {
          add(node, 2, hostY + 31 + index * 65);
          node.pods.forEach(pod => connect(podsShown.get(pod), node.id, "schedule"));
        }
        hostY += height + 13;
      }
    } else {
      let serviceY = 73;
      const services = items.filter(item => item.type === "service").sort((a, b) => prioritize(a, b) ||
        Number(b.pods.some(id => podsShown.has(id))) - Number(a.pods.some(id => podsShown.has(id))) || rank(a, b));
      for (const item of keep(services, MAX_COLUMN_ITEMS)) {
        const targets = item.pods.filter(id => podsShown.has(id));
        const pod = byID.get(targets[0]);
        const anchor = positions.get(pod?.workload) || positions.get(podsShown.get(pod?.id));
        const position = stack(item, 1, anchor?.y ?? serviceY);
        if (!anchor) serviceY = position.y + 66;
        for (const id of targets) {
          const controller = positions.get(byID.get(id)?.workload);
          const target = positions.get(podsShown.get(id));
          connect(item.id, podsShown.get(id), "route", controller ? controller.y + controller.height + 12 : Math.max(position.y, target.y) + 69);
        }
      }
      let ingressY = 73;
      for (const item of keep(items.filter(item => item.type === "ingress").sort((a, b) => prioritize(a, b) || rank(a, b)), MAX_COLUMN_ITEMS)) {
        const services = item.services.filter(service => positions.has(service));
        const anchor = positions.get(services[0]);
        const position = stack(item, 0, anchor?.y ?? ingressY);
        if (!anchor) ingressY = position.y + 66;
        services.forEach(service => connect(item.id, service, "route"));
      }
    }
    const height = Math.max(440, ...[...positions.values()].map(item => item.y + item.height + 28), ...hostGroups.map(group => group.y + group.height + 18));
    return { positions, columns, edges: [...edges.values()], hostGroups, lanes, height, truncated };
  }

  function edgePath(from, to, lane) {
    const a = { x: from.x + from.width, y: from.y + from.height / 2 };
    const b = { x: to.x, y: to.y + to.height / 2 };
    if (lane === null) {
      const middle = (a.x + b.x) / 2;
      return ["M", a.x, a.y, "C", middle, a.y, middle, b.y, b.x, b.y].join(" ");
    }
    const left = a.x + 20, right = b.x - 17, direction = b.y < lane ? -1 : 1;
    return ["M", a.x, a.y, "H", left - 6, "Q", left, a.y, left, a.y + 6, "V", lane - 6, "Q", left, lane, left + 6, lane,
      "H", right - 6, "Q", right, lane, right, lane + direction * 6, "V", b.y - direction * 6, "Q", right, b.y, right + 6, b.y, "H", b.x].join(" ");
  }

  function highlightedEdges(graph, selected) {
    const focused = graph.positions.get(selected);
    if (!focused) return null;
    const direct = graph.edges.filter(edge => edge.from === selected || edge.to === selected);
    const pods = new Set(direct.flatMap(edge => [edge.from, edge.to]).filter(id => ["pod", "group"].includes(graph.positions.get(id)?.item.type)));
    const services = new Set(graph.edges.filter(edge => edge.kind === "route" && pods.has(edge.to)).map(edge => edge.from));
    return new Set(graph.edges.filter(edge => direct.includes(edge) || pods.has(edge.from) || pods.has(edge.to) || edge.kind === "route" && services.has(edge.to)));
  }
  function setup(root, { renderIcons, openWorkload, showResource, closeDetails, cleanups, locale }) {
    const pane = root.querySelector("[data-kubernetes-topology-pane]");
    if (!pane) return null;
    const canvas = pane.querySelector("[data-kubernetes-canvas]");
    const scene = pane.querySelector("[data-kubernetes-scene]");
    const message = pane.querySelector("[data-kubernetes-topology-message]");
    const zoomValue = pane.querySelector("[data-kubernetes-zoom-value]");
    const words = labels[locale] || labels.en;
    const expanded = new Map();
    const camera = { x: 0, y: 0, zoom: 1, fit: "initial", height: 440 };
    let resources = new Map(), errors = {}, selected = "", focusTrigger = null;
    let loadedScope = "", snapshotRequest = null, eventRequest = null;
    let connection = root.dataset.kubernetesConnection, cameraScope = "";
    let drag = null;
    const mode = () => root.dataset.kubernetesTopologyMode;
    const hostLabel = key => key === UNKNOWN_HOST ? words.unmapped : key.slice(5);
    const params = () => new URLSearchParams(location.search);

    function drawCamera() {
      scene.style.transform = "translate(" + camera.x + "px," + camera.y + "px) scale(" + camera.zoom + ")";
      zoomValue.value = Math.round(camera.zoom * 100) + "%";
    }

    function fitScene(initial = false) {
      if (!canvas.clientWidth) return;
      const scale = Math.min(1.15, (canvas.clientWidth - 12) / GRID_WIDTH, (canvas.clientHeight - 12) / camera.height);
      camera.zoom = initial ? Math.max(canvas.clientWidth < 540 ? .6 : .72, scale) : scale;
      camera.x = initial && canvas.clientWidth < GRID_WIDTH * camera.zoom
        ? Math.min(0, canvas.clientWidth - GRID_WIDTH * camera.zoom)
        : (canvas.clientWidth - GRID_WIDTH * camera.zoom) / 2;
      camera.y = Math.max(5, (canvas.clientHeight - camera.height * camera.zoom) * .24);
      camera.fit = initial ? "initial" : "all";
      drawCamera();
    }

    function zoom(factor, x = canvas.clientWidth / 2, y = canvas.clientHeight / 2) {
      const next = Math.min(2.35, Math.max(.18, camera.zoom * factor));
      const ratio = next / camera.zoom;
      camera.x = x - (x - camera.x) * ratio;
      camera.y = y - (y - camera.y) * ratio;
      camera.zoom = next;
      camera.fit = false;
      drawCamera();
    }

    function draw(reset = false) {
      if (pane.hidden) return;
      const query = params();
      const placement = mode() === "placement";
      const graph = graphLayout(relevantResources(resources, query), placement, selected, expanded,
        (!query.has("namespace") || query.get("namespace") === NS_ALL) && new Set([...resources.values()].map(item => item.namespace).filter(Boolean)).size > 1, words);
      const active = document.activeElement;
      const focused = scene.contains(active) && [active.dataset.kubernetesResource, active.dataset.kubernetesGroup, active.dataset.kubernetesFocusHost].find(Boolean);
      scene.replaceChildren();
      const notices = [];
      if (!graph.positions.size) notices.push(words.empty);
      if (MESSAGE_KEYS.some(key => errors[key])) notices.push(words.partial);
      if (graph.truncated) notices.push(words.limited);
      message.textContent = notices.join(" · ");
      camera.height = graph.height;
      scene.style.width = GRID_WIDTH + "px";
      scene.style.height = graph.height + "px";
      pane.querySelector("[data-kubernetes-legend-route]").hidden = placement;
      pane.querySelector("[data-kubernetes-legend-placement]").hidden = !placement;
      for (const [index, [name, glyph, x]] of graph.columns.entries()) {
        const title = element("div", "kubernetes-graph-column");
        title.append(icon(glyph), name);
        Object.assign(title.style, { left: x + "px", top: "22px" });
        scene.append(title);
        if (index) {
          const divider = element("div", "kubernetes-graph-divider");
          Object.assign(divider.style, { left: (x - 24) + "px", height: (graph.height - 64) + "px" });
          scene.append(divider);
        }
      }
      for (const lane of graph.lanes) {
        const label = element("div", "kubernetes-graph-namespace", lane.name);
        label.style.top = (lane.y - 7) + "px";
        scene.append(label);
      }
      for (const group of graph.hostGroups) {
        const frame = element("div", "kubernetes-graph-host");
        frame.toggleAttribute("data-unmapped", group.host === UNKNOWN_HOST);
        Object.assign(frame.style, { top: group.y + "px", height: group.height + "px" });
        const button = element("button");
        button.type = "button";
        button.dataset.kubernetesFocusHost = group.host;
        button.setAttribute("aria-pressed", String(query.get("host") === group.host));
        button.title = hostLabel(group.host);
        button.append(icon("server"), element("span", "", hostLabel(group.host)), element("small", "", group.count + " Node"));
        frame.append(button);
        scene.append(frame);
      }
      const paths = document.createElementNS(SVG, "svg");
      paths.setAttribute("width", String(GRID_WIDTH));
      paths.setAttribute("height", String(graph.height));
      paths.setAttribute("aria-hidden", "true");
      paths.classList.add("kubernetes-graph-edges");
      const activeEdges = highlightedEdges(graph, selected);
      for (const edge of graph.edges) {
        const path = document.createElementNS(SVG, "path");
        path.classList.add(edge.kind);
        if (activeEdges) path.classList.add(activeEdges.has(edge) ? "active" : "dimmed");
        path.setAttribute("d", edgePath(graph.positions.get(edge.from), graph.positions.get(edge.to), edge.lane));
        paths.append(path);
      }
      scene.append(paths);

      for (const { item, x, y, width, height } of [...graph.positions.values()].sort((a, b) => a.x - b.x || a.y - b.y)) {
        const control = element("button", "kubernetes-graph-node");
        control.type = "button";
        if (item.type === "group") control.dataset.kubernetesGroup = item.workload;
        else control.dataset.kubernetesResource = item.id;
        if (item.status) control.dataset.state = item.status;
        control.setAttribute("aria-pressed", String(selected === item.id));
        Object.assign(control.style, { left: x + "px", top: y + "px", width: width + "px", height: height + "px" });
        const meta = description(item, words);
        const nodeLabel = [item.type === "group" ? item.name : item.kind + " " + item.name, item.namespace, meta, words[item.status]].filter(Boolean).join(" · ");
        control.title = nodeLabel;
        control.setAttribute("aria-label", nodeLabel);
        const symbol = element("span", "kubernetes-graph-icon");
        symbol.append(icon(shapes[item.type]));
        const text = element("span", "kubernetes-graph-label");
        text.append(element("strong", "", item.name), element("small", "", meta));
        control.append(symbol, text);
        if (item.status) control.append(icon(stateIcons[item.status]));
        scene.append(control);
      }
      renderIcons(scene);
      if (reset || camera.fit) fitScene(reset || camera.fit === "initial");
      if (focused) {
        [...scene.querySelectorAll("button")].find(button => [button.dataset.kubernetesResource, button.dataset.kubernetesGroup, button.dataset.kubernetesFocusHost].includes(focused))?.focus({ preventScroll: true });
      }
    }

    function snapshotURL() {
      const current = params();
      const url = new URL("/monitor/kubernetes/data", location.origin);
      url.searchParams.set("cluster", root.dataset.kubernetesConnection);
      for (const filter of ["namespace", "host"]) {
        const value = current.get(filter);
        if (value && value !== NS_ALL) url.searchParams.set(filter, value);
      }
      url.searchParams.set("topology", "1");
      return url;
    }

    function loadSnapshot(force = false) {
      const url = snapshotURL();
      const scope = url.toString();
      if (!force && loadedScope === scope) return Promise.resolve(true);
      if (snapshotRequest?.scope === scope) return snapshotRequest.promise;
      snapshotRequest?.controller.abort();
      const controller = new AbortController();
      const request = { scope, controller, promise: null };
      snapshotRequest = request;
      const quiet = loadedScope === scope;
      if (!quiet) message.textContent = words.loading;
      request.promise = fetch(url, { headers: { Accept: "application/json" }, cache: "no-store", signal: controller.signal })
        .then(async response => {
          if (!response.ok) throw new Error("HTTP " + response.status);
          const data = await response.json();
          if (snapshotRequest !== request || controller.signal.aborted) return false;
          if (!data.Topology) throw new Error(words.failed);
          resources = resourcesFrom(data.Topology);
          errors = data.Errors || {};
          loadedScope = scope;
          if (selected && !resources.has(selected)) {
            selected = "";
            closeDetails();
          }
          return true;
        })
        .catch(error => {
          if (error?.name !== "AbortError") message.textContent = words.failed;
          return false;
        })
        .finally(() => { if (snapshotRequest === request) snapshotRequest = null; });
      return request.promise;
    }

    function update(force = false) {
      const nextConnection = root.dataset.kubernetesConnection;
      if (nextConnection !== connection) {
        snapshotRequest?.controller.abort();
        loadedScope = "";
        connection = nextConnection;
        selected = "";
        resources.clear();
        expanded.clear();
        closeDetails();
      }
      const view = root.dataset.kubernetesView;
      const section = root.dataset.kubernetesSectionName;
      root.querySelectorAll("[data-kubernetes-section]").forEach(item => {
        item.hidden = view !== "list" || item.dataset.kubernetesSection !== section;
      });
      pane.hidden = view !== "topology";
      if (pane.hidden) {
        if (force) loadedScope = "";
        if (selected && loadedScope !== snapshotURL().toString()) loadSnapshot();
        return;
      }
      const scope = snapshotURL().toString() + "|" + mode();
      const reset = scope !== cameraScope;
      if (reset) {
        expanded.clear();
        cameraScope = scope;
        camera.fit = "initial";
      }
      loadSnapshot(force).then(loaded => {
        if (loaded && !pane.hidden) draw(reset);
      });
    }

    function workloadURL(key, suffix, pod = "") {
      const url = new URL("/monitor/kubernetes/clusters/" + encodeURIComponent(connection) +
        "/workloads/" + key.split("/").map(encodeURIComponent).join("/") + "/" + suffix, location.origin);
      if (pod) url.searchParams.set("pod", pod);
      return url.pathname + url.search;
    }

    function detailSection(title, content) {
      const section = element("section", "kubernetes-detail-section");
      section.append(element("h3", "", title), content);
      return section;
    }

    function facts(rows) {
      const list = element("dl", "kubernetes-resource-facts");
      for (const [label, value] of rows) {
        const row = element("div");
        row.append(element("dt", "", label), element("dd", "", value || "—"));
        list.append(row);
      }
      return list;
    }

    function related(ids, endpointReady) {
      const list = element("div", "kubernetes-resource-links");
      for (const id of ids.filter(Boolean).slice(0, 40)) {
        const item = resources.get(id);
        if (!item) continue;
        const button = element("button");
        button.type = "button";
        button.dataset.kubernetesResource = id;
        const text = element("span");
        const endpointStatus = endpointReady?.get(id);
        const meta = endpointStatus === undefined ? description(item, words) : endpointStatus ? words.ready : words.notReady;
        text.append(element("strong", "", item.name), element("small", "", meta));
        button.append(icon(shapes[item.type]), text, icon("chevron-right"));
        list.append(button);
      }
      return list;
    }

    function podDetails(item) {
      const value = item.value;
      const host = resources.get(item.node);
      const runtime = facts([[words.status, value.Reason || value.Phase], [words.node, value.Node || words.pending],
        [words.physical, host ? hostLabel(host.host) : ""], [words.controller, [value.ControllerKind, value.ControllerName].filter(Boolean).join(" ")],
        [words.ready, value.Ready], [words.restarts, String(value.Restarts)], [words.containers, (value.Containers || []).join(", ")]]);
      const result = [detailSection(words.summary, runtime), detailSection(words.related, related([item.workload, item.node, ...item.services]))];
      if (value.WorkloadKey && root.dataset.kubernetesCanLogs === "true") {
        const link = element("a", "button button--compact", words.logs);
        link.href = workloadURL(value.WorkloadKey, "logs", value.Name);
        link.target = "_blank";
        link.rel = "noopener";
        result.push(link);
      }
      if (value.WorkloadKey) {
        const list = element("div", "kubernetes-detail-list", words.loading);
        result.push(detailSection(words.events, list));
        const controller = new AbortController();
        eventRequest = controller;
        fetch(workloadURL(value.WorkloadKey, "details", value.Name), { headers: { Accept: "application/json" }, signal: controller.signal })
          .then(async response => {
            if (!response.ok) throw new Error();
            const data = await response.json();
            if (controller.signal.aborted) return;
            list.replaceChildren();
            if (!data.Events?.length) list.append(element("span", "kubernetes-detail-empty", words.noEvents));
            for (const event of (data.Events || []).slice(0, 20)) {
              const row = element("div");
              row.append(element("strong", "", event.Reason), element("span", "", event.Message));
              list.append(row);
            }
          }).catch(error => {
            if (error?.name !== "AbortError") list.textContent = words.partial;
          });
      }
      return result;
    }

    function resourceDetails(item) {
      const value = item.value;
      if (item.type === "pod") return podDetails(item);
      if (item.type === "node") {
        return [detailSection(words.summary, facts([
          [words.physical, hostLabel(item.host)], [words.role, value.Role],
          ["CPU", value.CPUAvailable ? Math.round(value.CPUPercent) + "%" : ""],
          [words.memory, value.MemoryAvailable ? Math.round(value.MemoryPercent) + "%" : ""],
        ])), detailSection("Pod · " + item.pods.length, related(item.pods))];
      }
      if (item.type === "service") {
        const ports = (value.Ports || []).map(port => [port.Name || port.Protocol, port.Port + (port.TargetPort ? " → " + port.TargetPort : "")]).filter(row => row[0]);
        const result = [detailSection(words.summary, facts([["Service", value.Type], ...ports,
          [words.endpoints, errors.endpointslices ? words.endpointsUnavailable : String(item.pods.length)], [words.outside, String(value.OtherEndpoints || 0)]]))];
        if (item.pods.length || item.ingresses.length) result.push(detailSection(words.related, related([...item.pods, ...item.ingresses], item.endpointReady)));
        return result;
      }
      const rules = (value.Rules || []).map(rule => [(rule.Host || "*") + (rule.Path || "/"), [rule.Service, rule.ServicePort].filter(Boolean).join(":")]);
      return [detailSection(words.rules, facts(rules)), detailSection(words.related, related(item.services))];
    }

    function selectResource(id, trigger) {
      eventRequest?.abort();
      if (!trigger.closest("[data-kubernetes-drawer]")) focusTrigger = trigger;
      if (id.startsWith("workload:")) {
        selected = id;
        draw();
        const item = resources.get(id);
        openWorkload(workloadURL(id.slice("workload:".length), "details"), item?.name || trigger.textContent.trim());
        return;
      }
      loadSnapshot().then(loaded => {
        const item = loaded && resources.get(id);
        if (!item || !root.isConnected) return;
        selected = id;
        draw();
        showResource(item.name, [item.kind, item.namespace, words[item.status]].filter(Boolean).join(" · "), resourceDetails(item));
      });
    }

    function onClick(event) {
      const button = event.target.closest("button");
      if (!button) return;
      const { kubernetesResource: resource, kubernetesGroup: group, kubernetesFocusHost: host, kubernetesZoom: control } = button.dataset;
      if (resource) {
        selectResource(resource, button);
      } else if (group) {
        const current = expanded.get(group) || (selected === group ? 3 : 0);
        expanded.set(group, current + 8);
        draw();
        [...scene.querySelectorAll("[data-kubernetes-resource]")].find(item => item.dataset.kubernetesResource === group)?.focus({ preventScroll: true });
      } else if (host) {
        const select = root.querySelector(".kubernetes-toolbar select[name=host]");
        if (!select) return;
        select.value = select.value === host ? "" : host;
        select.form.requestSubmit();
      } else if (control) {
        if (control === "fit") fitScene();
        else zoom(control === "in" ? 1.2 : 1 / 1.2);
      }
    }

    function pointerDown(event) {
      if (event.button !== 0 || event.target.closest("button")) return;
      drag = { x: event.clientX - camera.x, y: event.clientY - camera.y, id: event.pointerId };
      canvas.setPointerCapture(event.pointerId);
      canvas.classList.add("is-panning");
    }
    function pointerMove(event) {
      if (!drag || drag.id !== event.pointerId) return;
      camera.x = event.clientX - drag.x;
      camera.y = event.clientY - drag.y;
      camera.fit = false;
      drawCamera();
    }
    function pointerEnd() {
      drag = null;
      canvas.classList.remove("is-panning");
    }
    function onWheel(event) {
      if (!event.ctrlKey && !event.metaKey) return;
      event.preventDefault();
      const rect = canvas.getBoundingClientRect();
      zoom(event.deltaY < 0 ? 1.13 : 1 / 1.13, event.clientX - rect.left, event.clientY - rect.top);
    }

    root.addEventListener("click", onClick);
    canvas.addEventListener("pointerdown", pointerDown);
    canvas.addEventListener("pointermove", pointerMove);
    canvas.addEventListener("lostpointercapture", pointerEnd);
    canvas.addEventListener("wheel", onWheel, { passive: false });
    const resize = new ResizeObserver(() => { if (camera.fit) fitScene(camera.fit === "initial"); });
    resize.observe(canvas);
    cleanups.push(() => {
      snapshotRequest?.controller.abort();
      eventRequest?.abort();
      resize.disconnect();
      root.removeEventListener("click", onClick);
      canvas.removeEventListener("pointerdown", pointerDown);
      canvas.removeEventListener("pointermove", pointerMove);
      canvas.removeEventListener("lostpointercapture", pointerEnd);
      canvas.removeEventListener("wheel", onWheel);
    });
    update();
    return {
      update,
      selectWorkload(key, trigger) {
        eventRequest?.abort();
        selected = "workload:" + key;
        focusTrigger = trigger;
        draw();
      },
      close() {
        eventRequest?.abort();
        const previouslySelected = selected;
        selected = "";
        draw();
        if (focusTrigger?.isConnected) focusTrigger.focus({ preventScroll: true });
        else if (previouslySelected) {
          [...scene.querySelectorAll("[data-kubernetes-resource]")].find(item => item.dataset.kubernetesResource === previouslySelected)?.focus({ preventScroll: true });
        }
        focusTrigger = null;
      },
    };
  }

  window.ScriptBoardKubernetesWorkbench = setup;
})();
