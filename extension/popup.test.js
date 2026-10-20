"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const vm = require("node:vm");

// popup loads popup.js against a minimal DOM and returns the rendered text.
function popup() {
  const node = () => ({ children: [], textContent: "", className: "",
    append(...nodes) { this.children.push(...nodes); },
    replaceChildren(...nodes) { this.children = nodes; },
    addEventListener() {} });
  const elements = {};
  const context = vm.createContext({
    document: { createElement: node, getElementById: id => (elements[id] ??= node()) },
    chrome: { storage: { session: { get: async () => ({}), onChanged: { addListener() {} } } }, runtime: {} },
    Intl, Date,
  });
  vm.runInContext(readFileSync(require.resolve("./popup.js"), "utf8"), context);
  const text = n => n.children.length ? n.children.map(text).join(" | ") : n.textContent;
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
