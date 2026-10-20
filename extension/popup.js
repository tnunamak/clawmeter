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
  if (delivery.includes("unreachable")) return { text: "Clawmeter isn't running on this computer", tone: "bad" };
  if (delivery.includes("rejected")) return { text: "Clawmeter refused a reading; update Clawmeter", tone: "bad" };
  return { text: "Sending to Clawmeter", tone: "ok" };
}

function claude(status) {
  if (!status) return [paragraph("Not read yet.", "muted")];
  switch (status.state) {
    case "ok":
      if (!status.accounts?.length) return [paragraph("No Claude plan in this browser.", "muted")];
      return status.accounts.map(account => account.error ? row(account.name, "couldn't read", "warn") : account.delivery === "ignored"
        ? row(account.name, "not set up in Clawmeter", "muted")
        : row(account.name, account.resets ? `${account.resets} reset${account.resets === 1 ? "" : "s"}` : "no resets"));
    case "no_tab":
      return [paragraph("Keep a claude.ai tab open. Clawmeter reads resets through it.", "warn")];
    case "signed_out":
      return [paragraph("Sign in to claude.ai.", "warn")];
    default:
      return [paragraph("Couldn't read claude.ai: " + status.error, "warn")];
  }
}

function credits(status) {
  if (!status) return [paragraph("Not read yet.", "muted")];
  if (status.state === "signed_out") return [paragraph("Sign in to the Claude Console.", "warn")];
  if (status.state !== "ok") return [paragraph("Couldn't read the Claude Console: " + status.error, "warn")];
  const rows = status.pools.map(pool => row(pool.name, money(pool.balance, pool.currency)));
  for (const failure of status.failures || []) rows.push(row(failure.name, "couldn't read", "warn"));
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
