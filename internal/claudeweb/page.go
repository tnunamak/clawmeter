package claudeweb

import "html/template"

type checkPageData struct {
	Bookmark template.URL
	UsageURL string
	// Proven means the bookmark has delivered a result before, so the save
	// step moves behind a disclosure.
	Proven bool
	Ended  string
}

// checkPage is the one page a check opens. Claude Usage opens in a new tab so
// this tab can keep showing progress.
var checkPage = template.Must(template.New("check").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Check Claude resets</title>
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
  details { margin-top: 1.5rem; }
</style>
{{define "save"}}Drag <a class="button bookmark" id="bookmarklet" href="{{.Bookmark}}" draggable="true">Clawmeter resets</a> to your bookmarks bar.<br>
<small>Or <button id="copy" type="button">Copy bookmark URL</button> and paste it as a new bookmark's URL.</small>{{end}}
<h1>Check Claude resets</h1>
{{if .Proven}}
<p><a class="button primary" href="{{.UsageURL}}" target="_blank" rel="noopener noreferrer">Open Claude Usage</a></p>
<p>Then click the Clawmeter resets bookmark there.</p>
{{else}}
<ol>
  <li>{{template "save" .}}</li>
  <li><a class="button primary" href="{{.UsageURL}}" target="_blank" rel="noopener noreferrer">Open Claude Usage</a></li>
  <li>Click the bookmark there.</li>
</ol>
{{end}}
<p id="status" role="status" data-state="waiting">Waiting for the bookmark…</p>
{{if .Proven}}
<details><summary>Bookmark missing? Save it again</summary><p>{{template "save" .}}</p></details>
{{else}}
<p><small>If your browser asks to let claude.ai access your local network, allow it.</small></p>
{{end}}
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
  const show = (state, message) => { status.dataset.state = state; status.textContent = message; };
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
