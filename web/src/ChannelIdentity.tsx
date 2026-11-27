import { useLocale } from "./locale";

export const channelProfiles = {
  mock_pix: { title: "Brazil Pix Collections", template: "Mock Pix", market: "BR", currency: "BRL", method: "pix" },
  mock_card: { title: "US Card Collections", template: "Mock Card", market: "US", currency: "USD", method: "card" },
  mock_in_redirect: { title: "India Gateway Collections (redirect)", template: "Mock India redirect", market: "IN", currency: "INR", method: "gateway" },
  mock_in_qr: { title: "India Gateway Collections (QR)", template: "Mock India QR", market: "IN", currency: "INR", method: "gateway" },
  mock_in_unknown: { title: "India Gateway Collections (unsupported action)", template: "Mock India unsupported", market: "IN", currency: "INR", method: "gateway" },
} as const;

export type Provider = keyof typeof channelProfiles;
export type Translate = ReturnType<typeof useLocale>["t"];
export interface ChannelIdentity {
  id: string;
  provider: string;
  environment?: string;
  currency?: string;
  name?: string;
  platform_channel_id?: string;
  platform_channel_name?: string;
}

function marker(id: string) {
  let hash = 2166136261;
  for (const char of id) hash = Math.imul(hash ^ char.charCodeAt(0), 16777619);
  return (hash >>> 0).toString(16).toUpperCase().padStart(8, "0");
}

const pricingNames: Record<Provider, string> = {
  mock_pix: "Brazil Pix",
  mock_card: "US card",
  mock_in_redirect: "India gateway (redirect)",
  mock_in_qr: "India gateway (QR)",
  mock_in_unknown: "India gateway (unsupported action)",
};

export function channelName(value: ChannelIdentity, t: Translate, pricing = false) {
  const name = value.platform_channel_name ?? value.name;
  if (value.environment === "production") return name || t("Channel");
  const profile = channelProfiles[value.provider as Provider];
  const alias = name && name !== profile?.template && name !== value.provider ? name : "";
  return [t(pricing ? pricingNames[value.provider as Provider] ?? "Channel" : profile?.title ?? "Channel"), alias].filter(Boolean).join(" · ");
}

export function channelMarker(value: ChannelIdentity) {
  return "#" + marker(value.platform_channel_id ?? value.id);
}

export function channelTitle(value: ChannelIdentity, t: Translate, pricing = false) {
  return channelName(value, t, pricing) + " · " + channelMarker(value);
}

export function channelSubtitle(provider: string, t: Translate, environment?: string, productionCurrency?: string) {
  if (environment === "production") return [productionCurrency, t("Production")].filter(Boolean).join(" · ");
  const currency = channelProfiles[provider as Provider]?.currency;
  return [currency, t("Sandbox")].filter(Boolean).join(" · ");
}

export function ChannelTechnical({ value, account = false, resource }: {
  value: ChannelIdentity; account?: boolean; resource?: string;
}) {
  const { t } = useLocale();
  const originalName = value.platform_channel_name ?? value.name;
  return <details className="channel-technical">
    <summary>{t("Technical details")}</summary>
    {account && <div>{t("Account ID")} <code>{value.id}</code></div>}
    {(!account || value.platform_channel_id) && <div>{t("Channel ID")} <code>{value.platform_channel_id ?? value.id}</code></div>}
    {value.provider && <div>{t("Provider")} <code>{value.provider}</code></div>}
    {originalName && <div>{t("Stored channel name")} <code>{originalName}</code></div>}
    {resource && <div>{t("Resource ID")} <code>{resource}</code></div>}
  </details>;
}
