export type PaymentStatus = "requires_action" | "succeeded" | "expired" | "pending_confirmation" | "failed";

export interface Capability {
  market: "BR" | "US" | "IN";
  currency: "BRL" | "USD" | "INR";
  method: "pix" | "card" | "gateway";
}

export type NextAction =
  | { type: "redirect"; url: string }
  | { type: "display_qr"; qr_payload: string; qr_image_url: string }
  | { type: "unsupported" };

export interface FeeRate {
  bps: number; fixed_minor: number; version_id: string; source: "cost" | "default" | "override"; effective_at: string;
}

export interface OrderFee extends FeeRate {
  fee_minor: number;
}

export interface Payment extends Capability {
  id: string;
  merchant_reference: string;
  amount_minor: number;
  status: PaymentStatus;
  checkout_path?: string;
  checkout_url?: string;
  checkout_needs_review?: boolean;
  site_id?: string;
  route_snapshot?: { account_id: string; route_change_id?: string; platform_channel_id?: string };
  quote?: OrderFee | null;
  failure_code?: string;
  next_action?: NextAction;
  created_at: string;
  expires_at: string;
  paid_at?: string;
}

export interface Workspace {
  environment: "sandbox";
  request_signing_required?: boolean;
  merchant: { id: string; name: string };
  payments: Capability[];
  sites: { id: string; name: string; default: boolean; payments: Capability[] }[];
}

export interface Checkout extends Capability {
  merchant_name: string;
  reference: string;
  amount_minor: number;
  status: PaymentStatus;
  next_action?: NextAction;
  expires_at: string;
}

export class ApiError extends Error {
  constructor(public readonly status: number, message: string, public readonly code?: string) {
    super(message);
  }
}

const productionErrors: Record<string, string> = {
  platform_email_in_use: "This email address is already in use.",
  platform_member_not_manageable: "This platform member cannot be managed.",
  onboarding_authenticator_invalid: "Enter the current authenticator code. If you just used it, wait for the next one.",
  onboarding_authenticator_locked: "Too many verification attempts. Try again after the lockout.",
  recent_mfa_login_required: "Sign out and sign in with your authenticator, then retry within five minutes.",
  rate_version_changed: "The rate changed. Review the current version and reopen the editor.",
  default_rate_required: "Publish a default rate before setting a merchant rate.",
  merchant_login_conflict: "This email is already in use or first-time setup has been completed.",
  routing_setup_required: "Configure the upstream channel and its rates before enabling access.",
  routing_switch_unavailable: "The current route or target readiness changed. Check the routing table before retrying.",
  invalid_signing_key: "Enter a valid Ed25519 public key in unpadded Base64URL.",
  key_registration_conflict: "A key is already registered with this signing public key.",
  fund_action_conflict: "The funds record already has a different result.",
  payout_cancellation_unavailable: "The payout state or dispatch evidence no longer permits cancellation. Refresh the order.",
  upstream_receipt_required: "Confirm upstream receipt evidence for the settlement sources first.",
  fund_action_unavailable: "The balance or statement evidence does not allow this action.",
  settlement_batch_required: "Lock a settlement batch before recording a transfer.",
  settlement_reference_conflict: "This transfer reference was already recorded with a different amount or date.",
  invalid_settlement: "Check the settlement currency, amount, date and reference.",
  invalid_period_report_range: "Enter both dates in order, within 366 calendar days.",
  invalid_date_range: "Select up to 31 UTC calendar days.",
};

export async function request<T>(path: string, key?: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (key) headers.set("Authorization", `Bearer ${key}`);
  if (init.body) headers.set("Content-Type", "application/json");
  const response = await fetch(path, { ...init, headers, cache: "no-store" });
  if (response.status === 204) return undefined as T;
  if (!response.ok) {
    const data = await response.json().catch(() => undefined);
    const workspaceError = data?.error === "idempotency_conflict" ? "This request key was already used for different details." :
      data?.error === "invalid_business_domain" ? "Enter a domain such as shop.example.com." :
      data?.error === "member_email_conflict" ? "This email address is already in use." : undefined;
    throw new ApiError(response.status, data?.error?.message ?? workspaceError ?? productionErrors[data?.error] ?? "The request could not be completed.",
      typeof data?.error === "string" ? data.error : data?.error?.code);
  }
  return await response.json() as T;
}

export interface Refund {
  id: string;
  payment_id: string;
  currency: Capability["currency"];
  amount_minor: number;
  status: "pending_confirmation" | "succeeded" | "failed";
  failure_code?: string;
  created_at: string;
  updated_at: string;
}

export interface CollectionException {
  kind: "terminal_conflict" | "replay_conflict" | "webhook_failed" | "payment_confirmation_pending" | "refund_confirmation_pending";
  payment_id: string;
  subject_id: string;
  event_id?: string;
  at: string;
}

export function parseMinorUnits(value: string) {
  if (!/^\d{1,10}(?:[.,]\d{1,2})?$/.test(value)) return undefined;
  const [whole, fraction = ""] = value.split(/[.,]/);
  return Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
}
