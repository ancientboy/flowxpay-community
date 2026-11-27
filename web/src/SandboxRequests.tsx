import {useWorkspaceData,LoadState} from "./WorkspaceInventoryUI";
import {SettlementRequests} from "./SettlementRequests";
import {useLocale} from "./locale";
type Account={id:string;currency:string;scale:number;settle_available_minor:string;settlement_held_minor:string;refund_pending:boolean};
export function SandboxRequests({token,memberWrite}:{token:string;memberWrite:boolean}){
 const {t}=useLocale();
 const state=useWorkspaceData<Account[]>("/sandbox/workspace/settlement-requests?part=accounts",token);
 return <LoadState state={state} label={t("Settlement")}>{accounts=><SettlementRequests sandbox sandboxMember memberWrite={memberWrite} token={token} accounts={accounts} refresh={state.refresh}/>}</LoadState>;
}
