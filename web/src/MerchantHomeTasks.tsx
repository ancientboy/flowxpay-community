import type { ReactNode } from "react";
import { useLocale } from "./locale";
import { base } from "./WorkspaceAccess";
import { LoadState, useWorkspaceData } from "./WorkspaceInventoryUI";

export function MerchantHomeTasks({ token, canApprove, openPayouts, openSettlement, openIntegration, openReports, children }: {
  children: ReactNode; token: string; canApprove: boolean; openPayouts: () => void; openSettlement: () => void;
  openIntegration: () => void; openReports: () => void;
}) {
  const { locale } = useLocale();
  const l = (zh: string, en: string) => locale === "zh-CN" ? zh : en;
  const title = l("待处理事项", "Tasks needing attention");
  const state = useWorkspaceData<{ pending_payouts: number; quotes_to_review: number; refund_reviews: number }>(base + "/tasks", token);

  const hasTasks = state.data && (state.data.pending_payouts > 0 || state.data.quotes_to_review > 0 || state.data.refund_reviews > 0);
  const panel = <section className="workspace-panel merchant-home-tasks">
    <h2>{title}</h2>
    <LoadState state={state} label={title}>{data => {
      const tasks = [
        { count: data.quotes_to_review, title: canApprove ? l("核对结算报价", "Review settlement quotes") : l("查看待确认的结算报价", "View settlement quotes awaiting approval"), action: openSettlement },
        { count: data.pending_payouts, title: l("查看待确认代付", "View unconfirmed payouts"), action: openPayouts },
        { count: data.refund_reviews, title: l("查看退款待核对的资金账户", "View accounts with refunds under review"), action: openSettlement },
      ].filter(task => task.count > 0);
      return tasks.length ? <ul>{tasks.map(task => <li key={task.title}>
        <button type="button" className="text-button" onClick={task.action}>{task.title} · {task.count}</button>
      </li>)}</ul> : <p className="muted">{l("当前没有待确认代付、结算报价或退款核对事项。", "No unconfirmed payouts, settlement quotes or refund reviews at the moment.")}</p>;
    }}</LoadState>
    <div className="header-actions">
      <button type="button" className="secondary" onClick={openIntegration}>{l("查看接入步骤", "View integration steps")}</button>
      <button type="button" className="secondary" onClick={openReports}>{l("查看经营报表", "View business reports")}</button>
    </div>
  </section>;
  return <>{hasTasks && panel}{children}{!hasTasks && panel}</>;
}
