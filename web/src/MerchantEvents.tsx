import {useState,type FormEvent} from "react";
import {request} from "./api";
import {Dialog} from "./Dialog";
import {useLocale} from "./locale";
import {LoadState,useWorkspaceData,WorkspaceTable} from "./WorkspaceInventoryUI";
type Entry={id:string;order_id:string;status:string;state:string;attempts:number;last_result:string|null};
type Snapshot={revision:string|null;endpoint:string|null;events:Entry[]};
export function MerchantEvents({merchant,token,write=true,openOrder}:{merchant?:string;token:string;write?:boolean;openOrder?:(id:string)=>void}){
 const {locale}=useLocale();const tr=(cn:string,en:string)=>locale==="zh-CN"?cn:en;
 const [details,setDetails]=useState<Entry>();
 const [outcome,setOutcome]=useState("");
 const [before,setBefore]=useState("");const [action,setAction]=useState<"configure"|"rotate"|Entry>();const [key,setKey]=useState("");const [busy,setBusy]=useState(false);const [error,setError]=useState("");
 const params=new URLSearchParams();if(merchant)params.set("merchant_id",merchant);if(before)params.set("before",before);if(outcome)params.set("outcome",outcome);
 const path="/flowxpay/ops/api/merchant-events?"+params;const data=useWorkspaceData<Snapshot>(path,token);
 const attempts=useWorkspaceData<{attempt:number;result:string;finished_at:string}[]>(details?path+"&event_id="+details.id:undefined,token);
 const start=(v:typeof action)=>{setError("");setKey("");setAction(v)};
 async function submit(e:FormEvent<HTMLFormElement>){
 e.preventDefault();if(busy||!action)return;setBusy(true);setError("");const values=new FormData(e.currentTarget);
 try{const res=await request<{key?:string}>(path,token,{method:"POST",body:JSON.stringify(typeof action==="string"?{action,endpoint:values.get("endpoint"),revision:data.data?.revision??""}:{action:"replay",event_id:action.id})});data.refresh();if(res.key)setKey(res.key);else setAction(undefined)}
 catch{setError(tr("未确认操作成功。请先刷新通知列表核对。","Action not confirmed. Refresh delivery status before retrying."))}
 finally{setBusy(false)}}
 return <section className="workspace-panel"><div className="heading panel-heading"><h2>{tr("商户交易通知","Merchant transaction notifications")}</h2><button className="secondary" onClick={data.reload}>{tr("刷新","Refresh")}</button></div>
 <LoadState state={data} label={tr("通知","Notifications")}>{s=><><p className="wrap">{s.endpoint??tr("尚未配置通知地址","Notification endpoint not configured")}</p>
 {!merchant&&write&&<button className="secondary" onClick={()=>start(s.endpoint?"rotate":"configure")}>{s.endpoint?tr("更新地址及签名密钥","Update endpoint and signing key"):tr("配置 HTTPS 通知","Configure HTTPS notifications")}</button>}
 {merchant && <label>{tr("筛选订单结果","Filter order outcome")} <select value={outcome} onChange={e=>{setBefore("");setOutcome(e.target.value);}}>
  <option value="">{tr("全部","All")}</option><option value="succeeded">{tr("成功","Succeeded")}</option><option value="failed">{tr("失败","Failed")}</option>
 </select></label>}
 <WorkspaceTable label={tr("投递记录","Deliveries")} columns={[tr("事件 / 订单","Event / order"),tr("交易状态","Order status"),tr("投递状态","Delivery"),tr("尝试次数","Attempts"),tr("最近结果","Last result"),tr("操作","Action")]}>
 {s.events.map(r=><tr key={r.id}><td><button type="button" className="text-button" onClick={()=>setDetails(r)}>{r.id}</button><div>{openOrder ? <button type="button" className="text-button wrap" onClick={() => openOrder(r.order_id)}>{r.order_id}</button> : r.order_id}</div></td><td>{r.status}</td><td>{r.state}</td><td>{r.attempts}</td><td>{r.last_result??"—"}</td><td>{write&&s.endpoint&&(["dead","unconfigured"].includes(r.state)||Boolean(merchant)&&r.state==="delivered")&&<button className="secondary" onClick={()=>start(r)}>{tr("重发商户通知","Redeliver webhook")}</button>}</td></tr>)}</WorkspaceTable>
 {!s.events.length&&<p className="muted">{outcome?tr("没有符合条件的通知","No matching notifications"):tr("暂无终态交易通知","No terminal transaction notifications")}</p>}
 {s.events.length===50&&<button onClick={()=>setBefore(s.events[49].id)}>{tr("更早记录","Older events")}</button>}{before&&<button onClick={()=>setBefore("")}>{tr("最新记录","Latest")}</button>}
 </>}</LoadState>
 {details&&<Dialog drawer title={tr("通知尝试记录","Notification attempts")} close={()=>setDetails(undefined)}><p>{details.id}</p><LoadState state={attempts} label={tr("通知尝试记录","Notification attempts")}>{rows=><>{rows.length?<WorkspaceTable label={details.id} columns={[tr("尝试","Attempt"),tr("结果","Result"),tr("时间","Time")]}>{rows.map(r=><tr key={r.finished_at}><td>{r.attempt}</td><td>{r.result}</td><td>{new Date(r.finished_at).toLocaleString(locale)}</td></tr>)}</WorkspaceTable>:<p>{tr("暂无已完成的投递尝试","No completed delivery attempts")}</p>}</>}</LoadState></Dialog>}
 {action&&<Dialog title={key?tr("保存通知签名密钥","Save webhook signing key"):tr("交易通知设置","Notification settings")} locked={busy} close={()=>{setAction(undefined);setKey("")}}>
 {key?<><p>{tr("请立即复制此密钥，用于验证平台发送的交易通知。","Copy this one-time key to verify transaction notifications.")}</p><textarea readOnly value={key}/></>:<form onSubmit={submit}>
 {typeof action==="string"?<><label>HTTPS URL<input type="url" required name="endpoint" defaultValue={data.data?.endpoint??""}/></label>{action==="rotate"&&<p>{tr("保存后将使用新密钥签名后续通知。","Saving replaces the key for subsequent deliveries.")}</p>}</>:<p>{tr("将原交易结果发往商户当前配置的通知地址，沿用同一事件编号","Redeliver the original transaction result to the configured endpoint with the same event ID")} · {action.status} · {action.id}</p>}
 {error&&<p role="alert">{error}</p>}<button className="primary" disabled={busy}>{tr("确认","Confirm")}</button></form>}</Dialog>}</section>;
}
