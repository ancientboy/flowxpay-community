import {useEffect,useRef,useState} from "react";
import {request} from "./api";
import {StatementImportForm} from "./StatementImportForm";
import {useLocale} from "./locale";
import {ControlAction} from "./WorkspaceControls";
import {LoadState,useWorkspaceData,WorkspaceTable} from "./WorkspaceInventoryUI";

type Route={channel_id:string;channel_name:string;currency:string};
type Batch={id:string;batch_id:string;day:string;channel_id:string;currency:string;open:number;rows:number;digest:string};
type Diff={line:number;entry:string;order_id:string;reference:string;issue:string;resolved_at?:string;reason?:string};
const reasons:Record<string,string>={fee_mismatch:"手续费不一致",upstream_only:"仅上游有记录",platform_only:"仅平台有记录",state_mismatch:"订单状态或方向不一致",reference_mismatch:"上游流水不一致",amount_mismatch:"金额不一致",statement_conflict:"既有账单冲突"};
export function StatementImports({merchant,routes,operator,refresh,focusDifferences,openOrder}:{merchant:string;routes:Route[];operator:boolean;refresh:()=>void;focusDifferences?:boolean;openOrder?:(id:string)=>void}){
 const {locale}=useLocale();const zh=locale==="zh-CN";const l=(a:string,b:string)=>zh?a:b;
 const heading=useRef<HTMLHeadingElement>(null);
 const [openOnly,setOpenOnly]=useState(Boolean(focusDifferences));
 useEffect(()=>{if(focusDifferences){setOpenOnly(true);heading.current?.focus()}},[focusDifferences]);
 const [add,setAdd]=useState(false);const [selected,setSelected]=useState<Batch>();const [review,setReview]=useState<Diff>();const [page,setPage]=useState(0);const [batchPage,setBatchPage]=useState(0);
 const url="/flowxpay/ops/api/statement-imports?"+new URLSearchParams({merchant_id:merchant});
 const batches=useWorkspaceData<Batch[]>(url+"&page="+batchPage+(openOnly?"&open=1":""),"");const lines=useWorkspaceData<Diff[]>(selected?url+"&part=differences&id="+selected.id+"&page="+page:undefined,"");
 const scopes=Array.from(new Map(routes.map(r=>[r.channel_id+"/"+r.currency,r])).values());
 const refreshAll=()=>{batches.refresh();if(selected)lines.refresh();refresh()};
 return <section className="workspace-panel"><div className="heading panel-heading"><h2 id="upstream-statement-heading" ref={heading} tabIndex={-1}>{l("上游批量对账","Upstream statement imports")}</h2>
 {operator&&<button className="secondary" disabled={!scopes.length} onClick={()=>setAdd(true)}>{l("导入 CSV","Import CSV")}</button>}</div>
 <label className="platform-checkbox"><input type="checkbox" checked={openOnly} onChange={event=>{setOpenOnly(event.target.checked);setBatchPage(0);setSelected(undefined)}} />
 {l("仅显示有未处理差异的批次","Only show batches with open differences")}</label>
 <LoadState state={batches} label="CSV">{rows=><>{!rows.length && <p className="muted empty">{l("没有符合条件的对账批次","No statement batches match this filter")}</p>}<WorkspaceTable label="CSV" columns={[l("批号","Batch"),l("账单日","Date"),l("通道","Channel"),l("记录","Rows"),l("未处理差异","Open differences")]}>
 {rows.map(b=><tr key={b.id}><td><button className="text-button" onClick={()=>{setSelected(b);setPage(0)}}>{b.batch_id}</button></td><td>{b.day}</td><td>{b.channel_id} · {b.currency}</td><td>{b.rows}</td><td>{b.open}</td></tr>)}</WorkspaceTable>{batchPage>0&&<button onClick={()=>setBatchPage(batchPage-1)}>{l("上一页","Previous")}</button>}{rows.length===100&&<button onClick={()=>setBatchPage(batchPage+1)}>{l("下一页","Next")}</button>}</>}</LoadState>
 {selected&&<LoadState state={lines} label={selected.batch_id}>{rows=><><h3>{selected.batch_id}</h3><WorkspaceTable label={selected.batch_id} columns={[l("行 / 流水","Line / reference"),l("订单","Order"),l("差异","Difference"),l("处置","Review")]}>
 {rows.map(r=><tr key={r.line}><td>{r.line>0?r.line:"—"} · {r.reference??"—"}</td><td>{r.order_id && openOrder ? <button type="button" className="text-button wrap" onClick={() => openOrder(r.order_id)}>{r.order_id}</button> : r.order_id ?? "—"}</td><td>{zh?reasons[r.issue]:r.issue.replaceAll("_"," ")}</td><td>{r.resolved_at?r.reason:operator?<button onClick={()=>setReview(r)}>{l("核对凭证","Reconcile")}</button>:"—"}</td></tr>)}</WorkspaceTable>{!rows.length&&<p>{l("未发现差异","No differences")}</p>}{page>0&&<button onClick={()=>setPage(page-1)}>{l("上一页","Previous")}</button>}{rows.length===100&&<button onClick={()=>setPage(page+1)}>{l("下一页","Next")}</button>}</>}</LoadState>}
 {review&&selected&&<ControlAction title={l("核对差异","Reconcile difference")} close={()=>setReview(undefined)} refresh={refreshAll} submitLabel={l("核对并记录","Reconcile")} action={async f=>{
 await request(url+"&action=resolve","",{method:"POST",body:JSON.stringify({id:selected.id,line:review.line,order_id:f.get("order"),reason:f.get("reason")})})}}>
 <p>{l("金额或状态不符需先导入已匹配的更正账单。确认实际费差时需填写凭证依据。","Import matching corrected evidence for amount or state differences. Record the evidence for any accepted fee variance.")}</p>
 <label>{l("对应订单号","Order ID")}<input required name="order" defaultValue={review.order_id}/></label><label>{l("核对依据","Evidence and reason")}<input name="reason" minLength={3} maxLength={300} required/></label></ControlAction>}
 {add && <StatementImportForm url={url} scopes={scopes} close={() => setAdd(false)} refresh={refreshAll} />}</section>
}
