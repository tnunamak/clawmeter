"use strict";
const test = require("node:test"), assert = require("node:assert/strict");
const vm = require("node:vm"), fs = require("node:fs"), { webcrypto } = require("node:crypto");
function setup(crypto = webcrypto) {
  let at = Date.parse("2026-10-29T12:00:00Z"); const receipts = [];
  const context = vm.createContext({ URL, Date, Headers, crypto, TextEncoder, Uint8Array });
  for (const file of ["openai.js", "openai-observer.js"]) vm.runInContext(fs.readFileSync(require.resolve("./"+file), "utf8"), context);
  const observe = context.createOpenAICollector({ emit: r => receipts.push(JSON.parse(JSON.stringify(r))), hash: async s => webcrypto.subtle.digest("SHA-256", new TextEncoder().encode(s)).then(b => Buffer.from(b).toString("hex")), now: () => at });
  const read = (url, body, extra={}) => observe({ url, body, at, ...extra });
  return { read, receipts, advance: ms => { at += ms; }, now: () => at, context };
}
const base = "https://chatgpt.com/backend-api/product-account-links/";
const offer = () => ({ eligible:true, offer:{currency:"USD",cadence:"monthly",period_credit_amount_usd:"70.00",claimable_credit_amount_usd:"35",expires_at:"2026-11-20T00:00:00Z"} });
const discovery = org => ({destinations:[{destination_type:"api_org",destination_account_id:org,display_name:"Synthetic organization"}]});
test("an offer needs an exact account to organization discovery binding, regardless of arrival order", async () => {
  const s=setup();
  await s.read(base+"accounts/account-a/benefits/chatgpt_api_credits_promo?destination_type=api_org",offer());
  assert.equal(s.receipts.length,0);
  await s.read(base+"discovery?account_id=account-b&destination_type=api_org",discovery("org-b"));
  assert.equal(s.receipts.length,0);
  await s.read(base+"discovery?account_id=account-a&destination_type=api_org",discovery("org-a"));
  assert.equal(s.receipts.length,1);
  assert.equal(s.receipts[0].benefit.claimable_credit,3500);
  assert.equal(s.receipts[0].benefit.link_state,"unknown");
  assert.match(s.receipts[0].pool,/^[a-f0-9]{64}$/);
  assert.doesNotMatch(JSON.stringify(s.receipts),/org-a|account-a|balance/);
});
test("link changes have their own observation time and never freshen the offer", async () => {
  const s=setup();
  await s.read(base+"discovery?account_id=account-a&destination_type=api_org",discovery("org-a"));
  await s.read(base+"accounts/account-a/benefits/chatgpt_api_credits_promo?destination_type=api_org",offer());
  const first=s.receipts[0].benefit.observed_at;
  s.advance(60000);
  await s.read(base+"organizations/org-a",{link:{},can_link:false});
  assert.equal(s.receipts[1].link.state,"linked");
  assert.equal(s.receipts[1].benefit,undefined);
  assert.notEqual(s.receipts[1].link.observed_at,first);
  // A late older response cannot revive the unlinked state.
  await s.read(base+"organizations/org-a",{link:null,can_link:true},{at:s.now()-1});
  assert.equal(s.receipts.length,2);
});
test("stale discovery, wrong origin, unrelated endpoints and mutation responses never create offers", async () => {
  const s=setup();
  await s.read(base+"discovery?account_id=account-a&destination_type=api_org",discovery("org-a"));
  s.advance(300001);
  await s.read(base+"accounts/account-a/benefits/chatgpt_api_credits_promo?destination_type=api_org",offer());
  await s.read(base+"organizations/org-a",{link:null,can_link:true},{method:"POST"});
  await s.read("https://untrusted.example/backend-api/product-account-links/organizations/org-a",{link:null,can_link:true});
  assert.equal(s.receipts.length,0);
});
test("credit receipts require an explicit organization header and retain genuine zero", async () => {
  const s=setup(), url="https://api.openai.com/v1/dashboard/billing/credit_grants";
  const body={object:"credit_summary",total_granted:0,total_used:0,total_available:0,grants:{object:"list",data:[]}};
  await s.read(url,body); assert.equal(s.receipts.length,0);
  await s.read(url,body,{organization:"org-a"});
  assert.equal(s.receipts[0].credits.balance,0); assert.equal(s.receipts[0].benefit,undefined);
  s.advance(1); await s.read(url,{...body,total_available:undefined},{organization:"org-a"}).then(()=>assert.fail("malformed receipt accepted"),()=>{});
  assert.equal(s.receipts.length,1);
});
test("installed fetch observer returns the original response and emits only normalized financial data", {timeout:5000}, async () => {
  let releaseHash;
  const hashReady=new Promise(resolve=>{releaseHash=resolve;});
  const s=setup({subtle:{digest:async(...args)=>{await hashReady;return webcrypto.subtle.digest(...args);}}}), emitted=[];
  const url="https://api.openai.com/v1/dashboard/billing/credit_grants";
  const body={object:"credit_summary",total_granted:0,total_used:0,total_available:0,grants:{object:"list",data:[]}};
  const response={ok:true,url,clone:()=>({text:async()=>JSON.stringify(body)})};
  function XHR() {} XHR.prototype.open=function(){}; XHR.prototype.setRequestHeader=function(){};
  let finish;
  const delivered=new Promise(resolve=>{finish=resolve;});
  const win={location:{origin:"https://platform.openai.com",href:"https://platform.openai.com/settings/organization/billing/overview"},fetch:async()=>response,XMLHttpRequest:XHR,postMessage:r=>{emitted.push(r);finish();}};
  s.context.installOpenAIObserver(win);
  assert.equal(await win.fetch(url,{headers:{"OpenAI-Organization":"org-a",Authorization:"synthetic-secret"}}),response);
  // Fetch returns before hashing completes. Wait for the actual delivery,
  // independent of WebCrypto thread scheduling or runner speed.
  assert.equal(emitted.length,0);
  releaseHash();
  await delivered;
  assert.equal(emitted.length,1);
  assert.doesNotMatch(JSON.stringify(emitted),/synthetic-secret|Authorization|org-a/);
  assert.equal(emitted[0].receipt.credits.balance,0);
});
