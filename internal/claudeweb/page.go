package claudeweb

import "html/template"

type checkPageData struct {
	Bookmark  template.URL
	Title     string
	OpenURL   string
	OpenLabel string
	Site      string
	Ended     string
}

// checkPage is the one page a check opens. The Claude page opens in a new tab so
// this tab can keep showing progress. The save step is always shown: a
// browser can't tell Clawmeter whether the bookmark still exists, so the page
// never assumes it does. If the bookmark stays silent after Claude Usage
// opens, the page points back to that step. One bookmark serves both Claude
// Usage (resets) and the Claude Console (API credits).
var checkPage = template.Must(template.New("check").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{.Title}}</title>
<style>
  body { max-width: 34rem; margin: 3rem auto; padding: 0 1.25rem; font: 16px/1.6 system-ui, sans-serif; color: #202124; }
  h1 { font-size: 1.4rem; margin-bottom: 1.25rem; }
  ol { padding-left: 1.25rem; }
  li { margin: 1rem 0; }
  a.button, button { display: inline-block; font: inherit; padding: .4rem .8rem; border-radius: 6px; border: 1px solid #888; background: #f5f5f5; color: #111; text-decoration: none; cursor: pointer; }
  a.primary { background: #1a5fd0; border-color: #1a5fd0; color: #fff; }
  a.bookmark { cursor: grab; }
  small { color: #555; }
  #status { margin-top: 1.5rem; padding: .6rem .8rem; border-radius: 6px; background: #f1f3f4; }
  #status[data-state="done"] { background: #e6f4ea; font-weight: 600; }
  #status[data-state="retry"], #status[data-state="ended"] { background: #fef7e0; }
  #save.attention { background: #fef7e0; border-radius: 6px; padding: .4rem .6rem; }
  #hint { margin-top: .75rem; }
</style>
<h1>{{.Title}}</h1>
<ol>
  <li id="save">If you don't have the bookmark yet, or saved it before API credits were added, drag <a class="button bookmark" id="bookmarklet" href="{{.Bookmark}}" draggable="true">Clawmeter</a> to your bookmarks bar.<br>
  <small>Or <button id="copy" type="button">Copy bookmark URL</button> and paste it as a new bookmark's URL.</small></li>
  <li><a class="button primary" id="open" href="{{.OpenURL}}" target="_blank" rel="noopener noreferrer">{{.OpenLabel}}</a></li>
  <li>Click the Clawmeter bookmark there.</li>
</ol>
<p id="status" role="status" data-state="waiting">Waiting for the bookmark…</p>
<p id="hint" hidden>No response from the bookmark. If it's missing or old, save it again in step 1.</p>
<p><small>If your browser asks to let {{.Site}} access your local network, allow it.</small></p>
<script>
  document.querySelector("#copy").addEventListener("click", async event => {
    const value = document.querySelector("#bookmarklet").getAttribute("href");
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      const field = document.createElement("textarea");
      field.value = value;
      document.body.append(field);
      field.select();
      document.execCommand("copy");
      field.remove();
    }
    event.target.textContent = "Copied";
  });
  const status = document.querySelector("#status");
  const hint = document.querySelector("#hint");
  const show = (state, message) => {
    status.dataset.state = state;
    status.textContent = message;
    if (state !== "waiting") hint.hidden = true;
  };
  // A bookmark that never answers is most likely missing.
  document.querySelector("#open").addEventListener("click", () => {
    setTimeout(() => {
      if (status.dataset.state !== "waiting") return;
      hint.hidden = false;
      document.querySelector("#save").classList.add("attention");
    }, 45000);
  });
  const poll = async () => {
    try {
      const response = await fetch("/status", { cache: "no-store" });
      const { state, message } = await response.json();
      show(state, message);
      if (state === "done" || state === "ended") return;
    } catch {
      return show("ended", {{.Ended}});
    }
    setTimeout(poll, 1000);
  };
  poll();
</script>
</html>`))
