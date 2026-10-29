"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const {normalizeOpenAICredits: credits, normalizeOpenAIBenefit: benefit} = require("./openai.js");
const now = "2026-10-29T00:00:00Z";
function fixture() {
  return {object:"credit_summary", total_granted:30,total_used:2.125,total_available:27.875,total_paid_available:27.875,
    grants:{object:"list",data:[{object:"credit_summary",id:"synthetic-grant",grant_amount:30,used_amount:2.125,
      effective_at:1767225600,expires_at:1801440000,is_scheduled:false,is_held_for_prepaid_transition:false}]}};
}
function offer() { return {eligible:true,offer:{currency:"USD",cadence:"monthly",period_credit_amount_usd:"70.00",claimable_credit_amount_usd:"35",expires_at:"2026-11-20T00:00:00Z"}}; }
test("normalizes authoritative fractional cents and emits no identity or unknown fields", () => {
  const f=fixture(); f.secret="ignored"; f.grants.data[0].extra="ignored";
  const r=credits(f,now);
  assert.equal(r.balance,2788); assert.equal(r.paid_balance,2788);
  assert.deepEqual(r.grants,[{name:"API credit",granted:3000,remaining:2788,granted_at:"2026-01-01T00:00:00.000Z",expires_at:new Date(1801440000000).toISOString()}]);
  assert.equal(JSON.stringify(r).includes("synthetic"),false); assert.equal(JSON.stringify(r).includes("ignored"),false);
});
test("zero is valid and missing amounts are not zero; balance not derived from grants", () => {
  const f=fixture(); f.total_granted=0; f.total_used=0; f.total_available=0; f.total_paid_available=0; f.grants.data=[];
  assert.equal(credits(f,now).balance,0);
  for(const key of ["total_granted","total_used","total_available"]) {const x={...f};delete x[key];assert.throws(()=>credits(x,now));}
  const x=fixture();x.grants.data=[];assert.equal(credits(x,now).balance,2788);
});
test("debt is preserved, totals must reconcile", () => {
  const f=fixture();f.total_granted=0;f.total_used=0.015;f.total_available=-0.015;delete f.total_paid_available;f.grants.data=[];
  assert.equal(credits(f,now).balance,-2);
  f.total_available=1;assert.throws(()=>credits(f,now));
});
test("uses decimal difference before rounding and handles tiny numeric values", () => {
  const f=fixture();const g=f.grants.data[0];g.grant_amount=1.015;g.used_amount=0.01;
  assert.equal(credits(f,now).grants[0].remaining,101);
  g.grant_amount=1e-7;g.used_amount=0;assert.equal(credits(f,now).grants[0].remaining,0);
});
test("scheduled held future and expired grants never become active expiry; null stays unknown", () => {
  for(const change of [{is_scheduled:true},{is_held_for_prepaid_transition:true},{effective_at:1801440000,expires_at:1900000000},{expires_at:1767225700}]) {
    const f=fixture();Object.assign(f.grants.data[0],change);assert.deepEqual(credits(f,now).grants,[]);
  }
  const f=fixture();f.grants.data[0].expires_at=null;assert.equal("expires_at" in credits(f,now).grants[0],false);
  assert.equal(credits(f,Date.parse(now)).observed_at,"2026-10-29T00:00:00.000Z");
});
test("rejects duplicate or oversized or incomplete grant lists", () => {
  const f=fixture();f.grants.data.push({...f.grants.data[0]});assert.throws(()=>credits(f,now));
  f.grants.data=Array.from({length:201},(_,i)=>({...f.grants.data[0],id:"synthetic-"+i}));assert.throws(()=>credits(f,now));
  for(const more of [true,null,"false"]) {const x=fixture();x.grants.has_more=more;assert.throws(()=>credits(x,now));}
  const x=fixture();x.grants.has_more=false;assert.equal(credits(x,now).balance,2788);
});
test("strict grant shape dates flags and amounts", () => {
  for(const change of [{id:""},{object:"unknown"},{used_amount:31},{used_amount:-1},{grant_amount:NaN},{grant_amount:Infinity},{grant_amount:1e12},{effective_at:null},{expires_at:NaN},{expires_at:1767225600},{is_scheduled:undefined},{is_held_for_prepaid_transition:null}]) {
    const f=fixture();Object.assign(f.grants.data[0],change);assert.throws(()=>credits(f,now));
  }
});
test("malformed top-level inputs and observation dates are rejected", () => {
  for(const f of [null,[],{}, {object:"unknown"}, {...fixture(),total_granted:Infinity}]) assert.throws(()=>credits(f,now));
  for(const date of [undefined,null,NaN,Infinity,"2026-02-30T00:00:00Z","2026-01-01","2026-10-29T24:00:00Z"]) assert.throws(()=>credits(fixture(),date));
});
test("benefit is explicitly an offer, not a balance, monthly and annual", () => {
  const r=benefit(offer(),now);assert.equal(r.period_credit,7000);assert.equal(r.claimable_credit,3500);assert.equal("balance" in r,false);
  const f=offer();f.offer.cadence="annual";assert.equal(benefit(f,now).cadence,"annual");assert.equal(benefit(f,now).period_credit,7000);
});
test("false eligibility drops arbitrary reason, true requires an offer and eligibility presence", () => {
  assert.deepEqual(benefit({eligible:false,ineligibility_reason:"private",offer:null},now),{eligible:false,observed_at:"2026-10-29T00:00:00.000Z"});
  for(const x of [{},{eligible:"true"},{eligible:true,offer:null}]) assert.throws(()=>benefit(x,now));
});
test("benefit decimal strings round once and reject other numeric syntaxes", () => {
  const f=offer();f.offer.claimable_credit_amount_usd="1.005";assert.equal(benefit(f,now).claimable_credit,101);
  for(const value of ["$2","1e2","NaN","Infinity"," 2","2 ","-1","1000000000000",2,""]) {
    const x=offer();x.offer.claimable_credit_amount_usd=value;assert.throws(()=>benefit(x,now));
  }
  f.offer.claimable_credit_amount_usd="0";assert.equal(benefit(f,now).claimable_credit,0);
});
test("benefit currency bounds cadence expiry and amount presence", () => {
  for(const change of [{currency:"EUR"},{cadence:"weekly"},{period_credit_amount_usd:"0"},{claimable_credit_amount_usd:"201"},{claimable_credit_amount_usd:undefined},{expires_at:null}]) {
    const f=offer();Object.assign(f.offer,change);assert.throws(()=>benefit(f,now));
  }
  const f=offer();f.offer.expires_at="2026-01-01T00:00:00Z";assert.equal(benefit(f,now).eligible,true);
});
