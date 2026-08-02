"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

const repositoryRoot = path.resolve(__dirname, "..", "..");
const appScript = path.join(repositoryRoot, "internal", "app", "web", "assets", "app.js");
const appStyles = path.join(repositoryRoot, "internal", "app", "web", "assets", "app.css");
const contract = process.argv[2] || "all";

async function createHarness(browser) {
  const page = await browser.newPage({ viewport: { width: 900, height: 700 } });
  await page.route("http://scriptboard.test/**", route => route.fulfill({
    status: 200,
    contentType: "text/html",
    body: "<!doctype html><title>Assistant UI contract</title>",
  }));
  await page.goto("http://scriptboard.test/harness");
  await page.setContent(`<!doctype html>
    <html lang="en-US"><head><meta charset="utf-8"></head><body>
      <main data-assistant-workspace data-csrf-token="token" data-runtime-available="true"
        data-tools-called-one="Called 1 tool" data-tools-called-many="Called %d tools"
        data-tools-summary="%d succeeded · %d failed"
        data-tools-summary-active="%d succeeded · %d failed · %d in progress"
        data-events-url="/assistant-events">
        <section class="assistant-chat">
          <div class="assistant-transcript">
            <div class="assistant-message-list" data-assistant-message-list></div>
            <section data-assistant-approval-panel hidden>
              <h3 data-approval-tool></h3><p data-approval-target></p><small data-approval-expiry></small>
              <form data-assistant-approval-form><button type="submit">Approve</button></form>
            </section>
          </div>
          <form data-assistant-composer><textarea data-assistant-input></textarea><button class="assistant-send" type="submit">Send</button></form>
          <p data-assistant-error>Ready</p>
        </section>
      </main>
    </body></html>`);
  await page.addStyleTag({ path: appStyles });
  await page.addStyleTag({ content: `
    .assistant-transcript { display: block; width: 720px; height: 220px; min-height: 220px; max-height: 220px; flex: none; padding: 0; overflow-y: auto; }
    .assistant-message-list { display: grid; margin: 0; }
    .assistant-message { width: 70%; min-height: 34px; }
  ` });
  await page.evaluate(() => {
    class FakeEventSource {
      static instance;

      constructor() {
        this.listeners = new Map();
        this.readyState = 1;
        FakeEventSource.instance = this;
      }

      addEventListener(type, listener) {
        const listeners = this.listeners.get(type) || [];
        listeners.push(listener);
        this.listeners.set(type, listeners);
      }

      close() {
        this.readyState = 2;
      }

      emit(type, payload) {
        for (const listener of this.listeners.get(type) || []) {
          listener({ type, data: JSON.stringify(payload) });
        }
      }
    }

    window.EventSource = FakeEventSource;
    window.__assistantEmit = (type, payload) => FakeEventSource.instance.emit(type, payload);
  });
  await page.addScriptTag({ path: appScript });
  await page.waitForFunction(() => typeof window.__assistantEmit === "function");
  return page;
}

function message(id, role, body, status = "complete") {
  return { id, role, body, status, createdAt: "2026-08-01T12:00:00Z" };
}

