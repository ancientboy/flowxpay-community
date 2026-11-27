import {useState} from "react";
import {useLocale} from "./locale";
import {productionControl} from "./ProductionPlatformControls";
import {ControlAction} from "./WorkspaceControls";
export type Cycle = {channel_id:string;currency:string;days:number;zone:string;holidays:string[]};
export function ChannelCycle({channel,currency,market,cycle,editable,refresh}:{channel:string;currency:string;market:string;cycle?:Cycle;editable:boolean;refresh:()=>void}){
 const {locale}=useLocale();const zh=locale==="zh-CN";const [open,setOpen]=useState(false);
 const name=zh?"代收结算周期":"Collection settlement cycle";
 const zone=cycle?.zone??({TH:"Asia/Bangkok",IN:"Asia/Kolkata",BR:"America/Sao_Paulo"}[market]??"UTC");
 return <div><p>{name} · {cycle?(cycle.days?"T+"+cycle.days:"D0")+" · "+cycle.zone:(zh?"沿用已有资金账户等待天数":"Existing account delay")}</p>
 {editable&&<button className="secondary" onClick={()=>setOpen(true)}>{zh?"设置结算周期":"Set settlement cycle"}</button>}
 {open&&<ControlAction title={name} submitLabel={zh?"保存":"Save"} close={()=>setOpen(false)} refresh={refresh} action={async f=>{
 await productionControl("/funds",{action:"cycle",channel_id:channel,currency,maturity_days:Number(f.get("days")),zone:String(f.get("zone")).trim(),holidays:String(f.get("holidays")).trim().split(/[\s,，]+/).filter(Boolean)});
 }}>
 <p>{channel} · {currency} · {zone}</p><label>{name}<select name="days" defaultValue={cycle?.days??""} required><option value="" disabled>{zh?"请选择":"Select"}</option>{[0,1,2,3,4,5].map(n=><option key={n} value={n}>{n?"T+"+n:"D0"}</option>)}</select></label>
 <p>{zh
   ?"例如周五代收成功，D0 当天到期；T+1 顺延至下一个营业日，通常是周一。到期后仍须完成对账。新周期从新订单起生效。"
   :"For a Friday collection, D0 matures that day and T+1 matures on the next business day, usually Monday. Funds still require reconciliation. Changes apply to new orders."}</p>
 <details className="workspace-fields">
  <summary>{zh?"时区和节假日":"Time zone and holidays"}</summary>
  <label htmlFor="channel-cycle-zone">{zh?"通道当地时区":"Channel time zone"}</label>
  <input id="channel-cycle-zone" name="zone" defaultValue={zone} required />
  <label htmlFor="channel-cycle-holidays">{zh?"额外非营业日（YYYY-MM-DD，每行一天）":"Additional holidays (YYYY-MM-DD, one per line)"}</label>
  <textarea id="channel-cycle-holidays" name="holidays" defaultValue={cycle?.holidays.join("\n")}/>
 </details>
 </ControlAction>}</div>;
}
