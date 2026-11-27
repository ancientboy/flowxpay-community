import { useState } from "react";
import { useLocale } from "./locale";
import { productionControl } from "./ProductionPlatformControls";
import { ControlAction } from "./WorkspaceControls";

type Batch = { id: string; state: string; account_id: string; destination_currency: string; destination_amount: string };
type Route = { channel_id: string; channel_name: string; currency: string };
export type Movement = { kind: string; source_id: string; account_id: string; currency: string; scale: number; available_delta: string; created_at: string };

export function FundEvidence({ merchant, accounts, routes, batches, refresh }: {
  merchant: string; accounts:{id:string;currency:string}[]; routes: Route[]; batches: Batch[]; refresh: () => void;
}) {
  const {locale} = useLocale();
  const zh = locale === "zh-CN";
  const [mode,setMode] = useState<"topup" | "statement" | "receipt" | "group">();
  const [currency,setCurrency] = useState("USDT");
  const locked=batches.filter(b => b.state === "locked");
  const scopes=Array.from(new Map(routes.map(r=>[r.channel_id+"/"+r.currency,r])).values());
  const currencies=Array.from(new Set(locked.map(b=>b.destination_currency)));
  const title = mode === "topup" ? (zh?"确认商户充值到账":"Confirm merchant top-up") : mode === "receipt" ? (zh?"确认上游结算":"Confirm upstream settlement") : mode === "statement" ? (zh ? "登记已核对的上游账单" : "Record reconciled upstream statement") : (zh ? "合并确认线下付款" : "Confirm combined offline transfer");
  const label=(name:string,cn:string,en:string,options:{type?:string;pattern?:string;maxLength?:number}={})=>
    <label>{zh?cn:en}<input name={name} required autoComplete="off" {...options}/></label>;
  return <div className="detail-actions">
    <button className="secondary" disabled={!accounts.length} onClick={()=>setMode("topup")}>{zh?"登记充值到账":"Record received top-up"}</button>
    <button className="secondary" disabled={!scopes.length} onClick={()=>setMode("statement")}>{zh?"登记上游账单":"Record upstream statement"}</button>
    <button className="secondary" disabled={!scopes.length} onClick={()=>setMode("receipt")}>{zh?"确认上游结算":"Confirm upstream settlement"}</button>
    <button className="secondary" disabled={!currencies.some(c=>locked.filter(b=>b.destination_currency===c).length>=2)} onClick={()=>{
      setCurrency(currencies.find(c=>locked.filter(b=>b.destination_currency===c).length>=2) ?? "USDT");setMode("group");
    }}>{zh?"合并确认付款":"Confirm combined transfer"}</button>
    {mode && <ControlAction title={title} submitLabel={title} refresh={refresh} close={()=>setMode(undefined)} action={async f=>{
      const get=(n:string)=>String(f.get(n)??"").trim();
      if(mode==="topup"){
        const [currency,account_id]=get("account").split("/");
        await productionControl("/funds",{action:"topup",merchant_id:merchant,currency,account_id,actual_amount:get("actual"),transfer_reference:get("ref"),paid_on:get("date")});
      }else if(mode==="receipt"){
        const [channel_id,currency]=get("scope").split("/");await productionControl("/funds",{action:"upstream_receipt",merchant_id:merchant,channel_id,currency,batch_id:get("batch"),statement_entry_ids:get("receipt_entries").split(/[\s,，]+/).filter(Boolean),upstream_account:get("upstream_account"),source_minor:get("received"),paid_on:get("date"),transfer_reference:get("ref")});
      }else if(mode==="statement"){
        const [channel_id,currency]=get("scope").split("/");
        await productionControl("/funds",{action:"statement",merchant_id:merchant,channel_id,currency,
          order_id:get("order"),batch_id:get("batch"),entry_id:get("entry"),transfer_reference:get("ref"),paid_on:get("date"),
          source_minor:get("gross"),statement_fee_minor:get("fee"),statement_net_minor:get("net")});
      }else{
        const ids=f.getAll("batch").map(String);
        if(ids.length<2) throw new Error(zh?"请选择至少两个批次":"Select at least two batches");
        await productionControl("/funds",{action:"confirm_group",merchant_id:merchant,batch_ids:ids,destination_currency:currency,
          actual_amount:get("actual"),transfer_reference:get("ref"),paid_on:get("date")});
      }
    }}>
      {mode==="topup"?<>
        <p>{zh?"核对实际到账后登记。此操作立即增加该商户的可代付余额。":"Verify receipt before posting. This immediately increases the merchant’s payout balance."}</p>
        <label>{zh?"入账资金账户":"Credit funds account"}<select name="account" required>{accounts.map(a=><option key={a.currency+a.id} value={a.currency+"/"+a.id}>{a.id} · {a.currency}</option>)}</select></label>
        {label("actual","实际到账金额（原币）","Received amount (account currency)")}{label("ref","到账凭证流水号","Receipt reference",{maxLength:120})}{label("date","到账日期","Receipt date",{type:"date"})}
      </>:mode==="receipt"?<>
        <label>{zh?"通道和原币":"Channel and currency"}<select name="scope">{scopes.map(r=><option key={r.channel_id+r.currency} value={r.channel_id+"/"+r.currency}>{r.channel_name} · {r.currency}</option>)}</select></label>
        {label("upstream_account","上游账户标识","Upstream account ID",{maxLength:120})}
        {label("batch","已核对的上游账单编号","Reconciled upstream batch ID")}
        <label>{zh ? "账单明细编号（部分结算时逐行填写）" : "Statement entry IDs (one per line for partial settlement)"}<textarea name="receipt_entries" rows={3}/></label>
        {label("received","本次实收金额（最小货币单位）","Received amount (minor units)",{pattern:"[0-9]+"})}
        {label("date","上游实收日期","Upstream receipt date",{type:"date"})}
        {label("ref","上游到账流水","Upstream receipt reference",{maxLength:120})}
      </>:mode==="statement"?<>
        <p>{zh?"使用上游已结账单的原始数据。金额均填最小货币单位，例如 300 THB 填 30000。":"Enter figures from the upstream settled statement in minor units, e.g. 30000 for THB 300."}</p>
        <label>{zh?"通道和原币":"Channel and currency"}<select required name="scope">{scopes.map(r=><option key={r.channel_id+r.currency} value={r.channel_id+"/"+r.currency}>{r.channel_name} · {r.currency}</option>)}</select></label>
        {label("order","平台订单号","Platform order ID")}{label("batch","上游账单批次号","Upstream statement batch ID")}
        {label("entry","上游账单明细编号","Statement entry ID")}{label("ref","上游交易流水","Upstream transaction reference")}
        {label("date","账单日期","Statement date",{type:"date"})}{label("gross","账单本金","Statement gross",{pattern:"[0-9]+"})}
        {label("fee","上游实际费用","Actual upstream fee",{pattern:"[0-9]+"})}{label("net","账单净额","Statement net",{pattern:"[0-9]+"})}
      </>:<>
        <label>{zh?"付款币种":"Transfer currency"}<select value={currency} onChange={e=>setCurrency(e.target.value)}>{currencies.map(c=><option key={c}>{c}</option>)}</select></label>
        {locked.filter(b=>b.destination_currency===currency).map(b=><label key={b.id}><input type="checkbox" name="batch" value={b.id}/>{b.account_id} · {b.destination_amount} {currency} · {b.id}</label>)}
        {label("actual","实际转出总额（付款币种）","Actual total transferred")}{label("ref","线下转账流水","Offline transfer reference")}
        {label("date","实际付款日期","Paid on",{type:"date"})}
      </>}
    </ControlAction>}
  </div>;
}
