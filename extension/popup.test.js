"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const vm = require("node:vm");

// popup loads popup.js against a minimal DOM and returns the rendered text.
function popup(initialStatus) {
  const node = tag => ({ tag, children: [], textContent: "", className: "", handlers: {},
    append(...nodes) { this.children.push(...nodes); },
    after(node) { this.sibling = node; },
    setAttribute(name, value) { this[name] = value; },
    replaceChildren(...nodes) { this.children = nodes; },
    addEventListener(type, handler) { this.handlers[type] = handler; } });
  const elements = {};
  const context = vm.createContext({
    document: { createElement: node, getElementById: id => (elements[id] ??= node()) },
    chrome: { storage: { session: { get: async () => ({ status: initialStatus }), onChanged: { addListener() {} } } }, runtime: { sendMessage: async () => true } },
    Intl, Date,
  });
  vm.runInContext(readFileSync(require.resolve("./popup.js"), "utf8"), context);
  const text = n => typeof n === "string" ? n : [n.textContent, ...n.children.map(text)].filter(Boolean).join(" | ");
  return { context, text };
}

test("a failed refresh preserves the last status and can retry", async () => {
  const initialStatus = { delivery: ["saved"], claude: { state: "ok", accounts: [] } };
  const { context } = popup(initialStatus);
  context.render(initialStatus);
  const button = context.document.getElementById("refresh");
  const status = context.document.getElementById("connection-text");
  const previous = status.textContent;
  let calls = 0;
  context.chrome.runtime.sendMessage = async message => {
    assert.equal(message, "poll");
    if (++calls === 1) throw new Error("synthetic poll error");
    return true;
  };
  await button.handlers.click();
  assert.equal(button.disabled, false);
  assert.equal(button.textContent, "Refresh");
  assert.equal(status.textContent, previous);
  assert.equal(button.sibling.role, "status");
  assert.match(button.sibling.textContent, /Couldn't refresh/);
  await button.handlers.click();
  assert.equal(button.sibling.textContent, "");
  assert.equal(calls, 2);
});

test("opening a pinned tab can be retried after the worker rejects", async () => {
  const { context } = popup();
  let sends = 0;
  context.chrome.runtime.sendMessage = async message => {
    assert.equal(message, "open-claude");
    if (++sends === 1) throw new Error("synthetic worker failure");
    return true;
  };
  const button = context.openClaude();
  await button.handlers.click();
  assert.equal(button.disabled, false);
  assert.match(button.textContent, /Try again/);
  await button.handlers.click();
  assert.equal(sends, 2);
});

test("opening a pinned tab can be retried after the worker replies false", async () => {
  const { context } = popup();
  let sends = 0;
  context.chrome.runtime.sendMessage = async () => ++sends === 1 ? false : true;
  const button = context.openClaude();
  await button.handlers.click();
  assert.equal(button.disabled, false);
  assert.match(button.textContent, /Try again/);
  await button.handlers.click();
  assert.equal(sends, 2);
});

test("a partial claude.ai reading with no accounts says a tab did not answer, not that there is no plan", () => {
  const { context, text } = popup();
  const rendered = context.claude({ state: "ok", accounts: [], unchecked: 1 }).map(text).join("\n");
  assert.match(rendered, /1 tab didn't answer/);
  assert.doesNotMatch(rendered, /No Claude plan/);
  assert.match(context.claude({ state: "ok", accounts: [], unchecked: 0 }).map(text).join("\n"), /No Claude plan/);
});

test("a partial Console reading lists its pools and the tabs that did not answer", () => {
  const { context, text } = popup();
  const rendered = context.credits({ state: "ok", unchecked: 2, failures: [],
    pools: [{ name: "Team", balance: 1250, currency: "USD" }] }).map(text).join("\n");
  assert.match(rendered, /Team/);
  assert.match(rendered, /2 tabs didn't answer/);
});

test("each warning carries the action that clears it", () => {
  const { context, text } = popup();
  const [noTab] = context.claude({ state: "no_tab" });
  assert.match(text(noTab), /claude\.ai tab is open/);
  // A closed tab is a normal state, not a warning.
  assert.equal(noTab.className, "muted");
  assert.equal(noTab.children.at(-1).tag, "button");
  assert.equal(noTab.children.at(-1).textContent, "Open a pinned tab");
  const [signedOut] = context.claude({ state: "signed_out" });
  assert.equal(signedOut.children.at(-1).href, "https://claude.ai/login");
  const [consoleOut] = context.credits({ state: "signed_out" });
  assert.equal(consoleOut.children.at(-1).href, "https://platform.claude.com/");
});

test("an account Clawmeter does not track says how to add it", () => {
  const { context, text } = popup();
  const rendered = account => context.claude({ state: "ok", accounts: [account] }).map(text).join("\n");
  assert.match(rendered({ name: "Team", resets: 0, delivery: "ignored" }), /not in Clawmeter[\s\S]*providers source help claude/);
  assert.doesNotMatch(rendered({ name: "Max", resets: 1, delivery: "saved" }), /providers source/);
});

test("the connection line names each way a reading can fail", () => {
  const { context } = popup();
  assert.match(context.connection(undefined).text, /Waiting for the first reading/);
  assert.equal(context.connection({ delivery: ["saved", "ignored"] }).tone, "ok");
  assert.match(context.connection({ delivery: ["unreachable"], off: true }).text, /isn't running/);
  assert.match(context.connection({ delivery: ["rejected"], refused: "rejected" }).text, /update Clawmeter/);
  assert.match(context.connection({ delivery: ["saved", "failed"], refused: "failed" }).text, /couldn't save/);
  // A check that sent nothing shows what the last one found.
  assert.match(context.connection({ delivery: [], off: true }).text, /isn't running/);
  assert.match(context.connection({ delivery: ["foreign"], off: "foreign" }).text, /Another user's Clawmeter/);
  assert.match(context.connection({ delivery: [], refused: "failed" }).text, /couldn't save/);
  assert.match(context.connection({ delivery: [], refused: "rejected" }).text, /update Clawmeter/);
  assert.match(context.connection({ delivery: [] }).text, /Nothing to send/);
});
