package tray

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type recoveryHelpLink struct{ Label, URL string }
type recoveryHelpPage struct {
	Title, Account, Description string
	Commands                    []string
	Links                       []recoveryHelpLink
}
type helpCommand struct{ Text, Encoded string }
type helpDocument struct {
	Title, Account, Description string
	Commands                    []helpCommand
	Links                       []recoveryHelpLink
}

func helpLink(raw string) (string, error) {
	if !utf8.ValidString(raw) || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("help link contains invalid characters")
	}
	decoded, decodeErr := url.PathUnescape(raw)
	if decodeErr != nil || strings.IndexFunc(decoded, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("help link contains encoded control characters")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Hostname() == "" {
		return "", fmt.Errorf("help link must be HTTPS with a host and no credentials")
	}
	host := u.Hostname()
	if net.ParseIP(host) == nil {
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", fmt.Errorf("invalid help link host")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
					return "", fmt.Errorf("invalid help link host")
				}
			}
		}
	}
	return u.String(), nil
}

// Reject a symlink at the supplied directory, then pin it with OpenRoot.
// Ancestors may be OS-managed links, such as /var on macOS or a linked cache.
func helpRoot(dir string) (*os.Root, string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, "", err
	}
	before, err := os.Lstat(abs)
	if err != nil {
		return nil, "", err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, "", fmt.Errorf("help directory must be a real directory")
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, "", err
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		root.Close()
		return nil, "", fmt.Errorf("help directory changed while opening")
	}
	if err := root.Chmod(".", 0700); err != nil {
		root.Close()
		return nil, "", err
	}
	return root, abs, nil
}

func writeRecoveryHelp(dir string, page recoveryHelpPage) (string, error) {
	doc := helpDocument{Title: page.Title, Account: page.Account, Description: page.Description}
	for _, text := range append([]string{page.Title, page.Account, page.Description}, page.Commands...) {
		if !utf8.ValidString(text) {
			return "", fmt.Errorf("help text must be valid UTF-8")
		}
	}
	for _, command := range page.Commands {
		doc.Commands = append(doc.Commands, helpCommand{Text: command, Encoded: base64.StdEncoding.EncodeToString([]byte(command))})
	}
	for _, link := range page.Links {
		if !utf8.ValidString(link.Label) {
			return "", fmt.Errorf("help label must be valid UTF-8")
		}
		target, err := helpLink(link.URL)
		if err != nil {
			return "", err
		}
		doc.Links = append(doc.Links, recoveryHelpLink{Label: link.Label, URL: target})
	}
	var content bytes.Buffer
	if err := recoveryTemplate.Execute(&content, doc); err != nil {
		return "", err
	}
	root, abs, err := helpRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	const name = "recovery-help.html"
	if info, err := root.Lstat(name); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("help target must be a regular file, not a link")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	temporary := ".recovery-help-" + hex.EncodeToString(nonce) + ".tmp"
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer root.Remove(temporary)
	if _, err = file.Write(content.Bytes()); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := root.Rename(temporary, name); err != nil {
		return "", err
	}
	target := filepath.ToSlash(filepath.Join(abs, name))
	if !strings.HasPrefix(target, "/") {
		target = "/" + target
	}
	return (&url.URL{Scheme: "file", Path: target}).String(), nil
}

var recoveryTemplate = template.Must(template.New("help").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>
:root{color-scheme:light dark;font-family:system-ui,sans-serif}
body{max-width:44rem;margin:3rem auto;padding:0 1rem;line-height:1.55}
h1{font-size:1.65rem;line-height:1.25}.account{font-weight:600}
p{white-space:pre-wrap}pre{overflow:auto;padding:.75rem;background:Canvas;border:1px solid GrayText}
code{font-family:ui-monospace,monospace;white-space:pre}button{font:inherit;padding:.3rem .7rem}
a{overflow-wrap:anywhere}.feedback{margin-left:.6rem}li{margin:.5rem 0}
section+section{margin-top:1.5rem}textarea{display:block;box-sizing:border-box;width:100%;min-height:5rem;margin-top:.75rem;padding:.75rem;font:inherit;font-family:ui-monospace,monospace}
</style></head><body>
<h1>{{.Title}}</h1>{{if .Account}}<p class="account">Account: {{.Account}}</p>{{end}}
<p>{{.Description}}</p>
{{range .Commands}}<section><pre><code>{{.Text}}</code></pre><button type="button" data-command="{{.Encoded}}">Copy command</button><span class="feedback" role="status" aria-live="polite"></span></section>{{end}}
{{if .Links}}<ul>{{range .Links}}<li><a href="{{.URL}}" rel="noreferrer noopener">{{.Label}}</a></li>{{end}}</ul>
{{if .Account}}<p>Use the browser account for {{.Account}}.</p>{{end}}{{end}}
<script>
document.querySelectorAll('button[data-command]').forEach(button=>{
 button.addEventListener('click',async()=>{
  const feedback=button.nextElementSibling;
  button.parentElement.querySelector('textarea')?.remove();
  const raw=atob(button.dataset.command);
  const command=new TextDecoder('utf-8',{fatal:true}).decode(Uint8Array.from(raw,c=>c.charCodeAt(0)));
  try{
   if(!navigator.clipboard||!navigator.clipboard.writeText)throw new Error('Clipboard unavailable');
   await navigator.clipboard.writeText(command);feedback.textContent='Copied.';return;
  }catch(_){}
  const field=document.createElement('textarea');field.value=command;field.readOnly=true;
  field.setAttribute('aria-label','Command to copy');button.parentElement.appendChild(field);field.focus();field.select();
  let copied=false;try{copied=document.execCommand('copy');}catch(_){}
  if(copied){field.remove();button.focus();feedback.textContent='Copied.';}
  else{feedback.textContent='Copy unavailable. The command is selected; copy it manually.';}
 });
});
</script></body></html>
`))
