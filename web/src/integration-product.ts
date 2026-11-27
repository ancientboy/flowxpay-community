export type IntegrationProduct = {
  product_id: string; market: string; currency: string; direction: "collection" | "payout";
  ready: boolean; method: string; permitted: boolean; min_amount_minor: number | null;
  max_amount_minor: number | null; scale: number | null;
  quote: { bps: number; fixed_minor: number } | null;
};
