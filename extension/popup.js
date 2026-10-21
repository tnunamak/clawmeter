"use strict";

const element = id => document.getElementById(id);

function paragraph(text, className) {
  const p = document.createElement("p");
  p.textContent = text;
  if (className) p.className = className;
  return p;
}

function row(name, value, className) {
  const div = document.createElement("div");
  div.className = "row" + (className ? " " + className : "");
  div.append(paragraph(name), paragraph(value));
  return div;
}

function link(text, href) {
  const a = document.createElement("a");
  a.textContent = text;
  a.href = href;
  a.target = "_blank";
  return a;
}

function code(text) {
  const element = document.createElement("code");
  element.textContent = text;
  return element;
}

// fix is a warning followed by the one action that clears it.
function fix(text, action, className = "warn") {
  const p = paragraph(text + " ", className);
  p.append(action);
  return p;
}

// openClaude asks the worker to open a pinned claude.ai tab and read it.
function openClaude() {
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = "Open a pinned tab";
  button.addEventListener("click", async () => {
    button.disabled = true;
    button.textContent = "Opening…";
    await chrome.runtime.sendMessage("open-claude");
  });
  return button;
}

const money = (minor, currency) => {
  try {
    return new Intl.NumberFormat(undefined, { style: "currency", currency }).format(minor / 100);
  } catch {
    return (minor / 100).toFixed(2) + " " + currency;
  }
};

// connection says whether the last readings reached the Clawmeter app.
function connection(delivery) {
  if (!delivery.length) return { text: "Waiting for the first reading", tone: "" };
  if (delivery.includes("unreachable")) return { text: "The Clawmeter app isn't running", tone: "bad" };
  if (delivery.includes("rejected")) return { text: "Clawmeter refused a reading; update Clawmeter", tone: "bad" };
  if (delivery.includes("failed")) return { text: "Clawmeter couldn't save a reading", tone: "bad" };
  return { text: "Sending to Clawmeter", tone: "ok" };
}

// unchecked says that some tabs were asleep or did not answer, so accounts
// signed in only there are missing from this reading.
function unchecked(status) {
  const count = status.unchecked || 0;
  return count ? [paragraph(`${count} tab${count === 1 ? "" : "s"} didn't answer; accounts signed in only there aren't shown. Open ${count === 1 ? "it" : "them"} to include ${count === 1 ? "it" : "them"}.`, "warn")] : [];
}

// untracked tells how to add accounts that Clawmeter does not track.
function untracked(accounts) {
  return accounts.some(account => account.delivery === "ignored")
    ? [fix("Add it to Clawmeter with", code("clawmeter providers source help claude"), "muted")]
    : [];
}

function claude(status) {
  if (!status) return [paragraph("Not read yet.", "muted")];
  switch (status.state) {
    case "ok":
      if (!status.accounts?.length) {
        return status.unchecked ? unchecked(status) : [paragraph("No Claude plan in this browser.", "muted")];
      }
      return [...status.accounts.map(account => account.error ? row(account.name, "couldn't read", "warn") : account.delivery === "ignored"
        ? row(account.name, "not in Clawmeter", "muted")
        : row(account.name, account.resets ? `${account.resets} reset${account.resets === 1 ? "" : "s"}` : "no resets")),
        ...unchecked(status), ...untracked(status.accounts)];
    case "no_tab":
      return [fix("Resets are read while a claude.ai tab is open.", openClaude(), "muted")];
    case "signed_out":
      return [fix("Signed out of claude.ai.", link("Sign in", "https://claude.ai/login"))];
    default:
      return [paragraph("Couldn't read claude.ai: " + status.error, "warn")];
  }
}

function credits(status) {
  if (!status) return [paragraph("Not read yet.", "muted")];
  if (status.state === "signed_out") return [fix("Signed out of the Claude Console.", link("Sign in", "https://platform.claude.com/"))];
  if (status.state !== "ok") return [paragraph("Couldn't read the Claude Console: " + status.error, "warn")];
  const rows = status.pools.map(pool => row(pool.name, money(pool.balance, pool.currency)));
  for (const failure of status.failures || []) rows.push(row(failure.name, "couldn't read", "warn"));
  rows.push(...unchecked(status));
  return rows.length ? rows : [paragraph("No API organizations.", "muted")];
}

function render(status) {
  const state = connection(status?.delivery || []);
  element("connection").className = state.tone;
  element("connection-text").textContent = state.text;
  element("claude").replaceChildren(...claude(status?.claude));
  element("console").replaceChildren(...credits(status?.console));
  element("updated").textContent = status?.at
    ? "Updated " + new Date(status.at).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })
    : "";
}

async function load() {
  const { status } = await chrome.storage.session.get("status");
  render(status);
}

element("refresh").addEventListener("click", async () => {
  const button = element("refresh");
  button.disabled = true;
  button.textContent = "Reading…";
  try {
    await chrome.runtime.sendMessage("poll");
    await load();
  } finally {
    button.disabled = false;
    button.textContent = "Refresh";
  }
});

// A check that was already running when the popup opened lands here.
chrome.storage.session.onChanged.addListener(changes => {
  if (changes.status) render(changes.status.newValue);
});

void load();
