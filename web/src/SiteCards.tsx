import { useEffect, useRef } from "react";
import { ArrowRight } from "@phosphor-icons/react";
import { useLocale } from "./locale";

export interface Site {
  id: string;
  name: string;
  is_default: boolean;
  business_domain: string | null;
  merchant_id?: string;
  merchant_name?: string;
}

const methodNames: Record<string, string> = { pix: "Pix", card: "Card", gateway: "India gateway" };

type Method = { market: string; currency: string; method: string };

export interface SiteCard extends Site {
  merchant_id: string;
  merchant_name: string;
  assigned_accounts: number;
  available_accounts: number;
  methods: Method[];
  proposals: (Method & { available: boolean })[];
}

export function SiteExplanation() {
  const { t } = useLocale();
  return <p className="muted panel-note">{t("A site groups a merchant’s collection settings by store, website or business line. For one collection setup, use the default site created when your account was opened.")}</p>;
}

export function siteStatus(site: SiteCard) {
  if (site.methods.length) return "Ready for test orders";
  if (site.proposals.some(proposal => proposal.available)) return "Awaiting approval";
  if (site.proposals.length) return "Proposed channel unavailable";
  if (site.available_accounts) return "Assigned · awaiting routing";
  if (site.assigned_accounts) return "Assigned channels disabled";
  return "Channel not assigned";
}

export function SiteFacts({ site }: { site: Site | SiteCard }) {
  const { t } = useLocale();
  const format = ({ market, currency, method }: Method) => [market, currency, t(methodNames[method] ?? method)].join(" · ");
  return <dl className="site-card-facts">
    <div><dt>{t("Business domain")}</dt><dd className="wrap">{site.business_domain ?? t("Not configured")}</dd></div>
    {"methods" in site && <><div><dt>{t("Enabled payment methods")}</dt><dd>{site.methods.length
      ? site.methods.map(method => <span key={format(method)}>{format(method)}</span>)
      : t("No active payment methods")}</dd></div>
    {site.proposals.length > 0 && <div><dt>{t("Proposed methods")}</dt><dd>
      {site.proposals.map((proposal, index) => <span key={index}>{format(proposal)} · {t(proposal.available
        ? "Awaiting approval" : "Proposed channel unavailable")}</span>)}
    </dd></div>}</>}
  </dl>;
}

export function SiteCards<T extends Site | SiteCard>({ sites, platform, open, action, focused }: {
  sites: T[];
  platform?: boolean;
  open?: (site: T) => void;
  action?: string;
  focused?: string;
}) {
  const { t } = useLocale();
  const focus = useRef<HTMLLIElement>(null);
  useEffect(() => {
    if (focused) focus.current?.focus();
  }, [focused]);

  return sites.length ? <ul className="platform-card-grid platform-merchant-cards site-card-grid">
    {sites.map(site => <li key={site.id} className="platform-card" tabIndex={site.id === focused ? -1 : undefined}
      ref={site.id === focused ? focus : undefined}>
      <div className="platform-card-heading">
        <div className="platform-card-identity wrap">
          <h3>{site.name}</h3>
          {platform && site.merchant_name && <span className="muted small">{site.merchant_name} · {site.merchant_id}</span>}
        </div>
        <div className="header-actions">
          {site.is_default && <span className="tag gray">{t("Default site")}</span>}
          {"methods" in site && <SiteRoutingStatus site={site} />}
        </div>
      </div>
      <SiteFacts site={site} />
      {open && action && <button className="text-button panel-link platform-card-link" onClick={() => open(site)}
        aria-label={t("{action} for {site}", { action: t(action), site: site.name })}>
        {t(action)}<ArrowRight size={16} aria-hidden="true" />
      </button>}
    </li>)}
  </ul> : <p className="muted empty">{t("No sites to configure.")}</p>;
}

function SiteRoutingStatus({ site }: { site: SiteCard }) {
  const { t } = useLocale();
  return <span className={site.methods.length ? "tag green" : "tag blue"}>{t(siteStatus(site))}</span>;
}
