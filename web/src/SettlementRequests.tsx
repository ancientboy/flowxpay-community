import {useState} from "react";
import {ApiError,request} from "./api";
import {useLocale} from "./locale";
import {ControlAction} from "./WorkspaceControls";
import {Details,LoadState,useWorkspaceData,WorkspaceTable} from "./WorkspaceInventoryUI";

const productionEndpoint="/flowxpay/ops/api/settlement-requests";
type Account={id:string;currency:string;scale:number|null;settle_available_minor:string;not_due_minor?:string;settlement_held_minor?:string;refund_pending:boolean};
type Application={id:string;merchant_id:string;account_id:string;currency:string;source_minor:string;source_scale:number;requested_currency:string;
 network:string;recipient:string;phase:string;created_at:string;rate?:string;amount?:string;cancel_reason?:string;transfer_reference?:string};
const phases:Record<string,[string,string]>={requested:["待运营核对","Awaiting review"],quoted:["待商户确认报价","Awaiting merchant approval"],approved:["待付款","Ready for payment"],paying:["付款待确认","Payment in progress"],confirmed:["已结算","Settled"],cancelled:["已驳回","Rejected"]};

function decimal(minor:string,scale:number){
 const s=minor.padStart(scale+1,"0");return scale?s.slice(0,-scale)+"."+s.slice(-scale):s;
}
export function SettlementRequests({merchant,memberWrite=false,memberView=false,operator=false,accounts=[],refresh,token="", sandbox=false, sandboxMember=false,pending=false}:{
 merchant?:string;memberWrite?:boolean;memberView?:boolean;operator?:boolean;accounts?:Account[];refresh:()=>void;token?:string;sandbox?:boolean;sandboxMember?:boolean;pending?:boolean
}){
 const endpoint=sandbox?(sandboxMember?"/sandbox/workspace/settlement-requests":"/sandbox/ops/settlement-requests"):productionEndpoint;
 const post=(url:string,body:object)=>request(url,token,{method:"POST",body:JSON.stringify(body)});
 const {locale,exactMoney,dateTime}=useLocale();const zh=locale==="zh-CN";const l=(a:string,b:string)=>zh?a:b;
 const [page,setPage]=useState(0);
 const state=useWorkspaceData<Application[]>(endpoint+"?"+new URLSearchParams({page:String(page),...(merchant?{merchant_id:merchant}:{}),...(!sandbox && pending ? {pending:"1"} : {})}),token);
 const [form,setForm]=useState<{op:string;row?:Application;key:string}>();
 const [idx,setIdx]=useState(0);const [entered,setEntered]=useState("");
 const account=accounts[idx];const row=form?.row;
 const open=(op:string,row?:Application)=>{setIdx(0);setEntered("");setForm({op,row,key:crypto.randomUUID()})};
 const labels:Record<string,string>={submit:l("申请结算","Request settlement"),quote:l("给出结算报价","Quote settlement"),approve:l("接受此报价","Accept this quote"),paying:l("开始线下付款","Start offline payment"),confirm:l("登记付款凭证","Record transfer"),cancel:l("驳回并释放资金","Reject and release funds"),detail:l("申请详情","Request details")};
 const nextSteps: Record<string, string> = {
  requested:l("平台核对后报价","Platform to review and quote"),
  quoted:l("商户确认汇率与到账金额","Merchant to accept the quote"),
  approved:l("平台安排付款","Platform to arrange transfer"),
  paying:l("平台确认付款结果","Platform to confirm transfer result"),
  confirmed:l("可查付款凭证","Transfer details available"),
  cancelled:l("查看驳回原因","Check the rejection reason"),
 };
 function settlementAmount(item:Application) {
  if (!item.amount) return l("待报价","Awaiting quote");
  if (!memberView) return item.amount+" "+item.requested_currency;
  return <>{item.amount} {item.requested_currency}<span className="channel-subtitle muted small">{
    item.phase==="confirmed" ? l("已确认付款","Confirmed transfer")
      : item.phase==="cancelled" ? l("原报价 · 已取消","Previous quote · Cancelled")
      : ["approved","paying"].includes(item.phase) ? l("商户已确认 · 待结算","Accepted quote · Pending transfer")
      : l("报价 · 待商户确认","Quote · Awaiting approval")
  }</span></>;
 }
 const reload=()=>{state.refresh();refresh()};
 return <section className="workspace-panel"><div className="heading panel-heading"><h2>{l("商户结算申请","Merchant settlement requests")}</h2>
 {memberWrite&&<button className="primary" onClick={()=>open("submit")}>{labels.submit}</button>}</div>
 <LoadState state={state} label={l("结算申请","Settlement requests")}>{rows=><>
 {rows.length?<WorkspaceTable label={l("结算申请","Requests")} columns={[l("商户 / 账户","Merchant / account"),l("申请金额","Requested"),l("结算金额","Settlement amount"),l("状态","Status"),l("操作","Action")]}>
 {rows.map(r=><tr key={r.id}><td>{memberView ? r.account_id+" · "+r.currency : r.merchant_id+" · "+r.account_id}</td><td>{exactMoney(r.source_minor,r.currency,r.source_scale)}</td><td>{settlementAmount(r)}</td><td>{phases[r.phase]?.[zh?0:1]??r.phase}{memberView && <span className="channel-subtitle muted small">{nextSteps[r.phase]}</span>}</td><td><button className="text-button" onClick={()=>open("detail",r)}>{labels.detail}</button>
 {operator&&["requested","quoted"].includes(r.phase)&&<button className="secondary" onClick={()=>open("quote",r)}>{labels.quote}</button>}
 {operator&&r.phase==="approved"&&<button className="secondary" onClick={()=>open("paying",r)}>{labels.paying}</button>}
 {operator&&r.phase==="paying"&&<button className="secondary" onClick={()=>open("confirm",r)}>{labels.confirm}</button>}
 {operator&&["requested","quoted","approved"].includes(r.phase)&&<button className="text-button" onClick={()=>open("cancel",r)}>{labels.cancel}</button>}
 {memberWrite&&r.phase==="quoted"&&<button className="primary" onClick={()=>open("approve",r)}>{labels.approve}</button>}</td></tr>)}</WorkspaceTable>:<p className="empty muted">{l("暂无结算申请","No settlement requests")}</p>}
 {page>0&&<button onClick={()=>setPage(page-1)}>{l("上一页","Previous")}</button>}{rows.length===100&&<button onClick={()=>setPage(page+1)}>{l("下一页","Next")}</button>}</>}</LoadState>
 {form&&<ControlAction title={labels[form.op]} submitLabel={form.op==="detail"?l("关闭","Close"):labels[form.op]} refresh={reload} close={()=>setForm(undefined)}
 unavailable={form.op==="submit"&&(!account||account.scale===null||account.settle_available_minor==="0")}
 action={async f=>{
  const field=(n:string)=>String(f.get(n)??"").trim();
  if(form.op==="detail")return;
  if(form.op==="submit"){
   if(!account||account.scale===null)return;
   const [i,frac=""]=entered.split(".");
   if(!/^\d+(\.\d+)?$/.test(entered)||frac.length>account.scale)throw new ApiError(400,l("金额精度无效","Invalid amount precision"));
   const minor=BigInt(i+frac.padEnd(account.scale,"0"));
   if(minor<=0n||minor>BigInt(account.settle_available_minor))throw new ApiError(409,l("超过可结算金额","Insufficient eligible balance"));
   await post(endpoint,{action:"submit",request_id:form.key,account_id:account.id,currency:account.currency,source_minor:String(minor),destination_currency:field("dest"),network:field("rail"),recipient:field("payee")});return;
  }
  if(!row)return;
  const base={merchant_id:row.merchant_id,batch_id:row.id,action:form.op};
  if(form.op==="cancel")await post(sandbox?endpoint:"/flowxpay/ops/api/funds",{...base,reason:field("reason")});
  else if(form.op==="confirm")await post(sandbox?endpoint:"/flowxpay/ops/api/funds",{...base,paid_currency:row.requested_currency,paid_amount:field("amount"),transfer_reference:field("ref"),paid_on:field("day")});
  else await post(endpoint,{...base,...(form.op==="quote"?{fx_rate:field("rate")}:form.op==="approve"?{fx_rate:row.rate,expected_amount:row.amount}:{})});
 }}>
 {form.op==="submit"?<>
 <label>{l("原币资金账户","Source account")}<select value={idx} onChange={e=>{setIdx(Number(e.target.value));setEntered("")}}>{accounts.map((a,i)=><option key={a.id+a.currency} value={i}>{a.id} · {a.currency}</option>)}</select></label>
 <p>{l("当前可结算","Eligible now")} {account&&account.scale!==null?exactMoney(account.settle_available_minor,account.currency,account.scale):"—"} · {l("仅含到期且已对账的资金","Only matured, reconciled funds")}</p>
 {account&&account.scale!==null&&<p>{l("未到期代收净额","Unmatured net collections")} {exactMoney(account.not_due_minor??"0",account.currency,account.scale)} · {l("结算冻结","Reserved for settlement")} {exactMoney(account.settlement_held_minor??"0",account.currency,account.scale)}</p>}
 {account?.refund_pending&&<p>{l("退款待核对","Refund reconciliation pending")}</p>}
 <label>{l("申请原币金额","Amount in source currency")}<input required inputMode="decimal" value={entered} onChange={e=>setEntered(e.target.value)}/></label>
 {account&&account.scale!==null&&<button type="button" className="text-button" onClick={()=>setEntered(decimal(account.settle_available_minor,account.scale!))}>{l("全部结算","Use full amount")}</button>}
 <label>{l("结算币种","Settlement currency")}<select name="dest">{[...new Set(["USDT",account?.currency].filter(Boolean))].map(c=><option key={c}>{c}</option>)}</select></label>
 <label>{l("收款网络或银行","Receiving network or bank")}<input name="rail" required maxLength={48}/></label>
 <label>{l("收款地址或账户","Receiving address or account")}<input name="payee" required minLength={3} maxLength={240}/></label>
 <p>{l("提交后冻结原币金额，汇率与到账金额由你确认后进入付款。","Submission reserves the source amount. You approve the quote before payment.")}</p>
 </>:row&&<>
 <Details fields={[[l("申请编号","Request ID"),row.id],[l("申请时间","Requested at"),dateTime(row.created_at)],[l("来源","Source"),exactMoney(row.source_minor,row.currency,row.source_scale)],[l("收款网络或银行","Receiving network or bank"),row.network],[l("收款账户","Recipient"),row.recipient],[l("结算金额","Settlement amount"),settlementAmount(row)],[l("汇率","Rate"),row.rate?"1 "+row.currency+" = "+row.rate+" "+row.requested_currency:"—"],[l("驳回原因","Rejection reason"),row.cancel_reason??"—"],[l("付款流水","Payment reference"),row.transfer_reference??"—"]]}/>
 {form.op==="quote"&&<label>1 {row.currency} = {l("多少","rate in")} {row.requested_currency}<input name="rate" inputMode="decimal" required defaultValue={row.currency===row.requested_currency?"1":row.rate}/></label>}
 {form.op==="cancel"&&<label>{l("驳回原因","Reason")}<input name="reason" required maxLength={300}/></label>}
 {form.op==="paying"&&<p>{l("请核对收款账户。线下付款结果未确定时保持冻结。","Verify the recipient. Keep funds reserved until the transfer is confirmed.")}</p>}
 {form.op==="confirm"&&<><label>{l("实际付款金额","Actual amount")} ({row.requested_currency})<input name="amount" inputMode="decimal" required/></label><label>{l("付款流水或交易哈希","Transfer reference or transaction hash")}<input required name="ref"/></label><label>{l("付款日期","Paid on")}<input type="date" required name="day"/></label></>}
 </>}
 </ControlAction>}</section>
}
