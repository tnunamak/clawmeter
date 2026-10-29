"use strict";
// The isolated-world relay has extension privileges; the page observer does not.
window.addEventListener("message", event => {
  if (event.source !== window || event.origin !== "https://platform.openai.com" || event.data?.type !== "clawmeter-openai-receipt") return;
  void chrome.runtime.sendMessage({ type: "openai-receipt", receipt: event.data.receipt }).catch(() => {});
});