async function visibleMessageFlow(page, messageID) {
  return page.locator(`[data-message-id="${messageID}"]`).evaluate(article =>
    [...article.children].flatMap(element => {
      if (element.matches("[data-message-segment], [data-message-body]")) return [`text:${element.textContent}`];
      if (element.matches("[data-assistant-tool-cluster]")) return [`tool:${element.querySelector("[data-tool-cluster-name]")?.textContent || ""}`];
      return [];
    }),
  );
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await createHarness(browser);

    if (contract === "all" || contract === "scroll") {
      const filler = Array.from({ length: 18 }, (_, index) => message(`filler-${index}`, "user", `History ${index}`));
      filler.push(message("scroll-reply", "assistant", "Streaming", "streaming"));
      await page.evaluate(messages => window.__assistantEmit("snapshot", { messages, toolCalls: [] }), filler);
      const scrollState = await page.locator(".assistant-transcript").evaluate(transcript => {
        const maximum = transcript.scrollHeight - transcript.clientHeight;
        transcript.scrollTop = Math.max(1, Math.floor(maximum / 3));
        transcript.dispatchEvent(new Event("scroll"));
        return { before: transcript.scrollTop, maximum };
      });
      assert.ok(scrollState.maximum > scrollState.before, `fixture did not create a scrollable transcript: ${JSON.stringify(scrollState)}`);
      await page.evaluate(() => window.__assistantEmit("delta", { messageId: "scroll-reply", delta: " response", body: "Streaming response" }));
      const scrollAfter = await page.locator(".assistant-transcript").evaluate(transcript => transcript.scrollTop);
      assert.equal(scrollAfter, scrollState.before, "streaming output pulled a reader away from older messages");
    }

    if (contract === "all" || contract === "timeline") {
      await page.evaluate(() => window.__assistantEmit("snapshot", {
      messages: [{ id: "live-reply", role: "assistant", body: "", status: "streaming", createdAt: "2026-08-01T12:00:00Z" }],
      toolCalls: [],
      }));
      await page.evaluate(() => window.__assistantEmit("delta", { messageId: "live-reply", delta: "Before ", body: "Before " }));
      await page.evaluate(() => window.__assistantEmit("tool_started", {
        toolCall: { id: "tool-live", messageId: "live-reply", name: "inspect_host", status: "running", bodyOffset: 7 },
      }));
      await page.evaluate(() => window.__assistantEmit("tool_started", {
        toolCall: { id: "tool-live-2", messageId: "live-reply", name: "inspect_runs", status: "running", bodyOffset: 7 },
      }));
      await page.evaluate(() => window.__assistantEmit("delta", { messageId: "live-reply", delta: "after", body: "Before after" }));
      assert.deepEqual(await visibleMessageFlow(page, "live-reply"), ["text:Before ", "tool:Called 2 tools", "text:after"]);
      assert.equal(await page.locator('[data-message-id="live-reply"] [data-tool-call-id]').count(), 2);
      assert.equal((await page.locator('[data-message-id="live-reply"] [data-tool-cluster-target]').textContent()).trim(), "0 succeeded · 0 failed · 2 in progress");
      await page.evaluate(() => window.__assistantEmit("tool_finished", {
        toolCall: { id: "tool-live", messageId: "live-reply", name: "inspect_host", status: "complete", bodyOffset: 7 },
      }));
      await page.evaluate(() => window.__assistantEmit("tool_finished", {
        toolCall: { id: "tool-live-2", messageId: "live-reply", name: "inspect_runs", status: "error", bodyOffset: 7 },
      }));
      assert.equal((await page.locator('[data-message-id="live-reply"] [data-tool-cluster-target]').textContent()).trim(), "1 succeeded · 1 failed");

      await page.evaluate(() => window.__assistantEmit("snapshot", {
        messages: [{ id: "saved-reply", role: "assistant", body: "Before after", status: "complete", createdAt: "2026-08-01T12:00:00Z" }],
        toolCalls: [
          { id: "tool-saved", messageId: "saved-reply", name: "inspect_host", status: "complete", bodyOffset: 7 },
          { id: "tool-saved-2", messageId: "saved-reply", name: "inspect_runs", status: "error", bodyOffset: 7 },
        ],
      }));
      assert.deepEqual(await visibleMessageFlow(page, "saved-reply"), ["text:Before ", "tool:Called 2 tools", "text:after"]);
      assert.equal(await page.locator('[data-message-id="saved-reply"] [data-tool-call-id]').count(), 2);
      assert.equal((await page.locator('[data-message-id="saved-reply"] [data-tool-cluster-target]').textContent()).trim(), "1 succeeded · 1 failed");

      await page.evaluate(() => window.__assistantEmit("snapshot", {
        messages: [{ id: "single-reply", role: "assistant", body: "Done", status: "complete", createdAt: "2026-08-01T12:00:00Z" }],
        toolCalls: [{
          id: "tool-single", messageId: "single-reply", name: "inspect_host", status: "complete", bodyOffset: 0,
          requestJSON: '{\n  "tool": "inspect_host",\n  "parameters": {}\n}',
          responseJSON: '{\n  "status": "success",\n  "content": {\n    "cpu": 12\n  }\n}',
        }],
      }));
      assert.deepEqual(await visibleMessageFlow(page, "single-reply"), ["tool:Called 1 tool", "text:Done"]);
      assert.equal((await page.locator('[data-message-id="single-reply"] [data-tool-cluster-target]').textContent()).trim(), "1 succeeded · 0 failed");
      const singleToolRow = page.locator('[data-tool-call-id="tool-single"]');
      assert.equal(await singleToolRow.getAttribute("open"), null);
      await page.locator('[data-message-id="single-reply"] [data-assistant-tool-cluster] > summary').click();
      await singleToolRow.locator("summary").click();
      assert.equal(await singleToolRow.getAttribute("open"), "");
      assert.match(await singleToolRow.locator("[data-tool-request-json]").textContent(), /"tool": "inspect_host"/);
      assert.match(await singleToolRow.locator("[data-tool-response-json]").textContent(), /"cpu": 12/);
    }

    if (contract === "all" || contract === "alignment") {
      await page.evaluate(() => window.__assistantEmit("snapshot", {
      messages: [
        { id: "user-message", role: "user", body: "Question", status: "complete", createdAt: "2026-08-01T12:00:00Z" },
        { id: "assistant-message", role: "assistant", body: "Answer", status: "streaming", createdAt: "2026-08-01T12:00:00Z" },
      ],
      toolCalls: [],
      }));
      const alignment = await page.evaluate(() => {
      const list = document.querySelector("[data-assistant-message-list]").getBoundingClientRect();
      const user = document.querySelector(".assistant-message--user");
      const assistant = document.querySelector(".assistant-message--assistant");
      const userBounds = user.getBoundingClientRect();
      const assistantBounds = assistant.getBoundingClientRect();
      const quote = document.createElement("blockquote");
      quote.textContent = "Quoted content";
      const body = assistant.querySelector("[data-message-body], [data-message-segment]");
      body.classList.add("markdown-preview");
      body.append(quote);
      const assistantStyle = getComputedStyle(assistant);
      const quoteStyle = getComputedStyle(quote);
      return {
        userRight: Math.abs(userBounds.right - list.right),
        assistantLeft: Math.abs(assistantBounds.left - list.left),
        assistantBorder: assistantStyle.borderLeftWidth,
        assistantPadding: assistantStyle.paddingLeft,
        quoteBorder: quoteStyle.borderLeftWidth,
        quotePadding: quoteStyle.paddingLeft,
      };
      });
      assert.ok(alignment.userRight <= 1, JSON.stringify(alignment));
      assert.ok(alignment.assistantLeft <= 1, JSON.stringify(alignment));
      assert.equal(alignment.assistantBorder, "0px", JSON.stringify(alignment));
      assert.equal(alignment.assistantPadding, "0px", JSON.stringify(alignment));
      assert.equal(alignment.quoteBorder, "0px", JSON.stringify(alignment));
      assert.equal(alignment.quotePadding, "0px", JSON.stringify(alignment));
    }

    process.stdout.write("Assistant UI contract passed.\n");
  } finally {
    await browser.close();
  }
})().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
