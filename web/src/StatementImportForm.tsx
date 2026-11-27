import { useEffect, useRef, useState, type FormEvent } from "react";
import { Dialog } from "./Dialog";
import { useLocale } from "./locale";

const fields = ["entry_id", "upstream_reference", "order_id", "direction", "gross_minor", "fee_minor", "net_minor"];
type Route = { channel_id: string; channel_name: string; currency: string };
type Preview = {
  start_at: string; end_at: string; batch_already_exists: boolean; rows: number;
  by_direction: { direction: "collection" | "payout"; rows: number; gross_minor: string; differences: number; difference_minor: string; missing: number; missing_minor: string }[];
};
type Submission = { file: File; query: URLSearchParams; preview: Preview; token: string };

export function StatementImportForm({ url, scopes, close, refresh }: { url: string; scopes: Route[]; close: () => void; refresh: () => void }) {
  const { locale, dateTime, exactMoney } = useLocale();
  const l = (cn: string, en: string) => locale === "zh-CN" ? cn : en;
  const [headers, setHeaders] = useState<string[]>([]);
  const [submission, setSubmission] = useState<Submission>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const pending = useRef(false);
  const fileRevision = useRef(0);
  const previewHeading = useRef<HTMLHeadingElement>(null);
  useEffect(() => { if (submission) previewHeading.current?.focus(); }, [submission]);

  async function post(file: File, query: URLSearchParams) {
    return fetch(url + "&" + query, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "text/csv; charset=utf-8" }, body: file });
  }

  async function preview(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current) return;
    const values = new FormData(event.currentTarget);
    const file = values.get("file") as File;
    const [channel_id, currency] = String(values.get("scope")).split("/");
    const query = new URLSearchParams({ channel_id, currency, batch_id: String(values.get("batch")).trim(),
      day: String(values.get("day")), zone: String(values.get("zone")).trim(), complete: String(values.get("complete") === "on") });
    if (headers.length) query.set("mapping", JSON.stringify(fields.map(field => String(values.get("map-" + field)))));
    pending.current = true;
    setBusy(true);
    setError("");
    try {
      const response = await post(file, new URLSearchParams({ ...Object.fromEntries(query), action: "preview" }));
      if (response.ok) {
        const result = await response.json() as { preview: Preview; token: string };
        setSubmission({ file, query, preview: result.preview, token: result.token });
      } else {
        setError(l("预览失败，请核对商户通道、账单日期、时区、CSV 格式及重复明细。", "Preview failed. Check the channel, statement date, time zone, CSV format and duplicate entries."));
      }
    } catch {
      setError(l("无法读取预览，请重试。", "Preview unavailable. Try again."));
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }

  async function confirm() {
    if (!submission || pending.current || uncertain) return;
    pending.current = true;
    setBusy(true);
    setError("");
    try {
      const query = new URLSearchParams(submission.query);
      query.set("preview_token", submission.token);
      const response = await post(submission.file, query);
      if (response.ok) {
        refresh();
        close();
      } else if (response.status >= 500) {
        setUncertain(true);
      } else if (response.status === 409) {
        setSubmission(undefined);
        setError(l("预览范围已变化或批号已被其他内容使用，请重新预览。", "The preview changed or the batch ID is in use. Preview again."));
      } else {
        setError(l("导入未完成，请检查账单和当前权限。", "Import was rejected. Check the statement and your current access."));
      }
    } catch {
      setUncertain(true);
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }

  const scope = scopes.find(item => item.channel_id === submission?.query.get("channel_id") && item.currency === submission.query.get("currency"));
  const currency = submission?.query.get("currency") ?? "";
  const money = (value: string) => exactMoney(value, currency, true);
  return <Dialog drawer title={l("导入上游账单", "Import upstream statement")} locked={busy} close={() => { if (uncertain) refresh(); close(); }}>
    <div hidden={Boolean(submission)}>
      <form className="payment-form statement-import-form" onSubmit={preview}>
        <fieldset className="workspace-fields" disabled={busy}>
          <label>{l("通道和币种", "Channel and currency")}<select id="statement-import-scope" name="scope" required>
            <option value="">{l("选择通道", "Select channel")}</option>
            {scopes.map(row => <option key={row.channel_id + "/" + row.currency} value={row.channel_id + "/" + row.currency}>{row.channel_name} · {row.currency}</option>)}
          </select></label>
          <label>{l("账单批号", "Batch ID")}<input required id="statement-import-batch" name="batch" maxLength={120} /></label>
          <label>{l("账单日期", "Statement date")}<input required type="date" id="statement-import-day" name="day" /></label>
          <label>{l("上游账单时区", "Upstream statement time zone")}<input id="statement-import-zone" name="zone" required maxLength={100} autoComplete="off" /></label>
          <p className="muted">{l("填写上游账单注明的 IANA 时区。完整日核对按该时区划定起止时间。", "Enter the IANA time zone specified by the provider. It determines the complete-day comparison window.")}</p>
          <label>CSV<input type="file" name="file" accept=".csv,text/csv" required onChange={async event => {
            const file = event.target.files?.[0];
            const revision = ++fileRevision.current;
            setHeaders([]);
            if (!file) return;
            const first = (await file.slice(0, 8192).text()).replace(/^\uFEFF/, "").split(/\r?\n/)[0];
            const names = (first.match(/("(?:[^"]|"")*"|[^,]+)(?=,|$)/g) ?? []).map(value => value.replace(/^"|"$/g, "").replaceAll('""', '"'));
            if (revision === fileRevision.current) setHeaders(names);
          }} /></label>
          <p>{l("UTF-8，金额使用原币最小单位。", "UTF-8 with amounts in minor units.")}</p>
          <code className="wrap">{fields.join(",")}</code>
          {headers.length > 0 && <details key={headers.join(",")} open={fields.some(field => !headers.includes(field))}>
            <summary>{l("匹配上游文件列", "Map upstream columns")}</summary>
            {fields.map(field => <label key={field}>{field}<select required name={"map-" + field} defaultValue={headers.includes(field) ? field : ""}>
              <option disabled value="">{l("选择列", "Select column")}</option>
              {[...new Set(headers)].map(header => <option key={header}>{header}</option>)}
            </select></label>)}
          </details>}
          <label className="platform-checkbox"><input type="checkbox" name="complete" />
            {l("文件覆盖该商户、通道及币种一整天的代收和代付。检查平台单边缺单。", "The file contains all collections and payouts for this merchant, channel and currency for the full day. Check for missing provider rows.")}
          </label>
        </fieldset>
        <button className="primary" disabled={busy}>{busy ? l("正在生成预览…", "Preparing preview…") : l("预览导入", "Preview import")}</button>
      </form>
    </div>
    {submission && <section aria-label={l("导入预览", "Import preview")}>
      <h3 ref={previewHeading} tabIndex={-1}>{l("导入预览", "Import preview")}</h3>
      <p>{scope?.channel_name ?? submission.query.get("channel_id")} · {currency} · {submission.query.get("batch_id")}</p>
      <p>{submission.query.get("day")} · {submission.query.get("zone")}</p>
      <p>{l("检查时间范围", "Comparison window")} {dateTime(submission.preview.start_at)} – {dateTime(submission.preview.end_at)} {l("（结束时刻不含）", "(end excluded)")}</p>
      <p role="status">{l("预览尚未入库。最终匹配和凭证校验在确认时执行。", "Preview has not been saved. Final matching and evidence checks run on confirmation.")}</p>
      {submission.preview.batch_already_exists && <p className="panel-note">{l("这个批号已存在。仅完全相同的账单可重复提交。", "This batch ID exists. Only identical data can be submitted again.")}</p>}
      {submission.query.get("complete") !== "true" && <p>{l("本次不检查平台单边缺单。", "Platform-only missing rows are not checked for this file.")}</p>}
      <div className="statement-preview-totals">
        {submission.preview.by_direction.map(row => <section key={row.direction}>
          <h4>{row.direction === "collection" ? l("代收", "Collection") : l("代付", "Payout")}</h4>
          <dl>
            <dt>{l("文件笔数 / 本金", "Rows / gross")}</dt><dd>{row.rows} · {money(row.gross_minor)}</dd>
            <dt>{l("疑似差异 / 金额", "Likely discrepancies / amount")}</dt><dd>{row.differences} · {money(row.difference_minor)}</dd>
            {submission.query.get("complete") === "true" && <>
              <dt>{l("疑似缺单 / 金额", "Likely missing / amount")}</dt><dd>{row.missing} · {money(row.missing_minor)}</dd>
            </>}
          </dl>
        </section>)}
      </div>
      <div className="detail-actions">
        {uncertain ? <button type="button" className="secondary" onClick={() => { refresh(); close(); }}>{l("关闭并核对批次", "Close and check batch list")}</button> : <>
          <button type="button" className="secondary" disabled={busy} onClick={() => { setSubmission(undefined); setError(""); }}>{l("返回修改", "Back to edit")}</button>
          <button type="button" className="primary" disabled={busy} onClick={() => void confirm()}>{busy ? l("正在导入…", "Importing…") : l("确认导入", "Confirm import")}</button>
        </>}
      </div>
      {uncertain && <p role="alert">{l("未能确认提交结果，请先核对原批号。", "The submission outcome is unknown. Check the original batch ID first.")}</p>}
    </section>}
    {error && <p role="alert" className="error">{error}</p>}
  </Dialog>;
}
