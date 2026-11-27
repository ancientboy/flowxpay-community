import type { ReactNode } from "react";
import { ChannelTechnical, channelMarker, channelName, channelProfiles, channelSubtitle, type ChannelIdentity, type Provider } from "./ChannelIdentity";
import { useLocale } from "./locale";

const marketNames: Record<string, string> = { BR: "Brazil", IN: "India", US: "United States", TH: "Thailand" };
const methodNames: Record<string, string> = { card: "Card", gateway: "Gateway", pix: "Pix", promptpay: "PromptPay", bank_transfer: "Bank transfer" };

interface ChannelCardEntry extends ChannelIdentity {
  enabled: boolean;
  permitted?: boolean;
  configured?: boolean;
  market?: string;
  method?: string;
  direction?: "collection" | "payout";
  directions?: ("collection" | "payout")[];
  merchant_name?: string;
  site_name?: string;
}

export function PlatformChannelCards<T extends ChannelCardEntry>({ channels, label, accounts = false, onToggle, onPricing, pricingSummary, onOpenAccount, accountAction }: {
  channels: T[];
  label: string;
  accounts?: boolean;
  onToggle?: (channel: T) => void;
  onPricing?: (channel: T) => void;
  pricingSummary?: (channel: T) => ReactNode;
  onOpenAccount?: (channel: T) => void;
  accountAction?: string;
}) {
  const { t, locale } = useLocale();
  if (!channels.length) return <p className="empty muted">
    {t(accounts ? "No channel accounts are available." : "No channels have been configured.")}
  </p>;

  if (!accounts && channels.every(channel => channel.environment === "production")) {
    const suppliers = new Map<string,T[]>();
    for (const channel of channels) {
      const rows=suppliers.get(channel.id) ?? [];
      rows.push(channel);
      suppliers.set(channel.id,rows);
    }
    return <div className="product-routes" role="group" aria-label={label}>{[...suppliers].map(([id,rows])=>
      <article className="platform-card product-route-card" key={id}><header><h3>{channelName(rows[0],t)}</h3><small className="muted resource-id">{id}</small></header>
      <span className={"tag " + (rows[0].configured ? "green" : "gray")}>{rows[0].configured
        ? (locale === "zh-CN" ? "已登记上游连接" : "Connection details registered")
        : (locale === "zh-CN" ? "待技术接入" : "Integration required")}</span>
      {rows.map(row=><section className="product-route-row" key={row.currency+"/"+row.method}>
        <div className="header-actions">{row.directions?.map(dir=><strong key={dir} className={"product-direction "+dir}>{dir==="collection"?"↙ ":"↗ "}{t(dir==="collection"?"Collection":"Payout")}</strong>)}</div>
        <p>{t(marketNames[row.market??""]??row.market??"")} · {row.currency} · {t(methodNames[row.method??""]??row.method??"")}</p>
        {pricingSummary?.(row)}
        {onPricing&&<button className="secondary" onClick={()=>onPricing(row)}>{t("Channel pricing")}</button>}
      </section>)}</article>)}</div>;
  }

  const groups = new Map<string, { market: string; method: string; entries: T[] }>();
  for (const channel of channels) {
    const profile = channelProfiles[channel.provider as Provider];
    const market = channel.market ?? profile?.market ?? "";
    const method = channel.method ?? profile?.method ?? "";
    const key = market + "/" + method;
    const group = groups.get(key);
    if (group) group.entries.push(channel);
    else groups.set(key, { market, method, entries: [channel] });
  }

  return <div className="platform-channel-groups" role="group" aria-label={label}>
    {[...groups].sort(([left], [right]) => left.localeCompare(right)).map(([key, { market, method, entries }]) => {
      const title = market ? t(marketNames[market] ?? market) + " · " + t(methodNames[method] ?? method) : t("Other channels");
      return <section className="platform-channel-group" aria-label={title} key={key}>
        <h3>{title}</h3>
        <ul className="platform-card-grid" role="list">
          {entries.map(channel => <li className="platform-card" key={[channel.id, channel.currency, channel.method, channel.direction].join(":")}>
            <div className="platform-card-heading">
              <div className="platform-card-identity">
                <h4 className="wrap">{channelName(channel, t, Boolean(onPricing))}</h4>
                <p className="muted small wrap">{channelSubtitle(channel.provider, t, channel.environment, channel.currency)} · {channelMarker(channel)}</p>
              </div>
              <span className={channel.enabled ? "tag green" : "tag gray"}>
                {accounts && channel.permitted !== undefined
                  ? t(channel.enabled ? "Ready for orders" : channel.permitted ? "Enabled · Not ready" : "Access disabled")
                  : t(channel.enabled ? (accounts ? "Enabled" : channel.configured === false ? "Upstream configuration pending" : "Available to assign") : "Disabled")}
              </span>
            </div>
            {accounts && <dl className="platform-card-meta">
              <div><dt>{t("Merchant")}</dt><dd className="wrap">{channel.merchant_name}</dd></div>
              {channel.site_name && <div><dt>{t("Site")}</dt><dd className="wrap">{channel.site_name}</dd></div>}
              {channel.direction && <div><dt>{t("Direction")}</dt><dd>{t(channel.direction === "collection" ? "Collection" : "Payout")}</dd></div>}
            </dl>}
            {pricingSummary?.(channel)}
            <div className="platform-card-footer">
              <ChannelTechnical value={channel} account={accounts} />
              {onOpenAccount && <button type="button" className="secondary" onClick={() => onOpenAccount(channel)}>{accountAction}</button>}
              {onPricing && <button type="button" className="secondary" onClick={() => onPricing(channel)}>{t("Channel pricing")}</button>}
              {onToggle && <button type="button" className="secondary" onClick={() => onToggle(channel)}>
                {t(channel.enabled ? "Disable channel" : "Enable channel")}
              </button>}
            </div>
          </li>)}
        </ul>
      </section>;
    })}
  </div>;
}
