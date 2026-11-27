import { useRef, useState } from "react";
import { ApiError, request } from "./api";
import { useLocale } from "./locale";
import { platformAPI } from "./platformEnvironment";

export type FollowUp = { label: string; mine: boolean; claimed_at: string; active: boolean };

export function OrderFollowUp({ orderId, owner, operator, instructions, token, refresh }: {
  orderId: string; owner: FollowUp | null; operator: boolean; instructions: string[];
  token: string; refresh: () => void;
}) {
  const { locale, dateTime } = useLocale();
  const l = (zh: string, en: string) => locale === "zh-CN" ? zh : en;
  const [busy, setBusy] = useState(false);
  const [warning, setWarning] = useState("");
  const pending = useRef(false);
  async function change() {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setWarning("");
    try {
      await request<void>(platformAPI + "/order-evidence", token, { method: "POST",
        body: JSON.stringify({ order_id: orderId, action: owner?.mine ? "release" : "claim" }) });
      refresh();
    } catch (error) {
      setWarning(error instanceof ApiError && error.status === 409
        ? l("跟进人已变化，请先刷新证据。", "The assignee changed. Refresh the evidence first.")
        : l("跟进状态未确认，请先刷新证据。", "Follow-up status is unconfirmed. Refresh the evidence first."));
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }

  return <section className="order-follow-up" aria-label={l("订单下一步", "Next steps")}>
    <h3>{l("订单下一步", "Next steps")}</h3>
    {instructions.length ? <ul>{instructions.map(item => <li key={item}>{item}</li>)}</ul>
      : <p>{l("当前证据未发现需要跟进的事项。", "Current evidence shows no follow-up items.")}</p>}
    <p className="wrap">{l("跟进人", "Assignee")} · {owner
      ? owner.label + " · " + dateTime(owner.claimed_at) + (owner.active ? "" : l("（账号已停用）", " (inactive)")
      )
      : l("未领取", "Unassigned")}</p>
    {operator && ((owner?.mine || owner?.active === false) || (!owner && instructions.length > 0)) && <button type="button" className="secondary"
      disabled={busy || Boolean(warning)} onClick={() => void change()}>
      {owner?.mine ? l("释放跟进", "Release follow-up") : l("领取跟进", "Claim follow-up")}
    </button>}
    {warning && <p role="alert">{warning}</p>}
  </section>;
}
