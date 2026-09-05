package webapp

import (
	"crypto/subtle"
	"net"
	"strings"
)

func validPushAuth(header, secret string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	provided := header[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) == 1
}

// validCollectorAuth is validPushAuth extended with one exception, for the
// single-machine default a saCollector started with no secret at all
// relies on: if the evaluator likewise has no secret configured, a
// request that genuinely originates from loopback is trusted without one
// — the same trust boundary the evaluator's own local-only surfaces
// already rely on, not something new. trustProxy must be off for this to
// apply at all: behind a reverse proxy, remoteAddr is the proxy's own
// address, not the real caller's, so it can never stand in for "this is
// actually local" once a proxy is in the picture.
func validCollectorAuth(header, secret, remoteAddr string, trustProxy bool) bool {
	if secret != "" {
		return validPushAuth(header, secret)
	}
	if trustProxy || header != "" {
		return false
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
