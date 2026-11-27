package sitedomain

import (
	"net"
	"regexp"
	"strings"
)

var domainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func Valid(domain string) bool {
	if len(domain) > 253 || net.ParseIP(domain) != nil {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 || !strings.ContainsAny(labels[len(labels)-1], "abcdefghijklmnopqrstuvwxyz") {
		return false
	}
	for _, label := range labels {
		if !domainLabel.MatchString(label) {
			return false
		}
	}
	return true
}
