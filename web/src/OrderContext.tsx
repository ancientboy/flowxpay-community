import { useEffect, useState } from "react";
import { useLocale } from "./locale";

export function useOrderClock() {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30000);
    return () => window.clearInterval(timer);
  }, []);
  return now;
}

const units = [["day", 86400], ["hour", 3600], ["minute", 60]] as const;
export function OrderTime({ at, now }: { at?: string | null; now: number }) {
  const { locale } = useLocale();
  const time = Date.parse(at ?? "");
  if (!Number.isFinite(time)) return <>—</>;
  const seconds = (time - now) / 1000;
  const unit = units.find(([, length]) => Math.abs(seconds) >= length);
  const relative = unit ? new Intl.RelativeTimeFormat(locale).format(Math.trunc(seconds / unit[1]), unit[0])
    : seconds > 0 ? new Intl.RelativeTimeFormat(locale).format(Math.ceil(seconds), "second")
    : locale === "zh-CN" ? "刚刚" : "Just now";
  const iso = new Date(time).toISOString();
  return <time className="order-time" dateTime={iso}>{relative}<small>{iso.slice(0, 10)} {iso.slice(11, 19)} UTC</small></time>;
}

type Handling = { manual_review?: boolean; checkout_needs_review?: boolean; reviewed_by?: string; reviewed_at?: string;
  attempted_at?: string | null; lookup_at?: string | null; cancelled_at?: string | null };
export function HandlingNote({ record }: { record: Handling }) {
  const { locale } = useLocale();
  const zh = locale === "zh-CN";
  let label = zh ? "暂无处理方式信息" : "Handling unavailable";
  if (record.cancelled_at) label = zh ? "人工取消" : "Manually cancelled";
  else if (record.checkout_needs_review) label = zh ? "人工登记付款码失效" : "Expired code recorded manually";
  else if (record.manual_review || (record.reviewed_at && record.reviewed_by)) {
    label = zh ? "人工审核" : "Manually reviewed";
    if (record.attempted_at) label += zh ? " · 系统已派单" : " · Automated dispatch attempted";
  }
  else if (record.lookup_at) label = zh ? "系统已自动查单" : "Automated lookup recorded";
  else if (record.attempted_at) label = zh ? "系统已自动派单" : "Automated dispatch attempted";
  return <small className="order-handling">{label}</small>;
}

export function elapsedTime(start: string, end: string | null | undefined, locale: string) {
  const seconds = Math.floor((Date.parse(end ?? "") - Date.parse(start)) / 1000);
  if (!Number.isFinite(seconds) || seconds < 0) return null;
  const unit = units.find(([, length]) => seconds >= length) ?? ["second", 1];
  return new Intl.NumberFormat(locale, {style: "unit", unit: unit[0], unitDisplay: "long"}).format(Math.floor(seconds / unit[1]));
}
