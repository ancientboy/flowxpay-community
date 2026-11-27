import { memberPortal } from "./memberEnvironment";
import { ReportSourceStatus } from "./ReportSourceStatus";
import { useLocale } from "./locale";
import { useState } from "react";

import { Dialog } from "./Dialog";
import { base } from "./WorkspaceAccess";
import { Details, LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";

interface Chargeback {
  id: string;
  account_id: string;
  payment_id: string;
  merchant_reference: string;
  status: "opened" | "won" | "lost";
  currency: string;
  amount_minor: number;
  created_at: string;
  updated_at: string;
}

interface ChargebackDetail extends Chargeback {
  payment: { status: "succeeded"; amount_minor: number; paid_at: string | null; channel_reference: string };
  history: {
    event_id: string;
    status: Chargeback["status"];
    outcome: "applied" | "ignored" | "terminal_conflict";
    received_at: string;
    replay_conflict_at: string | null;
  }[];
}

const statuses = { opened: "Open", won: "Won", lost: "Lost" };
const outcomes = { applied: "Applied", ignored: "Ignored", terminal_conflict: "Conflicting final status" };
const path = base + "/chargebacks";

function Status({ status }: { status: Chargeback["status"] }) {
  const { t } = useLocale();
  const color = { opened: "blue", won: "green", lost: "amber" };
  return <span className={"tag " + color[status]}>{t(statuses[status])}</span>;
}

export function Chargebacks({ token, available }: { token: string; available: boolean }) {
  const { t } = useLocale();
  return available ? <>
    {memberPortal && <ReportSourceStatus token={token} kind="dispute" />}
    <ChargebackRecords token={token} />
  </> : <section className="workspace-panel chargebacks-panel">
    <p className="muted panel-note">{t("Chargeback reporting is unavailable until a channel dispute feed is connected.")}</p>
  </section>;
}

function ChargebackRecords({ token }: { token: string }) {
  const { t, money, dateTime } = useLocale();
  const cases = useWorkspaceData<Chargeback[]>(path, token);
  const [selected, setSelected] = useState<Chargeback>();
  return <section className="workspace-panel chargebacks-panel" aria-label={t(memberPortal ? "Chargebacks" : "Sandbox chargebacks")}>
    <div className="heading panel-heading"><p className="muted">{t(memberPortal ? "Cases reported by channels for paid collections." : "Simulated channel records for paid collections.")}</p>
      <button className="secondary" disabled={!cases.data && !cases.error} onClick={cases.reload}>{t("Refresh chargebacks")}</button>
    </div>
    <LoadState state={cases} label={t("chargebacks")}>{items => items.length ?
      <WorkspaceTable label={t("Chargebacks")} columns={[t("Chargeback"), t("Status"), t("Original order"), t("Amount"), t("Updated")]}>
        {items.map(item => <tr key={item.account_id + "/" + item.id}>
          <td><button className="text-button wrap" onClick={() => setSelected(item)}>{item.id}</button>
            <span className="resource-id account-id muted">{item.account_id}</span></td>
          <td><Status status={item.status} /></td>
          <td className="wrap">{item.merchant_reference}</td>
          <td>{money(item.amount_minor, item.currency)}</td>
          <td>{dateTime(item.updated_at)}</td>
        </tr>)}
      </WorkspaceTable> : <p className="muted empty">{t(memberPortal ? "No cases have been received." : "No chargebacks recorded.")}</p>}</LoadState>
    {cases.data?.length === 100 && <p className="muted small">{t("Showing the latest 100 chargebacks.")}</p>}
    {selected && <ChargebackDetails token={token} selected={selected} close={() => setSelected(undefined)} />}
  </section>;
}

function ChargebackDetails({ token, selected, close }: { token: string; selected: Chargeback; close: () => void }) {
  const { t, money, dateTime } = useLocale();
  const detail = useWorkspaceData<ChargebackDetail>(path + "/" + encodeURIComponent(selected.account_id) +
    "/" + encodeURIComponent(selected.id), token);
  return <Dialog drawer title={t("Chargeback details")} close={close}>
    <LoadState state={detail} label={t("chargeback details")}>{item => <>
      <Details fields={[
        [t("Chargeback ID"), item.id], [t("Channel account"), item.account_id],
        [t("Disputed amount"), money(item.amount_minor, item.currency)], [t("Status"), <Status status={item.status} />],
      ]} />
      <h3>{t("Original order")}</h3>
      <Details fields={[
        [t("Merchant reference"), item.merchant_reference], [t("Payment ID"), item.payment_id],
        [t("Channel reference"), item.payment.channel_reference],
        [t("Payment status"), item.payment.status === "succeeded" ? t("Paid") : item.payment.status],
        [t("Payment amount"), money(item.payment.amount_minor, item.currency)], [t("Paid at"), item.payment.paid_at ? dateTime(item.payment.paid_at) : "—"],
      ]} />
      <h3>{t("Channel event history")}</h3>
      {item.history.length ? <ol className="chargeback-history" aria-label={t("Channel event history")}>
        {item.history.map(event => <li key={event.event_id}>
          <div className="chargeback-event-result">
            <Status status={event.status} />
            <strong>{t(outcomes[event.outcome])}</strong>
          </div>
          <div className="resource-id wrap">{event.event_id}</div>
          <time className="muted small" dateTime={event.received_at}>{dateTime(event.received_at)}</time>
          {event.replay_conflict_at && <p className="muted small">{t("Event ID conflict")} · {dateTime(event.replay_conflict_at)}</p>}
        </li>)}
      </ol> : <p className="muted">{t("No channel events have been received.")}</p>}
    </>}</LoadState>
  </Dialog>;
}
