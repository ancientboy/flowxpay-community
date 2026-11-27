import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { messages } from "./messages";

const languages = {
  "en-US": "English",
  "zh-CN": "简体中文",
};
type Locale = keyof typeof languages;
const defaultLocale: Locale = "en-US";
const localeKey = "flowxpay.locale";

export function resolveLocale(value: unknown): Locale {
  return typeof value === "string" && Object.hasOwn(languages, value) ? value as Locale : defaultLocale;
}

export function translate(locale: Locale, source: string, values: Record<string, string> = {}) {
  const text = locale === "zh-CN" && Object.hasOwn(messages, source) ? messages[source] : source;
  return text.replace(/\{(\w+)\}/g, (placeholder, key: string) => Object.hasOwn(values, key) ? values[key] : placeholder);
}

function storedLocale() {
  try {
    const saved = window.localStorage.getItem(localeKey);
    const locale = resolveLocale(saved);
    if (saved !== null && saved !== locale) window.localStorage.setItem(localeKey, locale);
    return locale;
  } catch {
    return defaultLocale;
  }
}

const LocaleContext = createContext<{ locale: Locale; setLocale: (locale: Locale) => void }>({ locale: defaultLocale, setLocale: () => {} });

export function LocaleProvider({ children }: { children: ReactNode }) {
  const [locale, updateLocale] = useState(() => {
    const requested = new URLSearchParams(window.location.search).get("lang");
    return requested === null ? storedLocale() : resolveLocale(requested);
  });

  useEffect(() => { document.documentElement.lang = locale; }, [locale]);

  function setLocale(next: Locale) {
    updateLocale(next);
    try {
      window.localStorage.setItem(localeKey, next);
    } catch {
      // Keep the selected locale in memory if storage is unavailable.
    }
    const url = new URL(window.location.href);
    url.searchParams.set("lang", next);
    window.history.replaceState(window.history.state, "", url);
  }

  return <LocaleContext.Provider value={{ locale, setLocale }}>{children}</LocaleContext.Provider>;
}

function currencyFormatter(locale: Locale, currency: string, isoScale: boolean | number = false) {
  const digits = typeof isoScale === "number" ? isoScale : 2;
  return new Intl.NumberFormat(locale, {
    style: "currency", currency, currencyDisplay: "code",
    ...(isoScale !== true && { minimumFractionDigits: digits, maximumFractionDigits: digits }),
  });
}

export function useLocale() {
  const { locale, setLocale } = useContext(LocaleContext);
  const t = (source: string, values?: Record<string, string>) => translate(locale, source, values);
  const money = (amount: number, currency: string) => currencyFormatter(locale, currency).format(amount / 100);

  function exactMoney(value: string, currency: string, isoScale: boolean | number = false) {
    const format = currencyFormatter(locale, currency, isoScale);
    const digits = format.resolvedOptions().maximumFractionDigits ?? 2;
    const scale = 10n ** BigInt(digits);
    const minor = BigInt(value);
    const whole = minor / scale;
    const fraction = ((minor < 0n ? -minor : minor) % scale).toString().padStart(digits, "0");
    const parts = format.formatToParts(minor < 0n && whole === 0n ? -0 : whole);
    const formatted = parts.map(part => part.type === "fraction" ? fraction : part.value).join("");
    return locale === "en-US" ? formatted.replace(" ", " ").replace("-", "−") : formatted;
  }

  const dateTime = (value: string) => new Intl.DateTimeFormat(locale, {
    year: "numeric", month: "short", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit",
    hourCycle: "h23", timeZone: "UTC",
  }).format(new Date(value)) + " UTC";
  const date = (value: string) => new Intl.DateTimeFormat(locale, {
    year: "numeric", month: "short", day: "2-digit", timeZone: "UTC",
  }).format(new Date(value));

  function localizedLink(href: string) {
    const url = new URL(href, window.location.origin);
    if (url.origin !== window.location.origin) return href;
    url.searchParams.set("lang", locale);
    return url.pathname + url.search + url.hash;
  }

  return { locale, setLocale, t, money, exactMoney, dateTime, date, localizedLink };
}

export function LanguageSelect() {
  const { locale, setLocale, t } = useLocale();
  return <select id="flowxpay-language" className="language-select" aria-label={t("Language")} value={locale}
    onChange={event => setLocale(resolveLocale(event.target.value))}>
    {Object.entries(languages).map(([value, name]) => <option key={value} value={value} lang={value}>{name}</option>)}
  </select>;
}
