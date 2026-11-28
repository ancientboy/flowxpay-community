import { ApiError } from "../api";
import fixtures from "./fixtures.json";

const snapshots: Record<string, unknown> = fixtures;
const ops = "/sandbox/ops";
const workspace = "/sandbox/workspace";
const readOnly = "The online demo is read-only. Start the local sandbox to make changes.";

type Order = { id: string; merchant_reference?: string; reference?: string };
function filterOrders<T extends Order>(rows: T[], search: string | null) {
  return search ? rows.filter(row => row.id === search || row.reference === search || row.merchant_reference === search) : rows;
}

export function readDemo<T>(path: string, init: RequestInit): T {
  init.signal?.throwIfAborted();
  const origin = "https://community-demo.invalid";
  const url = new URL(path, origin);
  if (url.origin !== origin) throw new ApiError(404, "This data is unavailable in the online demo.");

  const method = init.method ?? "GET";
  const endpoint = url.pathname;
  if (method !== "GET") {
    if (method === "POST" && (endpoint === ops + "/logout" || endpoint === workspace + "/auth/logout")) return undefined as T;
    throw new ApiError(403, readOnly);
  }

  const query = url.searchParams;
  const reference = query.get("reference");
  if (endpoint === ops + "/dashboard") {
    const filters = new URLSearchParams();
    for (const key of ["direction", "tasks", "merchant"]) {
      const value = query.get(key);
      if (value) filters.set(key, value);
    }
    const dashboard = snapshots[endpoint + (filters.size ? "?" + filters : "")] as
      { payments: Order[] } | undefined;
    if (dashboard) return { ...dashboard, payments: filterOrders(dashboard.payments, reference) } as T;
  }
  if (endpoint === ops + "/operations" && query.get("merchant")) {
    const key = endpoint + "?" + new URLSearchParams({ merchant: query.get("merchant")! });
    if (Object.hasOwn(snapshots, key)) return snapshots[key] as T;
  }
  if (endpoint === ops + "/merchants") {
    const q = (query.get("q") ?? "").toLowerCase();
    return { merchants: fixtures["/sandbox/ops/merchants"].merchants.filter(
      merchant => merchant.name.toLowerCase().includes(q) || merchant.id.toLowerCase().includes(q),
    ) } as T;
  }
  if (endpoint === "/v1/payouts") return filterOrders(fixtures["/v1/payouts"], reference) as T;
  if (endpoint.startsWith("/v1/payouts/")) {
    const order = fixtures["/v1/payouts"].find(row => endpoint === "/v1/payouts/" + row.id);
    if (order) return order as T;
    throw new ApiError(404, "Payout not found.");
  }
  if (endpoint === workspace + "/payments") {
    return filterOrders(fixtures["/sandbox/workspace/payments"], reference) as T;
  }
  if (endpoint === workspace + "/overview") {
    const key = endpoint + "?" + new URLSearchParams({ currency: query.get("currency") ?? "", days: query.get("days") ?? "" });
    if (Object.hasOwn(snapshots, key)) return snapshots[key] as T;
  }
  if (endpoint.startsWith(workspace + "/settlement/") && endpoint.endsWith("/totals")) {
    const key = Object.keys(snapshots).find(key => key.startsWith(endpoint + "?"));
    if (key) return snapshots[key] as T;
  }
  if (endpoint === ops + "/finance" && query.get("from") && query.get("to")) {
    const report = fixtures["/sandbox/ops/finance"];
    if (query.get("from")! > report.to || query.get("to")! < report.from) {
      return { ...report, from: query.get("from"), to: query.get("to"), summary: [], payouts: [], settlements: [] } as T;
    }
  }
  if (Object.hasOwn(snapshots, endpoint)) return snapshots[endpoint] as T;
  throw new ApiError(404, "This data is unavailable in the online demo.");
}
