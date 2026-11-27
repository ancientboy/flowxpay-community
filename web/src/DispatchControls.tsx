import {useState} from "react";
import {useLocale} from "./locale";
import {ControlAction} from "./WorkspaceControls";
import {useWorkspaceData,LoadState,WorkspaceTable} from "./WorkspaceInventoryUI";
import {productionControl} from "./ProductionPlatformControls";

type Limit={scope:"merchant"|"channel";subject:string;direction:string;currency:string;daily_minor:string|null;used_minor:string;remaining_minor:string;rps:number};
type Liquidity={channel_id:string;currency:string;amount_minor:string|null;reserved_minor:string;available_minor:string|null;observed_at?:string;stale:boolean;missing:boolean;low:boolean|null};
type DispatchReport={alerts:{unknown_payouts:number;dead_notifications:number;open_differences:number};limits:Limit[];liquidity:Liquidity[];queue:{queued:number;unknown:number;oldest_seconds:number|null;checkout_p95_ms:number|null};exceptions:{order_id:string;merchant_id:string;reference:string;lookup_result:string;lookup_attempts:number;next_lookup_at?:string}[]};

export function DispatchControls({operator,token,openOrder}:{operator:boolean;token:string;openOrder:(id:string)=>void}){
 const {locale,dateTime,exactMoney}=useLocale();const zh=locale==="zh-CN";const l=(cn:string,en:string)=>zh?cn:en;
 const query=useWorkspaceData<DispatchReport>("/flowxpay/ops/api/dispatch-controls",token);
 const [edit,setEdit]=useState<Limit>();const title=l("派单与通道余额","Dispatch and upstream balances");
 const money=(amount:string|null,c:string)=>amount==null?"—":exactMoney(String(amount),c,true);
 return <section className="workspace-panel"><div className="heading panel-heading"><h2>{title}</h2><button className="secondary" onClick={query.refresh}>{l("刷新","Refresh")}</button></div>
 <LoadState state={query} label={title}>{d=><>
 {(d.alerts.unknown_payouts+d.alerts.dead_notifications+d.alerts.open_differences)>0&&<p role="status">{l("需要跟进","Needs attention")} · {l("代付待查超过 15 分钟","Payouts unconfirmed over 15 min")} {d.alerts.unknown_payouts} · {l("通知重试耗尽","Exhausted notifications")} {d.alerts.dead_notifications} · {l("未处理账单差异","Open statement differences")} {d.alerts.open_differences}</p>}
 <dl className="platform-card-meta">{[[l("等待派单","Queued orders"),d.queue.queued],[l("待查证","Unconfirmed orders"),d.queue.unknown],[l("最早排队时间（秒）","Oldest queue age (s)"),d.queue.oldest_seconds??"—"],[l("收银台就绪 P95（毫秒）","Checkout ready P95 (ms)"),d.queue.checkout_p95_ms??"—"]].map(([label,value])=><div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
 <WorkspaceTable label={title} columns={[l("上游账户","Upstream account"),l("余额快照","Reported balance"),l("代付占用","Pending payout reserves"),l("可用估算","Estimated available"),l("更新时间","Last observed")]}>
 {d.liquidity.map(x=><tr key={x.channel_id+x.currency}><td>{x.channel_id} · {x.currency}</td><td>{money(x.amount_minor,x.currency)}</td><td>{money(x.reserved_minor,x.currency)}</td><td>{x.stale?"—":money(x.available_minor,x.currency)} {x.low&&!x.stale&&<strong>{l("余额不足以支付一笔最低金额代付","Below minimum payout including fees")}</strong>}</td><td>{x.observed_at?dateTime(x.observed_at):"—"} {x.missing?l("暂无快照","No snapshot"):x.stale&&l("已过期","Stale")}</td></tr>)}</WorkspaceTable>
 <h3>{l("派单限额","Dispatch limits")}</h3><p>{l("按已受理的订单本金累计，每日 UTC 00:00（曼谷 07:00）重置。商户和通道限额同时生效。","Accepted order principal counts toward both limits. The day resets at 00:00 UTC (07:00 Bangkok).")}</p><WorkspaceTable label={l("限额","Limits")} columns={[l("作用范围","Scope"),l("方向","Direction"),l("每日累计额度","Daily limit"),l("今日已用 / 剩余","Used / remaining today"),l("每秒最多笔数","Maximum per second"),l("操作","Action")]}>
 {d.limits.map(x=><tr key={[x.scope,x.subject,x.direction,x.currency].join("/")}><td>{x.scope==="merchant"?l("商户","Merchant"):l("通道","Channel")} · {x.subject}</td><td>{x.direction==="payout"?l("代付","Payout"):l("代收","Collection")} · {x.currency}</td><td>{x.daily_minor?money(x.daily_minor,x.currency):"—"}</td><td>{money(x.used_minor,x.currency)} / {x.daily_minor?money(x.remaining_minor,x.currency):"—"}</td><td>{x.rps||"—"}</td><td>{operator&&<button className="text-button" onClick={()=>setEdit(x)}>{l("设置限额","Edit limits")}</button>}</td></tr>)}</WorkspaceTable>
 <h3>{l("待查证订单","Orders awaiting verification")}</h3>
 <form className="platform-finance-filters" onSubmit={event => {
   event.preventDefault();
   openOrder(String(new FormData(event.currentTarget).get("order_id")).trim());
 }}>
   <label>{l("平台单号","Platform order ID")} <input id="order-evidence-id" name="order_id" required maxLength={80} pattern="[a-zA-Z0-9._:\-]{1,80}" /></label>
   <button className="secondary">{l("查证订单","Find order evidence")}</button>
 </form>{d.exceptions.length?<WorkspaceTable label={l("查单","Status lookup")} columns={[l("订单 / 商户","Order / merchant"),l("原上游单号","Original reference"),l("查单结果","Last result"),l("查询次数","Attempts"),l("下次查证","Next lookup")]}>
 {d.exceptions.map(x=><tr key={x.order_id}><td><button type="button" className="text-button" onClick={() => openOrder(x.order_id)}>{x.order_id}</button><div>{x.merchant_id}</div></td><td>{x.reference}</td><td>{x.lookup_result||"—"}</td><td>{x.lookup_attempts}</td><td>{x.next_lookup_at?dateTime(x.next_lookup_at):"—"}</td></tr>)}</WorkspaceTable>:<p>{l("暂无待查证订单","No pending exceptions")}</p>}
 </>}</LoadState>
 {edit&&<ControlAction title={l("设置派单限额","Set dispatch limits")} submitLabel={l("保存","Save")} close={()=>setEdit(undefined)} refresh={query.refresh} action={async f=>{await productionControl("/dispatch-controls",{scope:edit.scope,subject:edit.subject,direction:edit.direction,currency:edit.currency,daily_minor:String(f.get("daily")).trim(),rps:Number(f.get("rps"))})}}>
 <p>{edit.subject} · {edit.currency} · {edit.direction==="collection"?l("代收","Collection"):l("代付","Payout")}</p>
 <label>{l("每日累计金额（最小货币单位，留空表示不限）","Daily limit (minor units, blank for no cap)")}<input name="daily" inputMode="numeric" pattern="[0-9]+" defaultValue={edit.daily_minor??""}/></label>
 <label>{l("每秒最多笔数（0 表示不限）","Maximum per second (0 for no cap)")}<input name="rps" type="number" min="0" required defaultValue={edit.rps}/></label>
 </ControlAction>}</section>
}

type OrderRisk = {reviewed:boolean; conflicts:{order_id:string;direction:"collection"|"payout";observed_state:string;callback_state:string;first_seen_at:string}[]; currencies:{currency:string;pending_minor:string;oldest_minutes:number|null;unreconciled_minor:string}[]};

export function OrderRiskOverview({token,openPending,openConflict,openReconciliation}:{
 token:string;openPending:(currency:string)=>void;
 openConflict:(direction:"collection"|"payout",reference:string)=>void;openReconciliation:()=>void
}){
 const {locale,exactMoney}=useLocale();
 const l=(cn:string,en:string)=>locale==="zh-CN"?cn:en;
 const title=l("全平台订单资金风险","Platform-wide order exposure");
 const query=useWorkspaceData<OrderRisk>("/flowxpay/ops/api/dispatch-controls?view=order-risk",token);
 return <section className="workspace-panel"><div className="heading panel-heading"><h2>{title}</h2><button className="secondary" onClick={query.refresh}>{l("刷新","Refresh")}</button></div>
 <LoadState state={query} label={title}>{d=><>
 {!d.reviewed&&<p role="alert">{l("数据库恢复待核对，新订单和上游派单已暂停。","Database recovery requires reconciliation. New orders and dispatch are paused.")}</p>}
 {d.conflicts.length>0&&<details><summary>{l("最近的终态回调冲突（最多 10 笔）","Recent terminal callback conflicts (up to 10)")}</summary>
 {d.conflicts.map(r=><p key={r.order_id}><button className="text-button" onClick={()=>openConflict(r.direction,r.order_id)}>{r.order_id}</button> · {l("平台","Platform")} {r.observed_state} · {l("上游","Upstream")} {r.callback_state}</p>)}</details>}
 {d.currencies.length?<WorkspaceTable label={title} columns={[l("币种","Currency"),l("待确认代付本金","Unconfirmed payout principal"),l("最长等待（分钟）","Oldest wait (minutes)"),l("到期未对平代收本金","Due unreconciled collection principal")]}>
 {d.currencies.map(r=><tr key={r.currency}><td>{r.currency}</td><td>{r.pending_minor !== "0"
   ? <button className="text-button" onClick={()=>openPending(r.currency)}>{exactMoney(r.pending_minor,r.currency,true)}</button>
   : exactMoney(r.pending_minor,r.currency,true)}</td><td>{r.oldest_minutes??"—"}</td><td>{r.unreconciled_minor !== "0"
   ? <button className="text-button" onClick={openReconciliation}>{exactMoney(r.unreconciled_minor,r.currency,true)}</button>
   : exactMoney(r.unreconciled_minor,r.currency,true)}</td></tr>)}</WorkspaceTable>:<p>{l("暂无待确认代付或到期未对平的代收。","No unconfirmed payouts or due unreconciled collections.")}</p>}
 </>}</LoadState></section>
}
