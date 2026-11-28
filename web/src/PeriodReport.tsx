import { useState, type FormEvent } from "react";
import { useLocale } from "./locale";
import { LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";

type Bucket = {
  period_start: string; period_end: string; currency: string; direction: "collection" | "payout"; scale: number | null;
  orders: number; requested_minor: string; succeeded: number; successful_minor: string;
  credited_minor: string | null; quoted_fee_minor: string | null; transferred_minor: string | null;
};
type Report = { from: string; to: string; group: string; channel: string; currency: string; merchant: string; rows: Bucket[]; channels: { id: string; name: string }[] };

function utcRange(days: number) {
  const end = new Date();
  const start = new Date(end);
  start.setUTCDate(start.getUTCDate() - days + 1);
  return { from: start.toISOString().slice(0, 10), to: end.toISOString().slice(0, 10) };
}

export function PeriodReport({ endpoint, token, merchant }: { endpoint: string; token: string; merchant?: string }) {
  const { locale, exactMoney, t } = useLocale();
  const l = (en: string, zh: string) => locale === "zh-CN" ? zh : en;
  const [filters, setFilters] = useState(() => ({ ...utcRange(7), group: "day", channel: "", currency: "" }));
  const [dates, setDates] = useState(() => utcRange(7));
  const [currency, setCurrency] = useState("");
  const [chosen, setChosen] = useState(7);
  const params = new URLSearchParams(filters);
  if (merchant) params.set("merchant", merchant);
  const state = useWorkspaceData<Report>(endpoint + "?" + params, token);
  const data = state.data;
  const currentReport = data && data.from === filters.from && data.to === filters.to && data.group === filters.group
    && data.channel === filters.channel && data.currency === filters.currency && (merchant === undefined || data.merchant === merchant) ? data : undefined;
  function change(values: Partial<typeof filters>) {
    if (Object.entries(values).every(([key, value]) => filters[key as keyof typeof filters] === value)) return;
    state.refresh();
    setFilters(previous => ({ ...previous, ...values }));
  }
  function preset(days: number) {
    const next = utcRange(days);
    setChosen(days);
    setDates(next);
    change(next);
  }
  function formSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setChosen(0);
    change({ ...dates, currency });
  }
  const columns = [l("Period", "周期"), t("Currency"), t("Direction"), l("New orders · Amount", "新建笔数 · 订单金额"),
    l("Successes · Principal", "成功笔数 · 本金"), l("Net credited", "净入账"), t("Quoted merchant fee"), l("Transferred allocation", "已出款分摊")];
  const amount = (value: string | null, row: Bucket) => value === null ? "—" :
    row.scale === null ? value + " " + row.currency + " · " + t("minor units") : exactMoney(value, row.currency, row.scale);

  return <section className="workspace-panel period-report">
    <div className="heading panel-heading"><h2>{l("Activity by period", "分周期经营数据")}</h2>
      <span className="muted small">UTC</span>
    </div>
    <div className="header-actions period-presets">
      {[[1, l("Today", "今日")], [7, l("Last 7 days", "近 7 天")], [30, l("Last 30 days", "近 30 天")],
        [90, l("Last 90 days", "近 90 天")]].map(([days, label]) =>
        <button type="button" className={chosen === days ? "primary" : "secondary"} aria-pressed={chosen === days}
          key={days} onClick={() => preset(Number(days))}>{label}</button>)}
    </div>
    <details className="period-advanced"><summary>{l("More filters", "更多筛选")}
      {(!chosen || filters.group !== "day" || filters.channel || filters.currency) &&
        <small> · {[!chosen && (filters.from + " – " + filters.to), filters.group !== "day" && (filters.group === "week" ? l("Weekly", "按周") : l("Monthly", "按月")), filters.channel, filters.currency].filter(Boolean).join(" · ")}</small>}
    </summary>
    <form className="platform-finance-filters period-filters" onSubmit={formSubmit}>
      <label>{t("Start date")}<input type="date" value={dates.from} required
        onChange={event => setDates({ ...dates, from: event.target.value })} /></label>
      <label>{t("End date")}<input type="date" value={dates.to} min={dates.from} required
        onChange={event => setDates({ ...dates, to: event.target.value })} /></label>
      <label>{l("Group by", "统计粒度")}<select id="report-period-group" value={filters.group} onChange={event => change({ group: event.target.value })}>
        <option value="day">{l("Day", "按日")}</option><option value="week">{l("Week", "按周")}</option><option value="month">{l("Month", "按月")}</option>
      </select></label>
      {merchant !== undefined && <label>{t("Channel")}<select value={filters.channel} onChange={event => change({ channel: event.target.value })}>
        <option value="">{l("All channels", "全部通道")}</option>
        {state.data?.channels.map(channel => <option key={channel.id} value={channel.id}>{channel.name} · {channel.id}</option>)}
      </select></label>}
      <label>{t("Currency")}<input value={currency} placeholder={l("All", "全部")} maxLength={3} pattern="[A-Z]{3}"
        onChange={event => setCurrency(event.target.value.toUpperCase())} /></label>
      <button className="secondary">{l("Apply date range", "应用筛选")}</button>
    </form>
    </details>
    <details className="period-basis muted small">
      <summary>{l("How this report is calculated", "统计口径")}</summary>
      <p>{l(
      "UTC periods. New orders use creation time, success uses confirmation time, credits use ledger time, and allocations use the recorded transfer date. Blank amounts have no supporting record.",
      "按 UTC 统计。新建按创建时间，成功按确认时间，入账按账本时间，出款分摊按登记的转账日期。横线表示该周期没有对应记录。")}</p>
    </details>
    <LoadState state={{ ...state, data: currentReport }} label={l("Activity by period", "分周期经营数据")}>{report =>
      report.rows.length ? <WorkspaceTable label={l("Activity by period", "分周期经营数据")} columns={columns}>
        {report.rows.map(row => <tr key={[row.period_start, row.currency, row.direction].join("/")}>
          <th scope="row" data-label={columns[0]}>{row.period_start}{row.period_end !== row.period_start && " – " + row.period_end}</th>
          <td data-label={columns[1]}>{row.currency}</td><td data-label={columns[2]}>{t(row.direction === "collection" ? "Collection" : "Payout")}</td>
          <td data-label={columns[3]}>{row.orders} · {amount(row.requested_minor, row)}</td>
          <td data-label={columns[4]}>{row.succeeded} · {amount(row.successful_minor, row)}</td>
          <td data-label={columns[5]}>{amount(row.credited_minor, row)}</td>
          <td data-label={columns[6]}>{amount(row.quoted_fee_minor, row)}</td>
          <td data-label={columns[7]}>{amount(row.transferred_minor, row)}</td>
        </tr>)}
      </WorkspaceTable> : <p className="muted empty">{l("No recorded activity in this range.", "这个范围内没有经营数据。")}</p>}
    </LoadState>
  </section>;
}
