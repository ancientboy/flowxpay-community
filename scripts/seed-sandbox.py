"""Create illustrative checkout rows through the local simulated API."""
import json
import os
from urllib.request import Request, urlopen

origin = os.environ["FLOWXPAY_API_ORIGIN"]

# A signed sandbox can start without fixture orders. Seeding must not require the merchant's private key.
if "M10028" in json.loads(os.environ.get("FLOWXPAY_SANDBOX_REQUEST_PUBLIC_KEYS") or "{}"):
    raise SystemExit(0)


def send(path, payload, extra_headers):
    headers = {"Content-Type": "application/json", **extra_headers}
    request = Request(origin + path, json.dumps(payload).encode(), headers, method="POST")
    with urlopen(request, timeout=5) as response:
        return json.load(response)


for reference, market, currency, method, minor, paid in [
    ("MP-104236", "US", "USD", "card", 7900, True),
    ("MP-104238", "BR", "BRL", "pix", 129000, False),
    ("MP-104239", "US", "USD", "card", 24900, True),
]:
    payment = send(
        "/v1/payments",
        {"merchant_reference": reference, "market": market, "currency": currency,
         "method": method, "amount_minor": minor},
        {"Authorization": "Bearer " + os.environ["FLOWXPAY_SANDBOX_KEY"],
         "Idempotency-Key": "seed-" + reference},
    )
    if paid:
        send("/sandbox" + payment["checkout_path"] + "/confirm", {"confirm": True}, {})
