"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const vm = require("node:vm");

// popup loads popup.js against a minimal DOM and returns the rendered text.
function popup() {
  const node = tag => ({ tag, children: [], textContent: "", className: "",
    append(...nodes) { this.children.push(...nodes); },
    replaceChildren(...nodes) { this.children = nodes; },
    addEventListener() {} });
  const elements = {};
  const context = vm.createContext({
    document: { createElement: node, getElementById: id => (elements[id] ??= node()) },
    chrome: { storage: { session: { get: async () => ({}), onChanged: { addListener() {} } } }, runtime: { sendMessage: async () => true } },
    Intl, Date,
  });
  vm.runInContext(readFileSync(require.resolve("./popup.js"), "utf8"), context);
  const text = n => typeof n === "string" ? n : [n.textContent, ...n.children.map(text)].filter(Boolean).join(" | ");
  return { context, text };
}

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
  assert.equal(context.connection(["saved", "ignored"]).tone, "ok");
  assert.match(context.connection(["unreachable"]).text, /isn't running/);
  assert.match(context.connection(["rejected"]).text, /update Clawmeter/);
  assert.match(context.connection(["saved", "failed"]).text, /couldn't save/);
});
