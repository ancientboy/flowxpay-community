package requestsign

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Message returns the canonical bytes signed by a merchant for one request.
func Message(environment, merchantID, keyID, method, target, contentType, idempotencyKey, timestamp, nonce string, body []byte) []byte {
	digest := sha256.Sum256(body)
	return []byte(strings.Join([]string{"flowxpay-request-v1", environment, merchantID, keyID,
		method, target, contentType, hex.EncodeToString(digest[:]), idempotencyKey, timestamp, nonce}, "\n"))
}
