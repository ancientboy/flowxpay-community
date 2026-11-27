import {useState} from "react";
import {useLocale} from "./locale";
import {Dialog} from "./Dialog";
import {LoadState,useWorkspaceData,WorkspaceTable} from "./WorkspaceInventoryUI";

type Activity={cursor:string;kind:string;source_id:string;created_at:string;available_delta:string;payout_held_delta:string;settlement_held_delta:string;dispute_held_delta:string};
type Line={order_id:string;channel_id?:string;upstream_account?:string;statement_batch_id?:string;statement_entry_id?:string;upstream_receipt_reference?:string;allocated_minor:string};
const names:Record<string,[string,string]>={topup:["充值到账","Confirmed top-up"],refund_recovery:["退款冲正","Refund recovery"],collection:["代收净入账","Net collection"],payout_hold:["代付冻结","Payout hold"],payout_consume:["代付成功","Payout completed"],payout_release:["代付释放","Payout release"],settlement_hold:["结算冻结","Settlement hold"],settlement_release:["结算释放","Settlement release"],settlement_consume:["结算完成","Settlement completed"],refund:["退款","Refund"],dispute_hold:["争议冻结","Dispute hold"],dispute_loss:["拒付扣款","Chargeback"],dispute_release:["争议释放","Dispute release"]};
export function FundHistory({merchant,token,account,currency,scale,batch,close,openSource}:{merchant?:string;token?:string;account:string;currency:string;scale:number|null;batch?:string;close:()=>void;openSource?:(direction:"collection"|"payout",id:string)=>void}){
 const {locale,exactMoney,dateTime}=useLocale();const zh=locale==="zh-CN";
 const [pages,setPages]=useState([""]);const cursor=pages[pages.length-1];
 const query=new URLSearchParams({part:batch?"lines":"ledger",account,currency});
 if(merchant)query.set("merchant_id",merchant);if(batch)query.set("batch",batch);if(cursor)query.set(batch?"after":"before",cursor);
 const state=useWorkspaceData<(Activity|Line)[]>("/flowxpay/ops/api/funds?"+query,token ?? "");
 const title=batch?(zh?"结算来源订单":"Settlement allocations"):(zh?"资金流水":"Account activity");
 function sourceLink(id:string,direction?: "collection"|"payout") {
  return openSource && direction ? <button className="text-button wrap" type="button" onClick={()=>{ close(); openSource(direction,id); }}>{id}</button> : id;
 }
 const money=(s:string)=>scale===null?s+" "+currency:exactMoney(s,currency,scale);
 return <Dialog drawer close={close} title={title}><p>{account} · {currency}</p>
 <LoadState state={state} label={title}>{rows=><>
 <WorkspaceTable label={title} columns={batch?(zh?["平台订单",...(merchant?["通道 / 上游凭证"]:[]),"本次分摊"]:["Order",...(merchant?["Channel / upstream evidence"]:[]),"Allocated"]):(zh?["业务 / 时间","来源编号","可用变动","代付冻结变动","结算冻结变动","争议冻结变动"]:["Event / time","Reference","Available change","Payout hold change","Settlement hold change","Dispute hold change"])}>
 {rows.map(row=>"kind" in row?<tr key={row.cursor}><td>{names[row.kind]?.[zh?0:1]??row.kind}<br/>{dateTime(row.created_at)}</td><td>{sourceLink(row.source_id,row.kind==="collection"?"collection":row.kind.startsWith("payout_")?"payout":undefined)}</td>
 <td>{money(row.available_delta)}</td><td>{money(row.payout_held_delta)}</td><td>{money(row.settlement_held_delta)}</td><td>{money(row.dispute_held_delta)}</td></tr>:
 <tr key={row.order_id}><td>{sourceLink(row.order_id,"collection")}</td>{merchant && <td>{row.channel_id}{row.upstream_account ? " / " + row.upstream_account : ""}<br/>{row.statement_batch_id} / {row.statement_entry_id}<br/>{row.upstream_receipt_reference ?? "—"}</td>}<td>{money(row.allocated_minor)}</td></tr>)}</WorkspaceTable>
 {!rows.length&&<p>{zh?"暂无记录":"No entries"}</p>}
 {pages.length>1&&<button className="secondary" onClick={()=>setPages(pages.slice(0,-1))}>{zh?"上一页":"Previous"}</button>}
 {rows.length===50&&<button className="secondary" onClick={()=>{const last=rows[49];setPages([...pages,"kind" in last?last.cursor:last.order_id]);}}>{zh?"下一页":"Next"}</button>}
 </>}</LoadState></Dialog>;
}
