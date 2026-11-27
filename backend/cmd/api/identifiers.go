package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	numberAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	merchantLength = 12
	resourceLength = 24
	paymentPrefix  = "pay_"
	payoutPrefix   = "po_"
	idAttempts     = 3
)

var errIDAllocation = errors.New("could not allocate a platform identifier")

func newPlatformNumber(prefix string, length int) string {
	var random [resourceLength]byte
	suffix := random[:length]
	rand.Read(suffix)
	for i, b := range suffix {
		suffix[i] = numberAlphabet[b&31]
	}
	return prefix + string(suffix)
}

func newResourceID(prefix string) string {
	return newPlatformNumber(prefix, resourceLength)
}

func validCheckoutPaymentID(id string) bool {
	suffix, ok := strings.CutPrefix(id, paymentPrefix)
	if !ok {
		return false
	}
	switch len(suffix) {
	case resourceLength:
		for i := range len(suffix) {
			if strings.IndexByte(numberAlphabet, suffix[i]) < 0 {
				return false
			}
		}
		return true
	case 32:
		_, err := hex.DecodeString(suffix)
		return err == nil
	default:
		return false
	}
}
